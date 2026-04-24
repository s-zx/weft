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

	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/web/sse"
)

const AgentChatStorePrefix = "agent:"
const AgentSourceName = "crest-agent"

// AgentOpts bundles everything RunAgent needs for a single turn.
type AgentOpts struct {
	Session *Session
	UserMsg *uctypes.AIMessage
	AIOpts  uctypes.AIOptsType
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

	chatOpts := uctypes.WaveChatOpts{
		ChatId:               AgentChatStorePrefix + opts.Session.ChatID,
		ClientId:             clientID,
		Config:               opts.AIOpts,
		Tools:                ToolsForMode(opts.Session),
		SystemPrompt:         systemPrompt,
		TabId:                opts.Session.TabID,
		AllowNativeWebSearch: false,
		Source:               AgentSourceName,
	}

	return aiusechat.WaveAIPostMessageWrap(ctx, sseHandler, opts.UserMsg, chatOpts)
}
