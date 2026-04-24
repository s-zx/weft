// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import "github.com/s-zx/crest/pkg/aiusechat/uctypes"

const (
	ModeAsk  = "ask"
	ModePlan = "plan"
	ModeDo   = "do"
)

const (
	DefaultStepBudget    = 40
	DefaultFailureBudget = 3
)

// ApprovalPolicy controls which tool calls are auto-approved vs require user approval.
// Resolution order per tool invocation:
//  1. AutoApproveAll → auto
//  2. Name in RequireApproval → needs approval
//  3. Name in AutoApproveTools → auto
//  4. Fallback behavior is tool-specific (read tools auto, mutation tools need approval)
type ApprovalPolicy struct {
	AutoApproveAll   bool
	AutoApproveTools map[string]bool
	RequireApproval  map[string]bool
}

type Mode struct {
	Name          string
	DisplayName   string
	ToolNames     []string
	AllowMutation bool
	Approval      ApprovalPolicy
	StepBudget    int
	FailureBudget int
}

var modes = map[string]*Mode{
	ModeAsk: {
		Name:        ModeAsk,
		DisplayName: "Ask",
		ToolNames: []string{
			"read_text_file",
			"read_dir",
			"get_scrollback",
			"cmd_history",
		},
		AllowMutation: false,
		Approval: ApprovalPolicy{
			AutoApproveAll: true,
		},
		StepBudget:    DefaultStepBudget,
		FailureBudget: DefaultFailureBudget,
	},
	ModePlan: {
		Name:        ModePlan,
		DisplayName: "Plan",
		ToolNames: []string{
			"read_text_file",
			"read_dir",
			"get_scrollback",
			"cmd_history",
			"write_plan",
		},
		AllowMutation: false,
		Approval: ApprovalPolicy{
			AutoApproveTools: map[string]bool{
				"read_text_file": true,
				"read_dir":       true,
				"get_scrollback": true,
				"cmd_history":    true,
				"write_plan":     true,
			},
		},
		StepBudget:    DefaultStepBudget,
		FailureBudget: DefaultFailureBudget,
	},
	ModeDo: {
		Name:        ModeDo,
		DisplayName: "Do",
		ToolNames: []string{
			"read_text_file",
			"read_dir",
			"get_scrollback",
			"cmd_history",
			"write_text_file",
			"edit_text_file",
			"shell_exec",
			"create_block",
			"focus_block",
			"browser.navigate",
			"browser.read_text",
			"browser.click",
			"browser.screenshot",
		},
		AllowMutation: true,
		Approval: ApprovalPolicy{
			AutoApproveTools: map[string]bool{
				"read_text_file": true,
				"read_dir":       true,
				"get_scrollback": true,
				"cmd_history":    true,
			},
			RequireApproval: map[string]bool{
				"write_text_file":  true,
				"edit_text_file":   true,
				"shell_exec":       true,
				"create_block":     true,
				"browser.navigate": true,
				"browser.click":    true,
			},
		},
		StepBudget:    DefaultStepBudget,
		FailureBudget: DefaultFailureBudget,
	},
}

func LookupMode(name string) (*Mode, bool) {
	if name == "" {
		name = ModeDo
	}
	m, ok := modes[name]
	return m, ok
}

// ResolveApproval returns one of uctypes.ApprovalAutoApproved or uctypes.ApprovalNeedsApproval
// for the given tool name under this mode. Pass the default the tool itself would choose if
// no mode policy applies (e.g. "auto" for reads, "needs-approval" for writes).
func (m *Mode) ResolveApproval(toolName string, defaultApproval string) string {
	if m == nil {
		return defaultApproval
	}
	if m.Approval.AutoApproveAll {
		return uctypes.ApprovalAutoApproved
	}
	if m.Approval.RequireApproval[toolName] {
		return uctypes.ApprovalNeedsApproval
	}
	if m.Approval.AutoApproveTools[toolName] {
		return uctypes.ApprovalAutoApproved
	}
	return defaultApproval
}
