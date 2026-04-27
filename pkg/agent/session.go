// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

// Session carries per-request context for a single agent turn.
// It is constructed in the HTTP handler from the request body plus workspace state
// and passed into tool factories that need terminal-aware data.
type Session struct {
	ChatID      string
	TabID       string
	BlockID     string
	// Mode is the canonical API mode string ("ask" / "plan" / "do" /
	// "bench"). The Mode struct is gone; per-mode data is now spread
	// across ToolNamesForMode / AllowMutationForMode / StepBudgetForMode
	// in profile.go. Empty defaults to "do".
	Mode        string
	AIOpts      uctypes.AIOptsType
	Cwd         string
	Connection  string
	LastCommand string
	RecentCmds  []string
	// Posture is the permissions strictness axis ("default", "acceptEdits",
	// "bypass", "bench"). Empty string is treated as the user's configured
	// defaultPosture (acceptEdits in shipped settings). The HTTP handler
	// fills this in from the request body or from `mode: "bench"` aliasing.
	Posture string
	// Ctx is the parent agent's request context. Tools that fan out child work
	// (e.g. spawn_task) must derive their context from this so cancellation
	// propagates from the user's tab close down to long-running sub-agents.
	Ctx context.Context
}
