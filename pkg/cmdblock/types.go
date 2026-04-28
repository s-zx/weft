// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

package cmdblock

import "github.com/s-zx/crest/pkg/cmdblock/cbtypes"

// Re-exports so callers can keep using cmdblock.CmdBlock / cmdblock.StateDone
// while the actual type lives in the leaf cbtypes package (see cbtypes for why).
type CmdBlock = cbtypes.CmdBlock

const (
	StatePrompt  = cbtypes.StatePrompt
	StateRunning = cbtypes.StateRunning
	StateDone    = cbtypes.StateDone
)
