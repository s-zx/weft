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
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

// ReadTextFile wraps pkg/aiusechat.GetReadTextFileToolDefinition with an
// agent-scoped approval callback and telemetry log name.
func ReadTextFile(approval func(any) string) uctypes.ToolDefinition {
	t := aiusechat.GetReadTextFileToolDefinition()
	t.ToolLogName = "agent:read_text_file"
	t.ToolApproval = approval
	return t
}
