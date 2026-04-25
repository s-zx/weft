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
	return t
}
