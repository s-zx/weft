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
	return t
}
