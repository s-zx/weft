// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
)

// Engine is the permissions decision engine. Per-agent (not global)
// so future multi-instance use cases (REPL, eval harness with custom
// rule set) can each own their own engine. Adapters are registered
// per-engine — the same shellExecAdapter could in principle behave
// differently in two engines, though today they don't.
type Engine struct {
	store *RuleStore

	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewEngine constructs an engine bound to the given RuleStore. The
// caller is responsible for setting builtin rules and any
// UserScopeBackend on the store before invoking Decide; the engine
// itself doesn't manage store lifecycle.
func NewEngine(store *RuleStore) *Engine {
	return &Engine{
		store:    store,
		adapters: make(map[string]Adapter),
	}
}

// RegisterAdapter attaches an adapter to the given tool name. Safe to
// call concurrently; intended for use at agent setup before the first
// Decide call. Replaces any existing adapter for that name.
func (e *Engine) RegisterAdapter(toolName string, adapter Adapter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.adapters[toolName] = adapter
}

func (e *Engine) lookupAdapter(toolName string) Adapter {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.adapters[toolName]
}

// Decide runs the §7 pipeline. The function is straight-line — we
// could split it into helpers, but the pipeline IS the design and
// readers benefit from seeing the whole thing in one place.
func (e *Engine) Decide(ctx context.Context, req CheckRequest) Decision {
	// Step 1 — bench posture short-circuits everything. Eval
	// harnesses post mode:"bench" and expect zero prompting; the
	// safety layer is intentionally skipped so a benchmark can do
	// `rm -rf` if the test calls for it. This is the only Mode
	// residue left in the system.
	if req.Posture == PostureBench {
		return Decision{
			Behavior: RuleAllow,
			Reason:   DecisionReason{Kind: ReasonPosture, Detail: "bench"},
		}
	}

	if err := ctx.Err(); err != nil {
		// Cancellation arriving mid-decide should fail closed: a
		// cancelled tool call must not silently auto-allow.
		return Decision{
			Behavior: RuleDeny,
			Reason: DecisionReason{
				Kind:   ReasonRule,
				Detail: "context canceled before permission check: " + err.Error(),
			},
		}
	}

	rules, _ := e.store.Load(ctx, req.ChatId, req.Cwd)
	adapter := e.lookupAdapter(req.ToolName)

	// Step 2 — tool-level deny in any scope wins immediately.
	// "Deny anywhere wins" is the design's safety-net principle:
	// once a user (or built-in) says "never," nothing should
	// override it.
	for _, r := range rules {
		if r.Behavior != RuleDeny {
			continue
		}
		if r.Content != "" {
			continue
		}
		if !r.Matches(req.ToolName) {
			continue
		}
		rule := r
		return Decision{
			Behavior: RuleDeny,
			Reason:   DecisionReason{Kind: ReasonRule, Detail: r.String()},
			Rule:     &rule,
		}
	}

	// Step 3 — content-specific Deny + Ask rules. These short-circuit
	// before safety because Deny anywhere is the authoritative refusal
	// and Ask is more conservative than safety's Ask. Allow rules are
	// deliberately deferred to step 6 — letting an Allow rule return
	// here would let a content pattern like `shell_exec(prefix:echo)`
	// auto-approve `echo \`rm -rf /\``: the safety substring matcher
	// never gets a chance to evaluate the inner command. The general
	// principle: only deny/ask short-circuit before safety; all
	// allow paths sit below safety.
	if adapter != nil {
		for _, r := range rules {
			if r.Content == "" {
				continue
			}
			if r.Behavior == RuleAllow {
				continue
			}
			if !r.Matches(req.ToolName) {
				continue
			}
			if !adapter.MatchContent(req.Input, r.Content) {
				continue
			}
			rule := r
			decision := Decision{
				Behavior: r.Behavior,
				Reason:   DecisionReason{Kind: ReasonRule, Detail: r.String()},
				Rule:     &rule,
			}
			if r.Behavior == RuleAsk {
				decision.Suggestions = adapter.SuggestRules(req.Input)
			}
			return decision
		}
	}

	// Step 4 — bypass-immune safety check. Runs BEFORE every Allow
	// path (step 5 tool-level Allow, step 6 content-specific Allow,
	// step 7 posture auto-allow) so a user can't accidentally (or
	// deliberately) unlock dangerous calls via an allow rule.
	// bench skipped this entirely back at step 1.
	safety := CheckSafety(req.ToolName, req.Input)
	if safety.Triggered {
		decision := Decision{
			Behavior: RuleAsk,
			Reason: DecisionReason{
				Kind:         ReasonSafetyCheck,
				Detail:       safety.Reason,
				BypassImmune: true,
			},
		}
		if adapter != nil {
			decision.Suggestions = adapter.SuggestRules(req.Input)
		}
		return decision
	}

	// Step 5 — tool-level allow rule (Content==""). Comes AFTER
	// safety so that an `allow shell_exec` rule can't unlock
	// `rm -rf /`.
	for _, r := range rules {
		if r.Behavior != RuleAllow {
			continue
		}
		if r.Content != "" {
			continue
		}
		if !r.Matches(req.ToolName) {
			continue
		}
		rule := r
		return Decision{
			Behavior: RuleAllow,
			Reason:   DecisionReason{Kind: ReasonRule, Detail: r.String()},
			Rule:     &rule,
		}
	}

	// Step 6 — content-specific Allow rule. Splitting from step 3
	// keeps allow paths below safety. Rule order is still scope
	// precedence desc + specificity desc, so the most-specific allow
	// wins — but only if safety hasn't already overridden.
	if adapter != nil {
		for _, r := range rules {
			if r.Behavior != RuleAllow {
				continue
			}
			if r.Content == "" {
				continue
			}
			if !r.Matches(req.ToolName) {
				continue
			}
			if !adapter.MatchContent(req.Input, r.Content) {
				continue
			}
			rule := r
			return Decision{
				Behavior: RuleAllow,
				Reason:   DecisionReason{Kind: ReasonRule, Detail: r.String()},
				Rule:     &rule,
			}
		}
	}

	// Step 7 — posture-driven auto-allow. Two paths: bypass auto-
	// allows everything; acceptEdits auto-allows file-edit tools
	// targeting paths inside cwd. Default posture falls through to
	// the per-tool default in step 8.
	if req.Posture == PostureBypass {
		return Decision{
			Behavior: RuleAllow,
			Reason:   DecisionReason{Kind: ReasonPosture, Detail: "bypass"},
		}
	}
	if req.Posture == PostureAcceptEdits && adapter != nil && adapter.IsFileEdit() {
		target := adapter.TargetPath(req.Input)
		if isInsideCwd(target, req.Cwd) {
			return Decision{
				Behavior: RuleAllow,
				Reason:   DecisionReason{Kind: ReasonPosture, Detail: "acceptEdits"},
			}
		}
	}

	// Step 8 — per-tool default. Mutations default to ask, reads to
	// allow. Tools without adapters default to ask (safe fallback —
	// in practice every mutating tool registers an adapter, and reads
	// are covered by the bundled allow rules from step 5).
	var defaultBehavior RuleBehavior
	if adapter != nil {
		defaultBehavior = adapter.DefaultBehavior()
	} else {
		defaultBehavior = RuleAsk
	}
	decision := Decision{
		Behavior: defaultBehavior,
		Reason:   DecisionReason{Kind: ReasonToolDefault, Detail: string(defaultBehavior)},
	}
	if defaultBehavior == RuleAsk && adapter != nil {
		decision.Suggestions = adapter.SuggestRules(req.Input)
	}
	return decision
}

// PersistRules forwards to the store. cwd is required for project
// scopes (sharedProject / localProject), ignored otherwise. Pass ""
// for cwd when persisting user-scope rules — the user-scope backend
// handles its own location.
func (e *Engine) PersistRules(ctx context.Context, scope RuleScope, cwd string, rules []Rule) error {
	return e.store.Persist(scope, cwd, rules)
}

// LoadRulesForChat is the public read path. Today only the
// permissions panel UI uses this directly; Decide does its own load
// internally to avoid re-walking the scopes for every check.
func (e *Engine) LoadRulesForChat(ctx context.Context, chatId, cwd string) ([]Rule, error) {
	return e.store.Load(ctx, chatId, cwd)
}

// AddSessionRule appends a rule to the in-memory session scope.
// Convenience wrapper — engines own a store, but the FE typically
// holds an Engine reference, not a *RuleStore.
func (e *Engine) AddSessionRule(chatId string, rule Rule) {
	e.store.AddSession(chatId, rule)
}

// LoadDefaultPosture returns the user's configured defaultPosture,
// falling back to acceptEdits when nothing is set or the user-scope
// backend isn't wired. Used by the agent runtime to seed per-chat
// posture when the API request doesn't supply one. Delegates to the
// underlying store; the indirection keeps callers from needing a
// *RuleStore reference (only the engine is plumbed through agent.go).
func (e *Engine) LoadDefaultPosture() Posture {
	return e.store.LoadDefaultPosture()
}

// isInsideCwd is a path containment check used by the acceptEdits
// posture: file edits to paths inside cwd auto-allow; edits outside
// fall through to the user prompt. Symbolic links and case-insensitive
// filesystems aren't normalized — a determined user can sidestep this,
// but the goal is to catch the common case ("agent edits a file in
// my repo") not to be a security boundary.
func isInsideCwd(target, cwd string) bool {
	if target == "" || cwd == "" {
		return false
	}
	target = filepath.Clean(target)
	cwd = filepath.Clean(cwd)
	rel, err := filepath.Rel(cwd, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..")
}
