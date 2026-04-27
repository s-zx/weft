// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

// WriteTextFile wraps aiusechat.GetWriteTextFileToolDefinition with the
// agent's approval callback and a stale-edit guard: if the model is
// overwriting a file it had previously read, the BeforeHook refuses the
// write when the file has changed externally since that read.
func WriteTextFile(chatId string, approval func(any) string) uctypes.ToolDefinition {
	t := aiusechat.GetWriteTextFileToolDefinition()
	t.ToolLogName = "agent:write_text_file"
	t.ToolApproval = approval
	t.Prompt = `write_text_file: Writes a complete file (overwrites if it exists).
- Prefer edit_text_file or multi_edit for modifications — they only send the diff. Use write_text_file for new files or full rewrites.
- For an existing file, you should normally read_text_file it first so you don't clobber unrelated content.
- Never create documentation files (*.md, README, CHANGELOG) unless the user explicitly asked for one.`
	t.BeforeHooks = []uctypes.BeforeToolHook{MtimeCheckHook(chatId)}
	t.AfterHooks = []uctypes.AfterToolHook{MtimeRecordHook(chatId)}
	return t
}

// EditTextFile wraps the underlying edit tool with the same stale-edit
// guard. The guard fires when the file has been modified externally between
// the agent's last read and this edit; without it, an out-of-band change is
// silently overwritten by the agent's stale view of the file.
func EditTextFile(chatId string, approval func(any) string) uctypes.ToolDefinition {
	t := aiusechat.GetEditTextFileToolDefinition()
	t.ToolLogName = "agent:edit_text_file"
	t.ToolApproval = approval
	t.Prompt = `edit_text_file: Performs an exact string replacement in a file.
- Read the file first if you don't already have its contents — the edit fails if "old_text" doesn't match exactly (whitespace, indentation, line endings included).
- "old_text" must be unique within the file, or pass "replace_all": true. If the match isn't unique, expand "old_text" with surrounding context until it is.
- Preserve indentation exactly as the file uses it (tabs vs spaces, leading whitespace).
- For multiple changes in the same file, prefer multi_edit over several edit_text_file calls — it applies sequentially in one round trip.`
	t.BeforeHooks = []uctypes.BeforeToolHook{MtimeCheckHook(chatId)}
	t.AfterHooks = []uctypes.AfterToolHook{MtimeRecordHook(chatId)}
	return t
}
