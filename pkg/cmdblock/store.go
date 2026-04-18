// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

package cmdblock

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

var ErrNotFound = errors.New("cmdblock not found")

// MakePromptStarted inserts a new "prompt" row at the moment OSC 16162;A is
// observed. The caller must know the parent terminal block id and the absolute
// byte offset at which the A sequence landed in the parent blockfile.
func MakePromptStarted(ctx context.Context, blockID string, promptOffset int64, shellType string) (*CmdBlock, error) {
	now := time.Now().UnixNano()
	cb := &CmdBlock{
		OID:          uuid.NewString(),
		BlockID:      blockID,
		State:        StatePrompt,
		PromptOffset: promptOffset,
		TsPromptNs:   now,
		CreatedAt:    now,
	}
	if shellType != "" {
		cb.ShellType = &shellType
	}
	err := wstore.WithTx(ctx, func(tx *wstore.TxWrap) error {
		cb.Seq = tx.GetInt64(`SELECT COALESCE(MAX(seq), 0) + 1 FROM db_cmdblock WHERE blockid = ?`, blockID)
		tx.Exec(`INSERT INTO db_cmdblock
			(oid, blockid, seq, state, cmd, cwd, shell_type, exit_code, duration_ms,
			 prompt_offset, cmd_offset, output_start_offset, output_end_offset,
			 ts_prompt_ns, ts_cmd_ns, ts_done_ns, agent_session_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			cb.OID, cb.BlockID, cb.Seq, cb.State,
			nilableString(cb.Cmd), nilableString(cb.Cwd), nilableString(cb.ShellType),
			nilableInt64(cb.ExitCode), nilableInt64(cb.DurationMs),
			cb.PromptOffset, nilableInt64(cb.CmdOffset),
			nilableInt64(cb.OutputStartOffset), nilableInt64(cb.OutputEndOffset),
			cb.TsPromptNs, nilableInt64(cb.TsCmdNs), nilableInt64(cb.TsDoneNs),
			nilableString(cb.AgentSessionID), cb.CreatedAt)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cb, nil
}

// MarkCommandSubmitted flips the latest prompt row for this block into
// "running" with the decoded command string, cwd, and byte offset where
// stdout is about to begin. Called on OSC 16162;C.
func MarkCommandSubmitted(ctx context.Context, oid string, cmd string, cwd string, cmdOffset int64, outputStartOffset int64, agentSessionID string) error {
	now := time.Now().UnixNano()
	var cwdArg interface{}
	if cwd != "" {
		cwdArg = cwd
	}
	var agentArg interface{}
	if agentSessionID != "" {
		agentArg = agentSessionID
	}
	return wstore.WithTx(ctx, func(tx *wstore.TxWrap) error {
		if !tx.Exists(`SELECT 1 FROM db_cmdblock WHERE oid = ?`, oid) {
			return ErrNotFound
		}
		tx.Exec(`UPDATE db_cmdblock
			SET state = ?, cmd = ?, cwd = ?, cmd_offset = ?, output_start_offset = ?,
			    ts_cmd_ns = ?, agent_session_id = ?
			WHERE oid = ?`,
			StateRunning, cmd, cwdArg, cmdOffset, outputStartOffset, now, agentArg, oid)
		return nil
	})
}

// MarkCommandDone finalizes a running row with exit code and output-end offset.
// Called on OSC 16162;D.
//
// If the row is still in the "prompt" state (i.e. no OSC 16162;C ever fired),
// the user hit Enter on an empty prompt — zsh/bash still emit the D from the
// next precmd using the previous exit code. In that case the row represents
// nothing worth showing, so we delete it to keep the list clean.
func MarkCommandDone(ctx context.Context, oid string, exitCode int64, outputEndOffset int64) error {
	now := time.Now().UnixNano()
	return wstore.WithTx(ctx, func(tx *wstore.TxWrap) error {
		var cb CmdBlock
		if !tx.Get(&cb, `SELECT * FROM db_cmdblock WHERE oid = ?`, oid) {
			return ErrNotFound
		}
		if cb.State == StatePrompt {
			tx.Exec(`DELETE FROM db_cmdblock WHERE oid = ?`, oid)
			return nil
		}
		var durationMs int64
		if cb.TsCmdNs != nil {
			durationMs = (now - *cb.TsCmdNs) / 1_000_000
		}
		tx.Exec(`UPDATE db_cmdblock
			SET state = ?, exit_code = ?, duration_ms = ?, output_end_offset = ?, ts_done_ns = ?
			WHERE oid = ?`,
			StateDone, exitCode, durationMs, outputEndOffset, now, oid)
		return nil
	})
}

// GetByBlockID returns the cmd_blocks for a parent terminal block ordered
// oldest-first. limit <= 0 means "all".
func GetByBlockID(ctx context.Context, blockID string, limit int) ([]*CmdBlock, error) {
	var rtn []*CmdBlock
	err := wstore.WithTx(ctx, func(tx *wstore.TxWrap) error {
		if limit > 0 {
			tx.Select(&rtn, `SELECT * FROM db_cmdblock WHERE blockid = ? ORDER BY seq ASC LIMIT ?`, blockID, limit)
		} else {
			tx.Select(&rtn, `SELECT * FROM db_cmdblock WHERE blockid = ? ORDER BY seq ASC`, blockID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rtn, nil
}

// LatestForBlock returns the most recent cmd_block for the given parent, or nil
// if there are none yet.
func LatestForBlock(ctx context.Context, blockID string) (*CmdBlock, error) {
	var cb CmdBlock
	var found bool
	err := wstore.WithTx(ctx, func(tx *wstore.TxWrap) error {
		found = tx.Get(&cb, `SELECT * FROM db_cmdblock WHERE blockid = ? ORDER BY seq DESC LIMIT 1`, blockID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &cb, nil
}

// getByOID loads a single row by oid. Returns (nil, nil) if not found so
// callers can publish "deleted" semantics without surfacing an error.
func getByOID(ctx context.Context, oid string) (*CmdBlock, error) {
	var cb CmdBlock
	var found bool
	err := wstore.WithTx(ctx, func(tx *wstore.TxWrap) error {
		found = tx.Get(&cb, `SELECT * FROM db_cmdblock WHERE oid = ?`, oid)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &cb, nil
}

// DeleteByBlockID removes all cmd_blocks belonging to a parent terminal block.
// Called when the parent block is deleted.
func DeleteByBlockID(ctx context.Context, blockID string) error {
	return wstore.WithTx(ctx, func(tx *wstore.TxWrap) error {
		tx.Exec(`DELETE FROM db_cmdblock WHERE blockid = ?`, blockID)
		return nil
	})
}

func nilableString(p *string) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

func nilableInt64(p *int64) interface{} {
	if p == nil {
		return nil
	}
	return *p
}
