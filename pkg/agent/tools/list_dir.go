// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

// ReadDir wraps pkg/aiusechat.GetReadDirToolDefinition for the agent.
func ReadDir(approval func(any) string) uctypes.ToolDefinition {
	t := aiusechat.GetReadDirToolDefinition()
	t.ToolLogName = "agent:read_dir"
	t.ToolApproval = approval
	t.Parallel = true
	t.Prompt = `read_dir: Lists the contents of a directory.
- Path must be absolute (or workspace-relative, ~ expanded).
- Use this to explore unfamiliar projects before reading specific files. Don't list a directory you've already inspected.
- For finding files by name pattern across a tree, prefer the search tool (faster, recursive, respects .gitignore).
- Parallel-safe — list multiple directories in one response when scoping out a project.`
	return t
}
