// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

// Package agent implements Crest's native coding agent. It is the *policy*
// layer on top of pkg/aiusechat's mechanism layer: it owns modes (ask/plan/do),
// approval rules, prompts, and Crest-aware tools. pkg/aiusechat handles
// provider routing, streaming, the step loop, and the approval registry.
//
// MUST NOT import pkg/agent from pkg/aiusechat — the dependency goes one way
// only.
package agent

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/web/sse"
)

const AgentChatStorePrefix = "agent:"
const AgentSourceName = "crest-agent"
const DefaultMaxAgentSteps = 50
const DefaultContextBudget = 100000

// AgentOpts bundles everything RunAgent needs for a single turn.
type AgentOpts struct {
	Session     *Session
	UserMsg     *uctypes.AIMessage
	AIOpts      uctypes.AIOptsType
	PlanContext string
}

// RunAgent drives one agent turn. It assembles a WaveChatOpts with the
// mode-specific system prompt, the tool list for that mode, and the terminal
// context, then hands control to aiusechat.WaveAIPostMessageWrap which owns
// streaming, step loop, and metrics.
func RunAgent(ctx context.Context, sseHandler *sse.SSEHandlerCh, clientID string, opts AgentOpts) error {
	systemPrompt := SystemPromptForMode(opts.Session.Mode)
	if termCtx := BuildTerminalContext(opts.Session); termCtx != "" {
		systemPrompt = append(systemPrompt, termCtx)
	}
	if skills := DiscoverSkills(opts.Session.Cwd); len(skills) > 0 {
		systemPrompt = append(systemPrompt, BuildSkillsContext(skills))
	}
	if opts.PlanContext != "" {
		systemPrompt = append(systemPrompt, "## Active Plan\nExecute the following plan step by step:\n\n"+opts.PlanContext)
	}

	chatOpts := uctypes.WaveChatOpts{
		ChatId:               AgentChatStorePrefix + opts.Session.ChatID,
		ClientId:             clientID,
		Config:               opts.AIOpts,
		Tools:                ToolsForMode(opts.Session),
		SystemPrompt:         systemPrompt,
		TabId:                opts.Session.TabID,
		AllowNativeWebSearch: false,
		Source:               AgentSourceName,
		MaxSteps:             DefaultMaxAgentSteps,
		ContextBudget:        DefaultContextBudget,
		MetricsCallback:      makeTrajectoryWriter(opts.Session.Cwd, opts.Session.ChatID),
	}

	return aiusechat.WaveAIPostMessageWrap(ctx, sseHandler, opts.UserMsg, chatOpts)
}

func makeTrajectoryWriter(cwd string, chatID string) func(*uctypes.AIMetrics) {
	return func(metrics *uctypes.AIMetrics) {
		if len(metrics.AuditLog) == 0 {
			return
		}
		dir := cwd
		if dir == "" {
			dir = os.TempDir()
		}
		trajDir := filepath.Join(dir, ".crest-trajectories")
		if err := os.MkdirAll(trajDir, 0755); err != nil {
			log.Printf("trajectory: mkdir failed: %v\n", err)
			return
		}
		filename := filepath.Join(trajDir, chatID+".json")
		trajectory := map[string]any{
			"schema":    "crest-trajectory-v1",
			"chatid":    metrics.ChatId,
			"model":     metrics.Usage.Model,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"duration":  metrics.RequestDuration,
			"usage": map[string]int{
				"input_tokens":  metrics.Usage.InputTokens,
				"output_tokens": metrics.Usage.OutputTokens,
			},
			"requests":    metrics.RequestCount,
			"tool_calls":  metrics.ToolUseCount,
			"tool_errors": metrics.ToolUseErrorCount,
			"had_error":   metrics.HadError,
			"events":      metrics.AuditLog,
		}
		data, err := json.MarshalIndent(trajectory, "", "  ")
		if err != nil {
			log.Printf("trajectory: marshal failed: %v\n", err)
			return
		}
		if err := os.WriteFile(filename, data, 0644); err != nil {
			log.Printf("trajectory: write failed: %v\n", err)
			return
		}
		log.Printf("trajectory: saved to %s\n", filename)
	}
}
