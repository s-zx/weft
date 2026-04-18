// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

// Package cbtypes holds pure data types shared between the cmdblock store and
// the RPC layer. It exists solely to break an import cycle:
//
//	pkg/wshrpc -> pkg/cmdblock -> pkg/wstore -> pkg/filestore -> pkg/wshrpc
//
// by being a leaf package with no dependencies on the DB stack.
package cbtypes

const (
	StatePrompt  = "prompt"
	StateRunning = "running"
	StateDone    = "done"
)

// CmdBlockChunkEvent is published on cmdblock:chunk while a cmdblock row is
// in the "running" state. Data64 is a base64-encoded slice of PTY bytes that
// landed in the parent blockfile starting at Offset. Includes OSC 16162
// sequences; consumers can either strip or pass through to xterm (which will
// treat unknown OSC numbers as a no-op).
type CmdBlockChunkEvent struct {
	BlockID string `json:"blockid"`
	OID     string `json:"oid"`
	Offset  int64  `json:"offset"`
	Data64  string `json:"data64"`
}

// CmdBlockAltScreenEvent is published on cmdblock:altscreen when the PTY
// enters (Enter=true) or leaves (Enter=false) the alternate screen buffer
// (DECSET 1049 / DECRST 1049). The frontend switches to a single full-height
// xterm in pass-through mode while Enter, and back to the block list on exit.
type CmdBlockAltScreenEvent struct {
	BlockID string `json:"blockid"`
	OID     string `json:"oid,omitempty"`
	Enter   bool   `json:"enter"`
}

// CmdBlock is one shell-command lifecycle tracked inside a terminal block.
//
// Each row covers the span from OSC 16162;A (prompt appeared) to OSC 16162;D
// (command done). The raw output bytes live in the parent terminal block's
// existing BlockFile_Term circular file; we only store offsets here so the
// frontend can replay a range into a per-command xterm instance.
type CmdBlock struct {
	OID               string  `db:"oid" json:"oid"`
	BlockID           string  `db:"blockid" json:"blockid"`
	Seq               int64   `db:"seq" json:"seq"`
	State             string  `db:"state" json:"state"`
	Cmd               *string `db:"cmd" json:"cmd,omitempty"`
	Cwd               *string `db:"cwd" json:"cwd,omitempty"`
	ShellType         *string `db:"shell_type" json:"shelltype,omitempty"`
	ExitCode          *int64  `db:"exit_code" json:"exitcode,omitempty"`
	DurationMs        *int64  `db:"duration_ms" json:"durationms,omitempty"`
	PromptOffset      int64   `db:"prompt_offset" json:"promptoffset"`
	CmdOffset         *int64  `db:"cmd_offset" json:"cmdoffset,omitempty"`
	OutputStartOffset *int64  `db:"output_start_offset" json:"outputstartoffset,omitempty"`
	OutputEndOffset   *int64  `db:"output_end_offset" json:"outputendoffset,omitempty"`
	TsPromptNs        int64   `db:"ts_prompt_ns" json:"tspromptns"`
	TsCmdNs           *int64  `db:"ts_cmd_ns" json:"tscmdns,omitempty"`
	TsDoneNs          *int64  `db:"ts_done_ns" json:"tsdonens,omitempty"`
	AgentSessionID    *string `db:"agent_session_id" json:"agentsessionid,omitempty"`
	CreatedAt         int64   `db:"created_at" json:"createdat"`
}
