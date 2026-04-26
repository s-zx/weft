// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package openaichat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/eventsource"
	"github.com/s-zx/crest/pkg/aiusechat/aiutil"
	"github.com/s-zx/crest/pkg/aiusechat/chatstore"
	"github.com/s-zx/crest/pkg/aiusechat/httpretry"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/wavebase"
	"github.com/s-zx/crest/pkg/web/sse"
)

// RunChatStep executes a chat step using the chat completions API
func RunChatStep(
	ctx context.Context,
	sseHandler *sse.SSEHandlerCh,
	chatOpts uctypes.WaveChatOpts,
	cont *uctypes.WaveContinueResponse,
) (*uctypes.WaveStopReason, []*StoredChatMessage, error) {
	if sseHandler == nil {
		return nil, nil, errors.New("sse handler is nil")
	}

	chat := chatstore.DefaultChatStore.Get(chatOpts.ChatId)
	if chat == nil {
		return nil, nil, fmt.Errorf("chat not found: %s", chatOpts.ChatId)
	}

	if chatOpts.Config.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(chatOpts.Config.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	// Convert stored messages to chat completions format
	var messages []ChatRequestMessage

	// Convert native messages
	for _, genMsg := range chat.NativeMessages {
		chatMsg, ok := genMsg.(*StoredChatMessage)
		if !ok {
			return nil, nil, fmt.Errorf("expected StoredChatMessage, got %T", genMsg)
		}
		messages = append(messages, *chatMsg.Message.clean())
	}

	req, err := buildChatHTTPRequest(ctx, messages, chatOpts)
	if err != nil {
		return nil, nil, err
	}

	client, err := aiutil.MakeHTTPClient(chatOpts.Config.ProxyURL)
	if err != nil {
		return nil, nil, err
	}
	resp, err := httpretry.Do(ctx, client, req, httpretry.DefaultConfig(), "openaichat")
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if wavebase.IsDevMode() {
		log.Printf("openaichat: response status=%d content-type=%q transfer-encoding=%q\n",
			resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Transfer-Encoding"))
	}
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Setup SSE if this is a new request (not a continuation)
	if cont == nil {
		if err := sseHandler.SetupSSE(); err != nil {
			return nil, nil, fmt.Errorf("failed to setup SSE: %w", err)
		}
	}

	// Stream processing
	stopReason, assistantMsg, err := processChatStream(ctx, resp.Body, sseHandler, chatOpts, cont)
	if err != nil {
		return nil, nil, err
	}

	return stopReason, []*StoredChatMessage{assistantMsg}, nil
}

func processChatStream(
	ctx context.Context,
	body io.Reader,
	sseHandler *sse.SSEHandlerCh,
	chatOpts uctypes.WaveChatOpts,
	cont *uctypes.WaveContinueResponse,
) (*uctypes.WaveStopReason, *StoredChatMessage, error) {
	decoder := eventsource.NewDecoder(body)
	var textBuilder strings.Builder
	var reasoningBuilder strings.Builder
	msgID := uuid.New().String()
	textID := uuid.New().String()
	reasoningID := uuid.New().String()
	var finishReason string
	textStarted := false
	reasoningStarted := false
	var toolCallsInProgress []ToolCall

	if cont == nil {
		_ = sseHandler.AiMsgStart(msgID)
	}
	_ = sseHandler.AiMsgStartStep()

	for {
		if err := ctx.Err(); err != nil {
			_ = sseHandler.AiMsgError("request cancelled")
			return &uctypes.WaveStopReason{
				Kind:      uctypes.StopKindCanceled,
				ErrorType: "cancelled",
				ErrorText: "request cancelled",
			}, nil, err
		}

		event, err := decoder.Decode()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			if sseHandler.Err() != nil {
				partialMsg := extractPartialTextMessage(msgID, textBuilder.String())
				return &uctypes.WaveStopReason{
					Kind:      uctypes.StopKindCanceled,
					ErrorType: "client_disconnect",
					ErrorText: "client disconnected",
				}, partialMsg, nil
			}
			if textBuilder.Len() > 0 || len(toolCallsInProgress) > 0 {
				log.Printf("openaichat: stream ended with error after receiving data: %v\n", err)
				break
			}
			_ = sseHandler.AiMsgError(err.Error())
			return &uctypes.WaveStopReason{
				Kind:      uctypes.StopKindError,
				ErrorType: "stream",
				ErrorText: err.Error(),
			}, nil, fmt.Errorf("stream decode error: %w", err)
		}

		data := strings.TrimSpace(event.Data())
		if data == "[DONE]" || data == "" {
			if data == "[DONE]" {
				break
			}
			continue
		}

		if wavebase.IsDevMode() {
			log.Printf("openaichat: raw-data len=%d data=%s\n", len(data), utilfn.TruncateString(data, 300))
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Printf("openaichat: failed to parse chunk: %v (data=%s)\n", err, utilfn.TruncateString(data, 100))
			continue
		}

		if wavebase.IsDevMode() && len(chunk.Choices) > 0 {
			c := chunk.Choices[0]
			log.Printf("openaichat: chunk content=%q reasoning=%q toolcalls=%d finish=%v\n",
				utilfn.TruncateString(c.Delta.Content, 50),
				utilfn.TruncateString(c.Delta.Reasoning, 50),
				len(c.Delta.ToolCalls),
				c.FinishReason)
		}

		if len(chunk.Choices) == 0 {
			if wavebase.IsDevMode() {
				log.Printf("openaichat: chunk with 0 choices: %s\n", utilfn.TruncateString(data, 200))
			}
			continue
		}

		choice := chunk.Choices[0]
		reasoning := choice.Delta.Reasoning
		if reasoning == "" {
			reasoning = choice.Delta.ReasoningContent
		}
		if reasoning != "" {
			if !reasoningStarted {
				_ = sseHandler.AiMsgReasoningStart(reasoningID)
				reasoningStarted = true
			}
			reasoningBuilder.WriteString(reasoning)
			_ = sseHandler.AiMsgReasoningDelta(reasoningID, reasoning)
		}
		if choice.Delta.Content != "" {
			if !textStarted {
				_ = sseHandler.AiMsgTextStart(textID)
				textStarted = true
			}
			textBuilder.WriteString(choice.Delta.Content)
			_ = sseHandler.AiMsgTextDelta(textID, choice.Delta.Content)
		}

		if len(choice.Delta.ToolCalls) > 0 {
			for _, tcDelta := range choice.Delta.ToolCalls {
				idx := tcDelta.Index
				for len(toolCallsInProgress) <= idx {
					toolCallsInProgress = append(toolCallsInProgress, ToolCall{Type: "function"})
				}

				tc := &toolCallsInProgress[idx]
				if tcDelta.ID != "" {
					tc.ID = tcDelta.ID
				}
				if tcDelta.Type != "" {
					tc.Type = tcDelta.Type
				}
				if tcDelta.Function != nil {
					if tcDelta.Function.Name != "" {
						tc.Function.Name = tcDelta.Function.Name
					}
					if tcDelta.Function.Arguments != "" {
						tc.Function.Arguments += tcDelta.Function.Arguments
					}
				}
			}
		}

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finishReason = *choice.FinishReason
		}
	}

	if reasoningStarted {
		_ = sseHandler.AiMsgReasoningEnd(reasoningID)
	}
	if textBuilder.Len() == 0 && reasoningBuilder.Len() > 0 {
		text := reasoningBuilder.String()
		if !textStarted {
			_ = sseHandler.AiMsgTextStart(textID)
			textStarted = true
		}
		textBuilder.WriteString(text)
		_ = sseHandler.AiMsgTextDelta(textID, text)
	}

	stopKind := uctypes.StopKindDone
	switch finishReason {
	case "length":
		stopKind = uctypes.StopKindMaxTokens
	case "tool_calls", "function_call":
		stopKind = uctypes.StopKindToolUse
	case "content_filter":
		stopKind = uctypes.StopKindContent
	}

	var validToolCalls []ToolCall
	for _, tc := range toolCallsInProgress {
		if tc.ID != "" && tc.Function.Name != "" {
			validToolCalls = append(validToolCalls, tc)
		}
	}

	if len(validToolCalls) > 0 && stopKind != uctypes.StopKindToolUse {
		stopKind = uctypes.StopKindToolUse
	}

	var waveToolCalls []uctypes.WaveToolCall
	if len(validToolCalls) > 0 {
		for _, tc := range validToolCalls {
			var inputJSON any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &inputJSON); err != nil {
					log.Printf("openaichat: failed to parse tool call arguments: %v\n", err)
					continue
				}
			}
			waveToolCalls = append(waveToolCalls, uctypes.WaveToolCall{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: inputJSON,
			})
		}
	}

	stopReason := &uctypes.WaveStopReason{
		Kind:      stopKind,
		RawReason: finishReason,
		ToolCalls: waveToolCalls,
	}

	assistantMsg := &StoredChatMessage{
		MessageId: msgID,
		Message: ChatRequestMessage{
			Role: "assistant",
		},
	}

	assistantMsg.Message.Content = textBuilder.String()
	if len(validToolCalls) > 0 {
		assistantMsg.Message.ToolCalls = validToolCalls
	}

	if textStarted {
		_ = sseHandler.AiMsgTextEnd(textID)
	}
	_ = sseHandler.AiMsgFinishStep()
	if stopKind != uctypes.StopKindToolUse {
		_ = sseHandler.AiMsgFinish(finishReason, nil)
	}

	return stopReason, assistantMsg, nil
}

func extractPartialTextMessage(msgID string, text string) *StoredChatMessage {
	if text == "" {
		return nil
	}

	return &StoredChatMessage{
		MessageId: msgID,
		Message: ChatRequestMessage{
			Role:    "assistant",
			Content: text,
		},
	}
}
