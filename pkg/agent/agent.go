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
	"sync"
	"time"

	"github.com/s-zx/crest/pkg/agent/permissions"
	"github.com/s-zx/crest/pkg/agent/tools"
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/web/sse"
)

const AgentChatStorePrefix = "agent:"
const AgentSourceName = "crest-agent"
const DefaultMaxAgentSteps = 50
const DefaultContextBudget = 100000

// permissionsEngine is the process-wide engine used by every agent
// turn. Initialized once on first use; the rule store is shared across
// chats so session rules from one chat don't leak into another (the
// store keys session rules by ChatId), but the builtin rule set and
// adapter registry are pure read-only state.
//
// Lazy init avoids paying the cost when the agent is never invoked
// (e.g. a process that only exposes non-agent endpoints).
var (
	permEngineOnce sync.Once
	permEngine     *permissions.Engine
)

// DefaultPermissionsEngine returns the process-wide engine, building
// it on first call. Wired from RunAgent; exposed so the future
// /permissions UI handler can reach the engine without a global var
// dance from a separate package.
func DefaultPermissionsEngine() *permissions.Engine {
	permEngineOnce.Do(func() {
		store := permissions.NewRuleStore()
		store.SetBuiltinRules(permissions.BuiltinRules())
		// TODO(p8c-followup): wire UserScopeBackend to wconfig so
		// `~/.config/waveterm/settings.json ai:permissions` rules
		// load. Until then, user scope is empty and only project
		// files + session rules + builtins fire.
		permEngine = permissions.NewEngine(store)
		permissions.RegisterDefaultAdapters(permEngine)
	})
	return permEngine
}

// resolvePosture returns the effective posture for a session. Order:
//  1. Explicit Session.Posture (set by the HTTP handler from request
//     body or `mode: "bench"` aliasing).
//  2. Otherwise, the engine's LoadDefaultPosture (reads UserScopeBackend
//     when wired; falls back to acceptEdits otherwise).
//  3. Otherwise, "acceptEdits" (the bundled default per design §5).
func resolvePosture(sess *Session, eng *permissions.Engine) permissions.Posture {
	if sess != nil && sess.Posture != "" {
		switch permissions.Posture(sess.Posture) {
		case permissions.PostureDefault,
			permissions.PostureAcceptEdits,
			permissions.PostureBypass,
			permissions.PostureBench:
			return permissions.Posture(sess.Posture)
		}
	}
	if eng != nil {
		return eng.LoadDefaultPosture()
	}
	return permissions.PostureAcceptEdits
}

// makeApprovalDecider builds the per-turn closure that the
// uctypes.WaveChatOpts.ApprovalDecider hook calls for every tool
// invocation. The closure captures cwd + chatId + a pointer to the
// session so the engine can run path-relative checks (acceptEdits)
// and load project rules.
//
// Posture is intentionally re-resolved on every call rather than
// captured at construction time. When Shift+Tab posture toggling
// lands on the FE, the user will be able to flip posture in the
// middle of an agent run — the toggle should take effect on the
// very next tool call, not "next user message" (which would require
// a fresh RunAgent invocation). Today this is a no-op since posture
// is set once by the HTTP handler, but it costs nothing and removes
// the silent-staleness footgun for whoever wires the toggle.
func makeApprovalDecider(eng *permissions.Engine, sess *Session) uctypes.ApprovalDecider {
	if eng == nil || sess == nil {
		return nil
	}
	chatId := AgentChatStorePrefix + sess.ChatID
	cwd := sess.Cwd
	return func(toolCall uctypes.WaveToolCall) uctypes.ApprovalDecision {
		ctx := sess.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		input, _ := toolCall.Input.(map[string]any)
		decision := eng.Decide(ctx, permissions.CheckRequest{
			ToolName: toolCall.Name,
			Input:    input,
			ChatId:   chatId,
			Cwd:      cwd,
			Posture:  resolvePosture(sess, eng),
		})
		return uctypes.ApprovalDecision{
			Behavior: string(decision.Behavior),
			Reason:   decision.Reason.Detail,
		}
	}
}

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
	if opts.Session != nil {
		opts.Session.Ctx = ctx
	}
	// === Cache-stable prefix ===
	// shared header → mode prompt → tool prompts (sorted). These rarely
	// change within a chat, so providers can cache the prefix.
	systemPrompt := SystemPromptForMode(opts.Session.Mode)
	toolList := ToolsForMode(opts.Session)
	if toolPrompts := BuildToolPromptSection(toolList); toolPrompts != "" {
		systemPrompt = append(systemPrompt, toolPrompts)
	}
	// === Dynamic suffix (per-request) ===
	// Terminal context, skills, project guidelines, and plan content
	// change between requests in the same chat — keep them after the
	// cacheable prefix so the cache stays warm.
	if termCtx := BuildTerminalContext(opts.Session); termCtx != "" {
		systemPrompt = append(systemPrompt, termCtx)
	}
	if skills := DiscoverSkills(opts.Session.Cwd); len(skills) > 0 {
		systemPrompt = append(systemPrompt, BuildSkillsContext(skills))
	}
	if guidelines := LoadProjectGuidelines(opts.Session.Cwd); guidelines != "" {
		systemPrompt = append(systemPrompt, guidelines)
	}
	if opts.PlanContext != "" {
		systemPrompt = append(systemPrompt, "## Active Plan\nExecute the following plan step by step:\n\n"+opts.PlanContext)
	}

	agentChatId := AgentChatStorePrefix + opts.Session.ChatID
	maxSteps := DefaultMaxAgentSteps
	if opts.Session.Mode != nil && opts.Session.Mode.StepBudget > 0 {
		maxSteps = opts.Session.Mode.StepBudget
	}
	eng := DefaultPermissionsEngine()
	posture := resolvePosture(opts.Session, eng)
	chatOpts := uctypes.WaveChatOpts{
		ChatId:               agentChatId,
		ClientId:             clientID,
		Config:               opts.AIOpts,
		Tools:                toolList,
		SystemPrompt:         systemPrompt,
		TabId:                opts.Session.TabID,
		AllowNativeWebSearch: false,
		Source:               AgentSourceName,
		MaxSteps:             maxSteps,
		ContextBudget:        DefaultContextBudget,
		MetricsCallback:      makeTrajectoryWriter(opts.Session.Cwd, opts.Session.ChatID),
		FileChangeCallback:   makeFileChangeRecorder(agentChatId, opts.UserMsg.MessageId),
		PendingTodosCheck:    makePendingTodosCheck(agentChatId),
		Posture:              string(posture),
		ApprovalDecider:      makeApprovalDecider(eng, opts.Session),
	}

	return aiusechat.WaveAIPostMessageWrap(ctx, sseHandler, opts.UserMsg, chatOpts)
}

func makePendingTodosCheck(chatId string) func() bool {
	return func() bool {
		return tools.HasPendingTodos(chatId)
	}
}

func makeFileChangeRecorder(chatId string, checkpointId string) func(string, string, bool) {
	return func(path, backupPath string, isNew bool) {
		DefaultCheckpointStore.RecordFileChange(chatId, checkpointId, FileChange{
			Path:        path,
			BackupPath:  backupPath,
			IsNew:       isNew,
			ContentHash: CurrentFileHash(path),
		})
	}
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
