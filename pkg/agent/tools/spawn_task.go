// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/chatstore"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/web/sse"
)

const (
	SpawnTaskTimeout  = 600 * time.Second
	SpawnTaskMaxSteps = 30
)

type spawnTaskInput struct {
	Task string `json:"task"`
	Mode string `json:"mode,omitempty"`
}

type SpawnTaskConfig struct {
	ParentOpts    uctypes.AIOptsType
	ParentCtx     context.Context
	Cwd           string
	PromptForMode func(string) []string
	ToolsForMode  func(string) []uctypes.ToolDefinition
}

func SpawnTask(cfg SpawnTaskConfig, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "spawn_task",
		DisplayName: "Spawn Sub-Task",
		Description: "Delegate a scoped sub-task to a child agent with isolated conversation context. Returns the sub-agent's final response text. Use for independent sub-tasks like 'read and summarize this file' or 'find all TODO comments'. Multiple spawn_task calls in a single response run in parallel.",
		ToolLogName: "agent:spawn_task",
		Prompt: `spawn_task: Delegates an isolated sub-task to a child agent.
- Use when a sub-task would otherwise dump a large amount of intermediate context into your conversation — e.g. "read these 20 files and summarize", "find all TODOs across the repo", "scan the dependencies and list outdated ones". The child runs the search/reads/etc. and returns just the final answer.
- The child gets ZERO context from this conversation. Write the "task" prompt as if briefing a stranger — include relevant file paths, what to look for, and what to return.
- Mode: "ask" for read-only research (default), "do" for tasks that actually modify files. Don't use "do" for anything destructive without explicit user authorization.
- Multiple spawn_task calls in one response run in parallel — fan out for independent work, but don't fan out for steps that depend on each other.
- Use only when the cost of inlining the work would clearly exceed the cost of an extra round-trip. For a single read or grep, just do it directly.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "Task instruction for the sub-agent. Be specific — the sub-agent has no context from the parent conversation.",
				},
				"mode": map[string]any{
					"type":        "string",
					"enum":        []string{"ask", "do"},
					"default":     "ask",
					"description": "'ask' for read-only, 'do' for tasks that modify files.",
				},
			},
			"required":             []string{"task"},
			"additionalProperties": false,
		},
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, err := parseSpawnTaskInput(input)
			if err != nil {
				return fmt.Sprintf("spawn_task (invalid: %v)", err)
			}
			taskPreview := utilfn.TruncateString(parsed.Task, 60)
			if output != nil {
				return fmt.Sprintf("sub-task done: %q", taskPreview)
			}
			return fmt.Sprintf("running sub-task: %q", taskPreview)
		},
		ToolTextCallback: func(input any) (string, error) {
			parsed, err := parseSpawnTaskInput(input)
			if err != nil {
				return "", err
			}
			return runSpawnTask(parsed, cfg)
		},
		ToolApproval: approval,
	}
}

func parseSpawnTaskInput(input any) (*spawnTaskInput, error) {
	params := &spawnTaskInput{}
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}
	if err := utilfn.ReUnmarshal(params, input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	params.Task = strings.TrimSpace(params.Task)
	if params.Task == "" {
		return nil, fmt.Errorf("task is required")
	}
	if params.Mode == "" {
		params.Mode = "ask"
	}
	if params.Mode != "ask" && params.Mode != "do" {
		return nil, fmt.Errorf("mode must be 'ask' or 'do'")
	}
	return params, nil
}

func runSpawnTask(params *spawnTaskInput, cfg SpawnTaskConfig) (string, error) {
	parentCtx := cfg.ParentCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, SpawnTaskTimeout)
	defer cancel()

	chatID := "subtask:" + uuid.New().String()
	defer chatstore.DefaultChatStore.Delete(chatID)

	msg := &uctypes.AIMessage{
		MessageId: uuid.New().String(),
		Parts:     []uctypes.AIMessagePart{{Type: uctypes.AIMessagePartTypeText, Text: params.Task}},
	}

	backend, err := aiusechat.GetBackendByAPIType(cfg.ParentOpts.APIType)
	if err != nil {
		return "", fmt.Errorf("get backend: %w", err)
	}

	var systemPrompt []string
	if cfg.PromptForMode != nil {
		systemPrompt = cfg.PromptForMode(params.Mode)
	}

	var taskTools []uctypes.ToolDefinition
	if cfg.ToolsForMode != nil {
		taskTools = cfg.ToolsForMode(params.Mode)
	}
	// The sub-agent has no SSE channel to the user, so it cannot prompt for
	// approval. Force every tool to auto-approve inside the child; the parent
	// already approved the spawn_task call itself, which is the visible gate.
	for i := range taskTools {
		taskTools[i].ToolApproval = autoApprovedFn
	}

	chatOpts := uctypes.WaveChatOpts{
		ChatId:       chatID,
		Config:       cfg.ParentOpts,
		Tools:        taskTools,
		SystemPrompt: systemPrompt,
		Source:       "crest-subtask",
		MaxSteps:     SpawnTaskMaxSteps,
	}

	convertedMsg, err := backend.ConvertAIMessageToNativeChatMessage(*msg)
	if err != nil {
		return "", fmt.Errorf("convert message: %w", err)
	}

	if err := chatstore.DefaultChatStore.PostMessage(chatOpts.ChatId, &chatOpts.Config, convertedMsg); err != nil {
		return "", fmt.Errorf("post message: %w", err)
	}

	sseHandler := sse.MakeDiscardSSEHandlerCh(ctx)
	defer sseHandler.Close()

	metrics, runErr := aiusechat.RunAIChat(ctx, sseHandler, backend, chatOpts)

	// Extract the sub-agent's final assistant text. Without this the parent
	// only gets a metrics blob, which is useless — the parent's whole reason
	// to spawn was to learn what the sub-agent figured out. Falls back to a
	// metrics-only response if extraction fails so the parent still sees the
	// failure mode rather than nothing.
	finalText := extractFinalAssistantText(backend, chatOpts.ChatId)

	metricsLine := fmt.Sprintf("[subtask: %d steps, %d tool calls, in=%d/out=%d tokens, error=%v]",
		metrics.RequestCount, metrics.ToolUseCount,
		metrics.Usage.InputTokens, metrics.Usage.OutputTokens,
		metrics.HadError)

	if runErr != nil {
		if finalText != "" {
			return finalText + "\n\n" + metricsLine + "\nError: " + runErr.Error(), nil
		}
		return "", fmt.Errorf("sub-task failed: %w", runErr)
	}
	if finalText == "" {
		return metricsLine + "\n(sub-agent produced no final text — likely hit step budget or completed silently)", nil
	}
	return finalText + "\n\n" + metricsLine, nil
}

func extractFinalAssistantText(backend aiusechat.UseChatBackend, chatId string) string {
	chat := chatstore.DefaultChatStore.Get(chatId)
	if chat == nil {
		return ""
	}
	uiChat, err := backend.ConvertAIChatToUIChat(*chat)
	if err != nil || uiChat == nil {
		return ""
	}
	// Walk backwards: the last assistant message's text parts are the final reply.
	for i := len(uiChat.Messages) - 1; i >= 0; i-- {
		msg := uiChat.Messages[i]
		if msg.Role != "assistant" {
			continue
		}
		var b strings.Builder
		for _, p := range msg.Parts {
			if p.Type == "text" && p.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString(p.Text)
			}
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			return s
		}
	}
	return ""
}

func autoApprovedFn(any) string {
	return uctypes.ApprovalAutoApproved
}
