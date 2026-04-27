// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

// Package permissions implements Crest's agent-tool permission engine.
//
// The model is three layers, evaluated in order on every tool call:
//
//  1. Bypass-immune Safety — hard list of catastrophic patterns
//     (`.git/`, `.ssh/`, `rm -rf /`, `prefix:sudo`, etc.). Always asks.
//  2. Rules — user-defined allow/deny/ask, loaded from 5 scopes
//     (CLI → user → sharedProject → localProject → session). Deny
//     in any scope wins.
//  3. Posture default — `default` falls through to the per-tool default
//     (`ask` for mutations, `allow` for reads); `acceptEdits` auto-allows
//     file edits inside cwd; `bypass` auto-allows everything (still
//     constrained by Layer 1); `bench` auto-allows everything ignoring
//     Layer 1 (eval-only).
//
// The package is leaf — it depends only on uctypes (for shared tool
// types) and standard library. Tools register a PermissionAdapter on
// their ToolDefinition; the engine consults it through that interface
// to avoid a permissions ↔ tools cycle.
//
// See docs/permissions-v2-design.md for the rationale.
package permissions

// RuleBehavior is the verdict a rule (or the engine) emits for a tool
// call. Only three values; everything else is a metadata refinement.
type RuleBehavior string

const (
	RuleAllow RuleBehavior = "allow"
	RuleDeny  RuleBehavior = "deny"
	RuleAsk   RuleBehavior = "ask"
)

// Posture is the per-chat strictness axis. Users cycle through three of
// the four values via Shift+Tab; `bench` is API-only (eval harnesses
// post `mode: "bench"` which the backend translates to PostureBench).
type Posture string

const (
	PostureDefault     Posture = "default"
	PostureAcceptEdits Posture = "acceptEdits"
	PostureBypass      Posture = "bypass"
	PostureBench       Posture = "bench" // hidden; never surfaced in UI
)

// RuleScope identifies where a rule came from. Higher numeric value =
// higher precedence in conflict resolution. Deny in ANY scope wins
// regardless of precedence.
type RuleScope int

const (
	ScopeUnknown RuleScope = iota
	ScopeBuiltin
	ScopeSession
	ScopeLocalProject
	ScopeSharedProject
	ScopeUser
	ScopeCLIArg
)

// String returns a stable short label for logging and the wire format.
// Order intentionally matches Claude Code's convention so existing UX
// docs translate verbatim.
func (s RuleScope) String() string {
	switch s {
	case ScopeBuiltin:
		return "builtin"
	case ScopeSession:
		return "session"
	case ScopeLocalProject:
		return "localProject"
	case ScopeSharedProject:
		return "sharedProject"
	case ScopeUser:
		return "user"
	case ScopeCLIArg:
		return "cliArg"
	default:
		return "unknown"
	}
}

// RuleSource records the provenance of a Rule. Path is set for rules
// loaded from a settings file (so the UI can show "where this came
// from"); it is empty for builtin rules and session-scope rules created
// from approval prompts.
type RuleSource struct {
	Scope RuleScope
	Path  string
}

// Rule is the unit of permission grammar. ParseRule turns the wire
// string ("shell_exec(prefix:npm)") into one; String turns it back.
type Rule struct {
	Behavior RuleBehavior
	ToolName string // exact tool name, "*", or "mcp__server__*"
	Content  string // optional matcher; empty = matches any call to ToolName
	Source   RuleSource
	AddedAt  int64 // unix ms; UI-only, zero for builtin
}

// DecisionReasonKind names the layer that produced a Decision. Useful
// for telemetry, the approval prompt UI ("blocked by safety check"
// vs "matched your `prefix:npm` rule"), and tests.
type DecisionReasonKind string

const (
	ReasonRule        DecisionReasonKind = "rule"
	ReasonSafetyCheck DecisionReasonKind = "safetyCheck"
	ReasonPosture     DecisionReasonKind = "posture"
	ReasonToolDefault DecisionReasonKind = "toolDefault"
)

// DecisionReason explains how the engine arrived at a Decision. The
// BypassImmune flag is set when a safety check fired; the dispatcher
// uses it to ensure no posture override sneaks past safety.
type DecisionReason struct {
	Kind         DecisionReasonKind
	Detail       string
	BypassImmune bool
}

// Decision is the engine's verdict on a single tool call. When
// Behavior == RuleAsk, Suggestions is populated by the relevant
// PermissionAdapter so the approval UI can offer "remember this"
// shortcuts.
type Decision struct {
	Behavior     RuleBehavior
	Reason       DecisionReason
	Rule         *Rule  // populated when Behavior decided by a rule
	Suggestions  []Rule // populated only when Behavior == RuleAsk
	UpdatedInput map[string]any
}

// CheckRequest is the input to Engine.Decide. Mode is intentionally
// absent — v2 has no Mode axis. Posture is the only strictness signal;
// rules + safety handle the rest.
type CheckRequest struct {
	ToolName string
	Input    map[string]any
	ChatId   string  // for session-scope lookups
	Cwd      string  // for project-scope file loading + acceptEdits path checks
	Posture  Posture // current per-chat posture
}
