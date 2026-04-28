// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

// GetScrollback wraps pkg/aiusechat.GetTermGetScrollbackToolDefinition. The
// upstream tool takes a tabId at construction so we thread the session's tab.
func GetScrollback(tabID string, approval func(any) string) uctypes.ToolDefinition {
	t := aiusechat.GetTermGetScrollbackToolDefinition(tabID)
	t.ToolLogName = "agent:get_scrollback"
	t.ToolApproval = approval
	t.Parallel = true
	t.Prompt = `get_scrollback: Reads the recent terminal output of a block.
- Use when you need the actual output of something the user just ran (or that you ran via shell_exec) — exit codes alone aren't enough.
- For structured "what commands ran here" data (cmd, exit code, duration), prefer cmd_history — it's cheaper than parsing scrollback.
- Output can be large; ask for the smallest line window that answers your question.
- Parallel-safe.`
	return t
}
