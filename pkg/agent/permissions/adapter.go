// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

// Adapter is the per-tool plug-in the engine consults to:
//
//   - match a rule's Content pattern against a concrete tool input
//     (each tool owns its matcher syntax: prefix:cmd for shell, glob
//     for file paths, host pattern for web tools);
//   - generate "remember this" rule suggestions when an Ask decision
//     is about to prompt the user;
//   - declare the tool's default behavior when no rule and no posture
//     applies (mutations → Ask, reads → Allow);
//   - expose the IsFileEdit/TargetPath signals the acceptEdits posture
//     needs to decide whether to auto-allow.
//
// One adapter per tool name; the same struct can be reused across
// tools (e.g. fileEditAdapter handles edit/write/multi_edit) by
// carrying the tool name as a struct field — that way SuggestRules
// can produce rules that name the right tool.
//
// Tools that don't need per-call logic (e.g. read_text_file lives
// happily under a global allow rule and never needs to match a path
// pattern in practice) can skip registering an adapter; the engine
// falls back to RuleAsk in that case, and the user's allow rules
// (including the bundled defaults) carry the day.
type Adapter interface {
	// MatchContent reports whether the rule pattern matches this
	// specific tool input. Empty pattern is the engine's job to
	// short-circuit — adapters may assume pattern is non-empty.
	MatchContent(input map[string]any, pattern string) bool

	// SuggestRules returns "remember this" suggestions for the
	// approval prompt UI, ordered most-specific first. Returning nil
	// is fine — UI will show plain Approve/Deny only.
	SuggestRules(input map[string]any) []Rule

	// DefaultBehavior is the tool's fallback when no rule matches and
	// the posture's auto-allow rules don't apply. Mutating tools
	// should return RuleAsk; reads should return RuleAllow.
	DefaultBehavior() RuleBehavior

	// IsFileEdit reports whether this tool writes files. The
	// acceptEdits posture auto-allows file-edit tools targeting paths
	// inside cwd. Non-file-edit tools (shell, web, browser) return
	// false and stay subject to default approval.
	IsFileEdit() bool

	// TargetPath returns the absolute path the tool will write, or ""
	// if the input doesn't carry one. Used by acceptEdits to compare
	// the target against cwd.
	TargetPath(input map[string]any) string
}
