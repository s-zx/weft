// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

// Mode strings remain on the API surface — `mode: "ask"` etc. is what
// the FE sends today, and Harbor relies on `mode: "bench"` to flip
// posture. Internally the Mode struct is gone (per
// docs/permissions-v2-design.md §11.2): per-mode data is now spread
// across:
//
//   - ToolNamesForMode → which tools the registry constructs
//   - AllowMutationForMode → whether MCP mutation tools come along
//   - StepBudgetForMode → outer-loop step ceiling
//   - SystemPromptByKey (in prompts.go) → which prompt file to seed
//
// Once Shift+Tab posture cycling lands and the FE stops sending the
// API mode field, all of this can collapse further. For now the v1
// migration window keeps these helpers around as pure functions of
// the API mode string.
const (
	ModeAsk   = "ask"
	ModePlan  = "plan"
	ModeDo    = "do"
	ModeBench = "bench"
)

const (
	DefaultStepBudget = 40
	BenchStepBudget   = 100
)

// validModes accepts only canonical names; the empty string is also
// allowed (NormalizeMode maps it to "do") but isn't in this map.
var validModes = map[string]bool{
	ModeAsk: true, ModePlan: true, ModeDo: true, ModeBench: true,
}

// ValidMode reports whether name is a recognized mode string. Empty
// is treated as valid (and elsewhere normalized to "do") so the FE
// can omit the field on a fresh chat.
func ValidMode(name string) bool {
	if name == "" {
		return true
	}
	return validModes[name]
}

// NormalizeMode returns the canonical mode name. Empty defaults to "do"
// (the everyday-coding mode); unknown names also fall through to "do"
// — the API handler validates separately, so this is the safe last-
// resort default for code paths that have already accepted whatever
// the caller sent.
func NormalizeMode(name string) string {
	if name == "" {
		return ModeDo
	}
	if !validModes[name] {
		return ModeDo
	}
	return name
}

// ToolNamesForMode returns the canonical tool-name list for a mode.
// The registry walks this and constructs each tool fresh per turn.
//
// "ask" / "plan" intentionally exclude write_text_file / edit_text_file
// / shell_exec etc. so the model doesn't waste tokens proposing edits
// that the permission engine would deny anyway. Once the FE has a
// proper "tool allowlist" picker (or `--tools` CLI), this becomes
// session-driven and the per-mode lists can collapse.
func ToolNamesForMode(name string) []string {
	switch NormalizeMode(name) {
	case ModeAsk:
		return []string{
			"read_text_file",
			"read_dir",
			"search",
			"get_scrollback",
			"cmd_history",
			"web_fetch",
		}
	case ModePlan:
		return []string{
			"read_text_file",
			"read_dir",
			"search",
			"get_scrollback",
			"cmd_history",
			"write_plan",
			"web_fetch",
		}
	case ModeBench:
		return []string{
			"read_text_file",
			"read_dir",
			"search",
			"write_text_file",
			"edit_text_file",
			"multi_edit",
			"shell_exec",
			"web_fetch",
			"spawn_task",
			"todo_write",
			"todo_read",
		}
	default: // ModeDo
		return []string{
			"read_text_file",
			"read_dir",
			"search",
			"get_scrollback",
			"cmd_history",
			"write_text_file",
			"edit_text_file",
			"multi_edit",
			"shell_exec",
			"create_block",
			"focus_block",
			"browser.navigate",
			"browser.read_text",
			"browser.click",
			"browser.screenshot",
			"web_fetch",
			"spawn_task",
			"todo_write",
			"todo_read",
		}
	}
}

// AllowMutationForMode reports whether MCP mutation tools should be
// included alongside the per-mode tool list. ask/plan are read-only
// at the toolbox level (defense in depth — the permission engine
// would also block writes, but excluding them avoids wasted model
// tokens proposing edits that get refused).
func AllowMutationForMode(name string) bool {
	switch NormalizeMode(name) {
	case ModeAsk, ModePlan:
		return false
	default:
		return true
	}
}

// StepBudgetForMode returns the outer-loop step ceiling. Bench gets
// 100 because eval harnesses run multi-step automation that needs
// headroom; everything else uses 40, which is plenty for interactive
// coding work.
func StepBudgetForMode(name string) int {
	if NormalizeMode(name) == ModeBench {
		return BenchStepBudget
	}
	return DefaultStepBudget
}

