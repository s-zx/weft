// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"github.com/s-zx/crest/pkg/agent/mcp"
	"github.com/s-zx/crest/pkg/agent/tools"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

const BrowserToolNamespace = "browser"
const ApprovalCategoryBrowser = "browser"

// ToolsForMode returns the concrete ToolDefinition list the step loop will see
// for this turn. Each tool is constructed fresh per request so closures capture
// the right session + mode.
func ToolsForMode(sess *Session) []uctypes.ToolDefinition {
	if sess == nil || sess.Mode == nil {
		return nil
	}
	out := make([]uctypes.ToolDefinition, 0, len(sess.Mode.ToolNames))
	for _, name := range sess.Mode.ToolNames {
		if td, ok := buildTool(name, sess); ok {
			out = append(out, td)
		}
	}
	if sess.Mode.AllowMutation {
		out = append(out, mcp.GetManager().GetAllTools()...)
	}
	return out
}

// buildTool maps a canonical tool name to its per-session ToolDefinition.
// Unknown names are ignored so a typo in a mode definition fails safe.
//
// The per-tool `ToolApproval` callback is no longer the policy
// authority — pkg/agent/permissions.Engine decides via
// WaveChatOpts.ApprovalDecider. We pass engineDeferredApproval here
// as a fail-closed safety net: if a code path forgets to wire the
// engine the tool lands in the approval prompt rather than running
// silently. See engineDeferredApproval below for the rationale.
func buildTool(name string, sess *Session) (uctypes.ToolDefinition, bool) {
	chatScope := AgentChatStorePrefix + sess.ChatID
	switch name {
	case "read_text_file":
		return tools.ReadTextFile(chatScope, engineDeferredApproval), true
	case "read_dir":
		return tools.ReadDir(engineDeferredApproval), true
	case "get_scrollback":
		return tools.GetScrollback(sess.TabID, engineDeferredApproval), true
	case "cmd_history":
		return tools.CmdHistory(sess.BlockID, engineDeferredApproval), true
	case "write_text_file":
		return tools.WriteTextFile(chatScope, engineDeferredApproval), true
	case "edit_text_file":
		return tools.EditTextFile(chatScope, engineDeferredApproval), true
	case "shell_exec":
		return tools.ShellExec(sess.TabID, sess.BlockID, sess.Cwd, sess.Connection, engineDeferredApproval), true
	case "write_plan":
		return tools.WritePlan(sess.TabID, sess.BlockID, sess.Cwd, sess.Connection, engineDeferredApproval), true
	case "create_block":
		return tools.CreateBlock(sess.TabID, sess.BlockID, sess.Connection, engineDeferredApproval), true
	case "focus_block":
		return tools.FocusBlock(sess.TabID, engineDeferredApproval), true
	case "browser.navigate":
		return tools.BrowserNavigate(sess.TabID, engineDeferredApproval), true
	case "browser.read_text":
		return tools.BrowserReadText(sess.TabID, engineDeferredApproval), true
	case "browser.click":
		return tools.BrowserClick(sess.TabID, engineDeferredApproval), true
	case "browser.screenshot":
		return tools.BrowserScreenshot(sess.TabID, engineDeferredApproval), true
	case "search":
		return tools.Search(sess.Cwd, engineDeferredApproval), true
	case "multi_edit":
		return tools.MultiEdit(chatScope, engineDeferredApproval), true
	case "todo_write":
		return tools.TodoWrite(AgentChatStorePrefix+sess.ChatID, engineDeferredApproval), true
	case "todo_read":
		return tools.TodoRead(AgentChatStorePrefix+sess.ChatID, engineDeferredApproval), true
	case "web_fetch":
		return tools.WebFetch(engineDeferredApproval), true
	case "spawn_task":
		cfg := tools.SpawnTaskConfig{
			ParentOpts: sess.AIOpts,
			ParentCtx:  sess.Ctx,
			Cwd:        sess.Cwd,
			PromptForMode: func(modeName string) []string {
				m, ok := LookupMode(modeName)
				if !ok {
					return nil
				}
				return SystemPromptForMode(m)
			},
			ToolsForMode: func(modeName string) []uctypes.ToolDefinition { return toolsForModeByName(modeName, sess) },
		}
		return tools.SpawnTask(cfg, engineDeferredApproval), true
	}
	return uctypes.ToolDefinition{}, false
}

func toolsForModeByName(modeName string, sess *Session) []uctypes.ToolDefinition {
	m, ok := LookupMode(modeName)
	if !ok {
		return nil
	}
	subSess := &Session{
		ChatID:     sess.ChatID,
		TabID:      sess.TabID,
		BlockID:    sess.BlockID,
		Mode:       m,
		AIOpts:     sess.AIOpts,
		Cwd:        sess.Cwd,
		Connection: sess.Connection,
	}
	return ToolsForMode(subSess)
}

// engineDeferredApproval is the policy-deferred fallback installed
// on every tool's ToolApproval slot now that the permissions engine
// owns the real decision. The engine runs ahead of this callback in
// CreateToolUseData; this fallback only gets consulted when no
// ApprovalDecider is wired (legacy WaveChatOpts callers, tests, eval
// harnesses without the agent runtime).
//
// Returns ApprovalNeedsApproval to fail CLOSED. An earlier draft
// returned "" which IsApproved() treats as approved — that meant a
// caller forgetting to wire the engine got silent auto-approve on
// every tool, with no log line. Failing closed means the worst case
// is "user sees an approval prompt for every tool" instead of
// "destructive call ran unchecked." Production paths (RunAgent →
// makeApprovalDecider) always wire the decider so users never see
// this in normal use.
func engineDeferredApproval(_ any) string {
	return uctypes.ApprovalNeedsApproval
}
