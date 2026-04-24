// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

func WriteTextFile(approval func(any) string) uctypes.ToolDefinition {
	t := aiusechat.GetWriteTextFileToolDefinition()
	t.ToolLogName = "agent:write_text_file"
	t.ToolApproval = approval
	return t
}

func EditTextFile(approval func(any) string) uctypes.ToolDefinition {
	t := aiusechat.GetEditTextFileToolDefinition()
	t.ToolLogName = "agent:edit_text_file"
	t.ToolApproval = approval
	return t
}
