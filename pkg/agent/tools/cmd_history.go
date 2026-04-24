// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/cmdblock"
	"github.com/s-zx/crest/pkg/cmdblock/cbtypes"
	"github.com/s-zx/crest/pkg/filestore"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/wavebase"
)

const (
	cmdHistoryDefaultLimit = 20
	cmdHistoryMaxLimit     = 100
	cmdHistoryOutputBytes  = 4096 // per-row tail cap so the LLM context stays bounded
)

type cmdHistoryInput struct {
	BlockID       string `json:"block_id,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	IncludeOutput bool   `json:"include_output,omitempty"`
}

type cmdHistoryRow struct {
	Seq        int64  `json:"seq"`
	State      string `json:"state"`
	Cmd        string `json:"cmd,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	ShellType  string `json:"shell_type,omitempty"`
	ExitCode   *int64 `json:"exit_code,omitempty"`
	DurationMs *int64 `json:"duration_ms,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

type cmdHistoryOutput struct {
	BlockID string          `json:"block_id"`
	Count   int             `json:"count"`
	Rows    []cmdHistoryRow `json:"rows"`
}

// CmdHistory reports the last N commands run inside the terminal block the
// agent was invoked from (or another block the caller names explicitly).
// This tool is unique to Crest — it uses the block-level cmdblock tracker
// plus the per-block circular terminal buffer.
func CmdHistory(defaultBlockID string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "cmd_history",
		DisplayName: "Command History",
		Description: "Report the last N commands run in a terminal block, including exit codes, durations, cwd, and optionally a short tail of each command's output. Defaults to the terminal block the agent was invoked from.",
		ToolLogName: "agent:cmd_history",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Optional terminal block id. Defaults to the block the agent was invoked from.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     cmdHistoryMaxLimit,
					"default":     cmdHistoryDefaultLimit,
					"description": "Maximum number of most-recent commands to return.",
				},
				"include_output": map[string]any{
					"type":        "boolean",
					"default":     false,
					"description": "Include a short tail of each command's output. Keep off unless you actually need it — output is verbose.",
				},
			},
			"additionalProperties": false,
		},
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, _ := parseCmdHistoryInput(input, defaultBlockID)
			return fmt.Sprintf("reading last %d commands from block %s", parsed.Limit, parsed.BlockID)
		},
		ToolAnyCallback: func(input any, _ *uctypes.UIMessageDataToolUse) (any, error) {
			parsed, err := parseCmdHistoryInput(input, defaultBlockID)
			if err != nil {
				return nil, err
			}
			return runCmdHistory(context.Background(), parsed)
		},
		ToolApproval: approval,
	}
}

func parseCmdHistoryInput(input any, defaultBlockID string) (*cmdHistoryInput, error) {
	params := &cmdHistoryInput{}
	if input != nil {
		if err := utilfn.ReUnmarshal(params, input); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}
	}
	if params.BlockID == "" {
		params.BlockID = defaultBlockID
	}
	if params.BlockID == "" {
		return nil, fmt.Errorf("block_id is required and no default is available in this session")
	}
	if params.Limit == 0 {
		params.Limit = cmdHistoryDefaultLimit
	}
	if params.Limit < 1 {
		return nil, fmt.Errorf("limit must be >= 1")
	}
	if params.Limit > cmdHistoryMaxLimit {
		params.Limit = cmdHistoryMaxLimit
	}
	return params, nil
}

func runCmdHistory(ctx context.Context, params *cmdHistoryInput) (*cmdHistoryOutput, error) {
	rows, err := cmdblock.GetByBlockID(ctx, params.BlockID, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("cmdblock lookup failed: %w", err)
	}

	// GetByBlockID returns oldest-first; reverse so most recent is first.
	result := &cmdHistoryOutput{BlockID: params.BlockID, Rows: make([]cmdHistoryRow, 0, len(rows))}
	for i := len(rows) - 1; i >= 0; i-- {
		cb := rows[i]
		row := toRow(cb)
		if params.IncludeOutput && cb.State == cbtypes.StateDone {
			row.OutputTail, row.Truncated = readOutputTail(ctx, cb)
		}
		result.Rows = append(result.Rows, row)
	}
	result.Count = len(result.Rows)
	return result, nil
}

func toRow(cb *cbtypes.CmdBlock) cmdHistoryRow {
	row := cmdHistoryRow{
		Seq:        cb.Seq,
		State:      cb.State,
		ExitCode:   cb.ExitCode,
		DurationMs: cb.DurationMs,
	}
	if cb.Cmd != nil {
		row.Cmd = *cb.Cmd
	}
	if cb.Cwd != nil {
		row.Cwd = *cb.Cwd
	}
	if cb.ShellType != nil {
		row.ShellType = *cb.ShellType
	}
	if cb.TsPromptNs > 0 {
		row.StartedAt = time.Unix(0, cb.TsPromptNs).UTC().Format(time.RFC3339)
	}
	return row
}

func readOutputTail(ctx context.Context, cb *cbtypes.CmdBlock) (string, bool) {
	if cb.OutputStartOffset == nil || cb.OutputEndOffset == nil {
		return "", false
	}
	start := *cb.OutputStartOffset
	end := *cb.OutputEndOffset
	if end <= start {
		return "", false
	}
	totalLen := end - start
	readLen := totalLen
	truncated := false
	if readLen > cmdHistoryOutputBytes {
		start = end - cmdHistoryOutputBytes
		readLen = cmdHistoryOutputBytes
		truncated = true
	}
	_, data, err := filestore.WFS.ReadAt(ctx, cb.BlockID, wavebase.BlockFile_Term, start, readLen)
	if err != nil {
		return "", false
	}
	return string(data), truncated
}
