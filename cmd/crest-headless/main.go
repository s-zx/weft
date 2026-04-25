// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/s-zx/crest/pkg/agent"
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/web/sse"
)

type HeadlessRequest struct {
	Prompt string `json:"prompt"`
	Mode   string `json:"mode,omitempty"`
	Model  string `json:"model,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
}

type HeadlessEvent struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
	Text string `json:"text,omitempty"`
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req HeadlessRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			emitEvent(HeadlessEvent{Type: "error", Text: fmt.Sprintf("invalid JSON: %v", err)})
			continue
		}
		if req.Prompt == "" {
			emitEvent(HeadlessEvent{Type: "error", Text: "prompt is required"})
			continue
		}
		if err := runHeadless(ctx, req); err != nil {
			emitEvent(HeadlessEvent{Type: "error", Text: err.Error()})
		}
		emitEvent(HeadlessEvent{Type: "done"})
	}
}

func runHeadless(ctx context.Context, req HeadlessRequest) error {
	modeName := req.Mode
	if modeName == "" {
		modeName = agent.ModeBench
	}
	mode, ok := agent.LookupMode(modeName)
	if !ok {
		return fmt.Errorf("unknown mode: %s", modeName)
	}

	cwd := req.Cwd
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
	}

	aiOpts := uctypes.AIOptsType{
		APIType: uctypes.APIType_AnthropicMessages,
		Model:   "claude-sonnet-4-5",
	}
	if req.Model != "" {
		aiOpts.Model = req.Model
	}

	apiToken := os.Getenv("ANTHROPIC_API_KEY")
	if apiToken == "" {
		apiToken = os.Getenv("CREST_API_KEY")
	}
	if apiToken == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY or CREST_API_KEY environment variable is required")
	}
	aiOpts.APIToken = apiToken

	endpoint := os.Getenv("CREST_API_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://api.anthropic.com"
	}
	aiOpts.Endpoint = endpoint
	aiOpts.ThinkingLevel = uctypes.ThinkingLevelMedium

	if _, err := aiusechat.GetBackendByAPIType(aiOpts.APIType); err != nil {
		return fmt.Errorf("backend init: %w", err)
	}

	chatID := uuid.New().String()
	sess := &agent.Session{
		ChatID: chatID,
		Mode:   mode,
		AIOpts: aiOpts,
		Cwd:    cwd,
		Ctx:    ctx,
	}

	msg := &uctypes.AIMessage{
		MessageId: uuid.New().String(),
		Parts:     []uctypes.AIMessagePart{{Type: uctypes.AIMessagePartTypeText, Text: req.Prompt}},
	}

	sseHandler := sse.MakeDiscardSSEHandlerCh(ctx)
	defer sseHandler.Close()

	opts := agent.AgentOpts{
		Session: sess,
		UserMsg: msg,
		AIOpts:  aiOpts,
	}

	emitEvent(HeadlessEvent{Type: "start", Data: map[string]string{
		"chatid": chatID,
		"mode":   modeName,
		"model":  aiOpts.Model,
	}})

	err := agent.RunAgent(ctx, sseHandler, "", opts)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}

	return nil
}

func emitEvent(event HeadlessEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Println(string(data))
}
