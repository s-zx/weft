// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0
//
// TODO(permissions-v2 follow-up, see docs/permissions-v2-design.md §11.2):
// this file is slated for full deletion. The current state — Mode struct
// holding ToolNames + AllowMutation + StepBudget + FailureBudget — is a
// halfway point. Approval policy was extracted into pkg/agent/permissions
// in this sitting; the remaining responsibilities still need new homes:
//
//   - ToolNames: should source from a session-level tool allowlist
//     (mirroring the `--tools` CLI flag described in design §10).
//   - StepBudget / FailureBudget: should be functions of posture (bench →
//     100, others → 40) rather than a per-mode constant.
//   - AllowMutation: redundant once permissions rules drive the actual
//     authorization — the engine refuses mutations the user hasn't
//     allowed regardless of whether the tool is in the toolbox.
//   - SystemPromptForMode: no longer mode-aware; collapses to a single
//     default with SYSTEM.md user override.
//
// Until that refactor lands modes.go stays as a tool list + budget
// dispatcher.

package agent

const (
	ModeAsk   = "ask"
	ModePlan  = "plan"
	ModeDo    = "do"
	ModeBench = "bench"
)

const (
	DefaultStepBudget    = 40
	DefaultFailureBudget = 3
)

// Mode is a per-turn bundle: tool list, mutation gate, step/failure
// budgets. Approval policy used to live here too — that's been
// extracted into pkg/agent/permissions (Engine + posture + rules) so
// modes are now a pure "what tools can the agent see, how long can it
// run" struct. Plan-mode-style enforcement (no edits) was previously
// done via ApprovalPolicy and is now simply a function of which tools
// land in ToolNames; if `edit_text_file` isn't here, the model can't
// call it.
//
// `bench` survives because eval harnesses need a longer step budget
// (100 vs 40) and a different failure budget. The HTTP handler also
// translates `mode: "bench"` → posture `bench` for permissions
// (see http.go).
type Mode struct {
	Name          string
	DisplayName   string
	ToolNames     []string
	AllowMutation bool
	StepBudget    int
	FailureBudget int
}

const BenchStepBudget = 100

var modes = map[string]*Mode{
	ModeAsk: {
		Name:        ModeAsk,
		DisplayName: "Ask",
		ToolNames: []string{
			"read_text_file",
			"read_dir",
			"search",
			"get_scrollback",
			"cmd_history",
			"web_fetch",
		},
		AllowMutation: false,
		StepBudget:    DefaultStepBudget,
		FailureBudget: DefaultFailureBudget,
	},
	ModePlan: {
		Name:        ModePlan,
		DisplayName: "Plan",
		ToolNames: []string{
			"read_text_file",
			"read_dir",
			"search",
			"get_scrollback",
			"cmd_history",
			"write_plan",
			"web_fetch",
		},
		AllowMutation: false,
		StepBudget:    DefaultStepBudget,
		FailureBudget: DefaultFailureBudget,
	},
	ModeDo: {
		Name:        ModeDo,
		DisplayName: "Do",
		ToolNames: []string{
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
		},
		AllowMutation: true,
		StepBudget:    DefaultStepBudget,
		FailureBudget: DefaultFailureBudget,
	},
	ModeBench: {
		Name:        ModeBench,
		DisplayName: "Bench",
		ToolNames: []string{
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
		},
		AllowMutation: true,
		StepBudget:    BenchStepBudget,
		FailureBudget: 10,
	},
}

func LookupMode(name string) (*Mode, bool) {
	if name == "" {
		name = ModeDo
	}
	m, ok := modes[name]
	return m, ok
}
