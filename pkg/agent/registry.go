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

// ToolsForSession returns the concrete ToolDefinition list the step
// loop will see for this turn. Each tool is constructed fresh per
// request so closures capture the right session. The tool list is
// derived from the session's mode string (legacy API behavior — once
// the FE sends an explicit tool allowlist, this collapses to "iterate
// sess.Tools").
func ToolsForSession(sess *Session) []uctypes.ToolDefinition {
	if sess == nil {
		return nil
	}
	names := ToolNamesForMode(sess.Mode)
	out := make([]uctypes.ToolDefinition, 0, len(names))
	for _, name := range names {
		if td, ok := buildTool(name, sess); ok {
			out = append(out, td)
		}
	}
	if AllowMutationForMode(sess.Mode) {
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
				return SystemPromptByKey(modeName)
			},
			ToolsForMode: func(modeName string) []uctypes.ToolDefinition { return toolsForModeByName(modeName, sess) },
		}
		return tools.SpawnTask(cfg, engineDeferredApproval), true
	}
	return uctypes.ToolDefinition{}, false
}

// toolsForModeByName builds the toolbox a spawn_task subagent sees,
// given the parent session for terminal context (TabID / BlockID /
// Cwd / Connection) and the subtask's mode name. Just clones the
// session with a different mode string.
func toolsForModeByName(modeName string, sess *Session) []uctypes.ToolDefinition {
	if !ValidMode(modeName) {
		return nil
	}
	subSess := &Session{
		ChatID:     sess.ChatID,
		TabID:      sess.TabID,
		BlockID:    sess.BlockID,
		Mode:       modeName,
		AIOpts:     sess.AIOpts,
		Cwd:        sess.Cwd,
		Connection: sess.Connection,
	}
	return ToolsForSession(subSess)
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
