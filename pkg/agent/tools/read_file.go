// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

// Package tools contains agent-specific tool adapters. Each adapter takes an
// approval resolver closure (produced by the agent package from the active
// mode's ApprovalPolicy) and returns a ready-to-use uctypes.ToolDefinition.
//
// Most adapters are thin wrappers over existing pkg/aiusechat tool definitions
// — we reuse the input schema, description, and callback logic, and only
// substitute the approval callback and the telemetry log name.
package tools

import (
	"path/filepath"

	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/wavebase"
)

// ReadTextFile wraps pkg/aiusechat.GetReadTextFileToolDefinition with an
// agent-scoped approval callback and telemetry log name. The MtimeRecordHook
// fires after a successful read so the matching write/edit tools can detect
// external modifications between the agent's read and its write.
func ReadTextFile(chatId string, approval func(any) string) uctypes.ToolDefinition {
	t := aiusechat.GetReadTextFileToolDefinition()
	t.ToolLogName = "agent:read_text_file"
	t.ToolApproval = approval
	t.Parallel = true
	t.Prompt = `read_text_file: Reads a file from the local filesystem.
- The "filename" parameter must be an absolute path or workspace-relative path. Tilde (~) and $HOME are expanded.
- By default reads up to 2000 lines. When you already know the section you need, use the "offset" and "limit" parameters — don't read the whole file.
- Don't re-read a file you just edited. The Edit/Write tool would have errored if the change failed; the new content is what you wrote.
- Reading is parallel-safe — issue multiple read_text_file calls in a single response when you need to inspect several files.`
	t.AfterHooks = []uctypes.AfterToolHook{MtimeRecordHook(chatId)}
	return t
}

// extractFilenameFromInput pulls the "filename" key out of a tool's input
// map and returns the absolute path. Tilde-expanded and absolute-path-checked
// — anything we can't normalize returns "" so the tracker just no-ops.
func extractFilenameFromInput(input any) string {
	type fnHolder struct {
		Filename string `json:"filename"`
	}
	holder := &fnHolder{}
	if err := utilfn.ReUnmarshal(holder, input); err != nil || holder.Filename == "" {
		return ""
	}
	expanded, err := wavebase.ExpandHomeDir(holder.Filename)
	if err != nil || !filepath.IsAbs(expanded) {
		return ""
	}
	return expanded
}
