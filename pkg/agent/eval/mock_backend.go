// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/web/sse"
)

type MockMessage struct {
	ID   string
	Role string
}

func (m *MockMessage) GetMessageId() string { return m.ID }
func (m *MockMessage) GetRole() string      { return m.Role }
func (m *MockMessage) GetUsage() *uctypes.AIUsage { return nil }

type MockBackend struct {
	lock         sync.Mutex
	responses    []GoldenResponse
	responseIdx  int
	ToolsCalled  []string
	LastText     string
}

func MakeMockBackend(responses []GoldenResponse) *MockBackend {
	return &MockBackend{
		responses: responses,
	}
}

func (mb *MockBackend) RunChatStep(
	ctx context.Context,
	sseHandler *sse.SSEHandlerCh,
	chatOpts uctypes.WaveChatOpts,
	cont *uctypes.WaveContinueResponse,
) (*uctypes.WaveStopReason, []uctypes.GenAIMessage, error) {
	mb.lock.Lock()
	defer mb.lock.Unlock()

	if mb.responseIdx >= len(mb.responses) {
		return &uctypes.WaveStopReason{Kind: uctypes.StopKindDone}, nil, nil
	}

	resp := mb.responses[mb.responseIdx]
	mb.responseIdx++

	msgId := uuid.New().String()
	msg := &MockMessage{ID: msgId, Role: "assistant"}

	if resp.Text != "" {
		mb.LastText = resp.Text
	}

	if len(resp.ToolCalls) > 0 {
		var toolCalls []uctypes.WaveToolCall
		for _, tc := range resp.ToolCalls {
			mb.ToolsCalled = append(mb.ToolsCalled, tc.Name)
			callId := uuid.New().String()
			toolCalls = append(toolCalls, uctypes.WaveToolCall{
				ID:    callId,
				Name:  tc.Name,
				Input: tc.Input,
			})
		}
		return &uctypes.WaveStopReason{
			Kind:      uctypes.StopKindToolUse,
			ToolCalls: toolCalls,
		}, []uctypes.GenAIMessage{msg}, nil
	}

	return &uctypes.WaveStopReason{Kind: uctypes.StopKindDone}, []uctypes.GenAIMessage{msg}, nil
}

func (mb *MockBackend) UpdateToolUseData(chatId string, toolCallId string, toolUseData uctypes.UIMessageDataToolUse) error {
	return nil
}

func (mb *MockBackend) RemoveToolUseCall(chatId string, toolCallId string) error {
	return nil
}

func (mb *MockBackend) ConvertToolResultsToNativeChatMessage(toolResults []uctypes.AIToolResult) ([]uctypes.GenAIMessage, error) {
	var msgs []uctypes.GenAIMessage
	for _, tr := range toolResults {
		msgs = append(msgs, &MockMessage{
			ID:   fmt.Sprintf("tool-result-%s", tr.ToolUseID),
			Role: "tool",
		})
	}
	return msgs, nil
}

func (mb *MockBackend) ConvertAIMessageToNativeChatMessage(message uctypes.AIMessage) (uctypes.GenAIMessage, error) {
	return &MockMessage{ID: message.MessageId, Role: "user"}, nil
}

func (mb *MockBackend) GetFunctionCallInputByToolCallId(aiChat uctypes.AIChat, toolCallId string) *uctypes.AIFunctionCallInput {
	return nil
}

func (mb *MockBackend) ConvertAIChatToUIChat(aiChat uctypes.AIChat) (*uctypes.UIChat, error) {
	return &uctypes.UIChat{ChatId: aiChat.ChatId}, nil
}
