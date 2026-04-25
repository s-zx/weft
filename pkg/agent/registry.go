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
func buildTool(name string, sess *Session) (uctypes.ToolDefinition, bool) {
	switch name {
	case "read_text_file":
		return tools.ReadTextFile(approvalResolver(sess, name, uctypes.ApprovalAutoApproved)), true
	case "read_dir":
		return tools.ReadDir(approvalResolver(sess, name, uctypes.ApprovalAutoApproved)), true
	case "get_scrollback":
		return tools.GetScrollback(sess.TabID, approvalResolver(sess, name, uctypes.ApprovalAutoApproved)), true
	case "cmd_history":
		return tools.CmdHistory(sess.BlockID, approvalResolver(sess, name, uctypes.ApprovalAutoApproved)), true
	case "write_text_file":
		return tools.WriteTextFile(approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
	case "edit_text_file":
		return tools.EditTextFile(approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
	case "shell_exec":
		return tools.ShellExec(sess.TabID, sess.BlockID, sess.Cwd, sess.Connection, approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
	case "write_plan":
		return tools.WritePlan(sess.TabID, sess.BlockID, sess.Cwd, sess.Connection, approvalResolver(sess, name, uctypes.ApprovalAutoApproved)), true
	case "create_block":
		return tools.CreateBlock(sess.TabID, sess.BlockID, sess.Connection, approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
	case "focus_block":
		return tools.FocusBlock(sess.TabID, approvalResolver(sess, name, uctypes.ApprovalAutoApproved)), true
	case "browser.navigate":
		return tools.BrowserNavigate(sess.TabID, approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
	case "browser.read_text":
		return tools.BrowserReadText(sess.TabID, approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
	case "browser.click":
		return tools.BrowserClick(sess.TabID, approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
	case "browser.screenshot":
		return tools.BrowserScreenshot(sess.TabID, approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
	case "search":
		return tools.Search(approvalResolver(sess, name, uctypes.ApprovalAutoApproved)), true
	case "multi_edit":
		return tools.MultiEdit(approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
	case "todo_write":
		return tools.TodoWrite(AgentChatStorePrefix+sess.ChatID, approvalResolver(sess, name, uctypes.ApprovalAutoApproved)), true
	case "todo_read":
		return tools.TodoRead(AgentChatStorePrefix+sess.ChatID, approvalResolver(sess, name, uctypes.ApprovalAutoApproved)), true
	case "web_fetch":
		return tools.WebFetch(approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
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
		return tools.SpawnTask(cfg, approvalResolver(sess, name, uctypes.ApprovalNeedsApproval)), true
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

// approvalResolver returns a closure that consults the session's mode policy.
func approvalResolver(sess *Session, toolName string, defaultApproval string) func(any) string {
	mode := sess.Mode
	return func(any) string {
		return mode.ResolveApproval(toolName, defaultApproval)
	}
}
