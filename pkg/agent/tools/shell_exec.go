// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/blockcontroller"
	"github.com/s-zx/crest/pkg/filestore"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/wavebase"
	"github.com/s-zx/crest/pkg/waveobj"
	"github.com/s-zx/crest/pkg/wcore"
	"github.com/s-zx/crest/pkg/wps"
)

const (
	shellExecDefaultTimeout = 120
	shellExecMaxTimeout     = 600
	shellExecTailBytes      = 8192
	shellExecPollInterval   = 500 * time.Millisecond
	shellExecSigintWait     = 3 * time.Second
)

type shellExecInput struct {
	Cmd         string `json:"cmd"`
	Cwd         string `json:"cwd,omitempty"`
	TimeoutSec  int    `json:"timeout_sec,omitempty"`
	CloseOnExit bool   `json:"close_on_exit,omitempty"`
}

type shellExecOutput struct {
	BlockID    string `json:"block_id"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	StdoutTail string `json:"stdout_tail"`
	Truncated  bool   `json:"truncated"`
	TimedOut   bool   `json:"timed_out"`
}

// ShellExec creates a visible cmd-block in the user's tab, runs the command,
// waits for completion, and returns the exit code plus a tail of the output.
// This is the Crest-native differentiator: agent shell actions are first-class
// blocks the user can observe and interact with, not hidden subprocesses.
func ShellExec(tabID, defaultBlockID, defaultCwd, defaultConnection string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "shell_exec",
		DisplayName: "Shell Execute",
		Description: "Run a shell command in a new visible terminal block. The command output is visible to the user in real-time. Returns exit code and a tail of stdout. Use for builds, tests, git operations, installs, and any shell task.",
		ToolLogName: "agent:shell_exec",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cmd": map[string]any{
					"type":        "string",
					"description": "Shell command to execute.",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory. Defaults to the agent's terminal cwd.",
				},
				"timeout_sec": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     shellExecMaxTimeout,
					"default":     shellExecDefaultTimeout,
					"description": "Maximum seconds to wait. Default 120, max 600.",
				},
				"close_on_exit": map[string]any{
					"type":        "boolean",
					"default":     false,
					"description": "Close the block automatically when the command finishes.",
				},
			},
			"required":             []string{"cmd"},
			"additionalProperties": false,
		},
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, err := parseShellExecInput(input)
			if err != nil {
				return fmt.Sprintf("shell_exec (invalid: %v)", err)
			}
			if output != nil {
				if out, ok := output.(*shellExecOutput); ok {
					if out.TimedOut {
						return fmt.Sprintf("ran %q — timed out after %ds", truncCmd(parsed.Cmd), parsed.TimeoutSec)
					}
					return fmt.Sprintf("ran %q — exit %d in %dms", truncCmd(parsed.Cmd), out.ExitCode, out.DurationMs)
				}
			}
			return fmt.Sprintf("running %q", truncCmd(parsed.Cmd))
		},
		ToolAnyCallback: func(input any, toolUseData *uctypes.UIMessageDataToolUse) (any, error) {
			parsed, err := parseShellExecInput(input)
			if err != nil {
				return nil, err
			}
			out, err := runShellExec(context.Background(), parsed, tabID, defaultBlockID, defaultCwd, defaultConnection)
			if err != nil {
				return nil, err
			}
			if toolUseData != nil {
				toolUseData.BlockId = out.BlockID
			}
			return out, nil
		},
		ToolApproval: approval,
	}
}

func parseShellExecInput(input any) (*shellExecInput, error) {
	params := &shellExecInput{}
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}
	if err := utilfn.ReUnmarshal(params, input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	params.Cmd = strings.TrimSpace(params.Cmd)
	if params.Cmd == "" {
		return nil, fmt.Errorf("cmd is required")
	}
	if params.TimeoutSec <= 0 {
		params.TimeoutSec = shellExecDefaultTimeout
	}
	if params.TimeoutSec > shellExecMaxTimeout {
		params.TimeoutSec = shellExecMaxTimeout
	}
	return params, nil
}

func runShellExec(ctx context.Context, params *shellExecInput, tabID, defaultBlockID, defaultCwd, defaultConnection string) (*shellExecOutput, error) {
	if tabID == "" {
		return nil, fmt.Errorf("agent session has no tab context")
	}

	cwd := params.Cwd
	if cwd == "" {
		cwd = defaultCwd
	}

	ctx = waveobj.ContextWithUpdates(ctx)

	meta := waveobj.MetaMapType{
		waveobj.MetaKey_View:          "term",
		waveobj.MetaKey_Controller:    "cmd",
		waveobj.MetaKey_Cmd:           params.Cmd,
		waveobj.MetaKey_CmdRunOnStart: true,
	}
	if cwd != "" {
		meta[waveobj.MetaKey_CmdCwd] = cwd
	}
	if defaultConnection != "" {
		meta[waveobj.MetaKey_Connection] = defaultConnection
	}
	if params.CloseOnExit {
		meta[waveobj.MetaKey_CmdCloseOnExit] = true
	}

	blockDef := &waveobj.BlockDef{Meta: meta}
	block, err := wcore.CreateBlock(ctx, tabID, blockDef, nil)
	if err != nil {
		return nil, fmt.Errorf("create cmd block: %w", err)
	}
	blockID := block.OID

	layoutAction := &waveobj.LayoutActionData{
		ActionType:    wcore.LayoutActionDataType_SplitVertical,
		BlockId:       blockID,
		TargetBlockId: defaultBlockID,
		Position:      "after",
	}
	if defaultBlockID == "" {
		layoutAction = &waveobj.LayoutActionData{
			ActionType: wcore.LayoutActionDataType_Insert,
			BlockId:    blockID,
		}
	}
	if err := wcore.QueueLayoutActionForTab(ctx, tabID, *layoutAction); err != nil {
		return nil, fmt.Errorf("queue layout: %w", err)
	}
	wps.Broker.SendUpdateEvents(waveobj.ContextGetUpdatesRtn(ctx))

	if err := blockcontroller.ResyncController(ctx, tabID, blockID, nil, false); err != nil {
		return nil, fmt.Errorf("start controller: %w", err)
	}

	startTime := time.Now()
	deadline := startTime.Add(time.Duration(params.TimeoutSec) * time.Second)
	timedOut := false
	exitCode := 0

	for {
		if time.Now().After(deadline) {
			timedOut = true
			break
		}
		status := blockcontroller.GetBlockControllerRuntimeStatus(blockID)
		if status != nil && status.ShellProcStatus == blockcontroller.Status_Done {
			exitCode = status.ShellProcExitCode
			break
		}
		time.Sleep(shellExecPollInterval)
	}

	if timedOut {
		blockcontroller.SendInput(blockID, &blockcontroller.BlockInputUnion{
			SigName: "SIGINT",
		})
		sigintDeadline := time.Now().Add(shellExecSigintWait)
		gracefulDone := false
		for time.Now().Before(sigintDeadline) {
			time.Sleep(shellExecPollInterval)
			status := blockcontroller.GetBlockControllerRuntimeStatus(blockID)
			if status == nil || status.ShellProcStatus == blockcontroller.Status_Done {
				if status != nil {
					exitCode = status.ShellProcExitCode
				}
				gracefulDone = true
				break
			}
		}
		if !gracefulDone {
			blockcontroller.DestroyBlockController(blockID)
			exitCode = -1
		}
	}

	durationMs := time.Since(startTime).Milliseconds()

	tail, truncated := readBlockTail(ctx, blockID)

	return &shellExecOutput{
		BlockID:    blockID,
		ExitCode:   exitCode,
		DurationMs: durationMs,
		StdoutTail: tail,
		Truncated:  truncated,
		TimedOut:   timedOut,
	}, nil
}

func readBlockTail(ctx context.Context, blockID string) (string, bool) {
	wfile, err := filestore.WFS.Stat(ctx, blockID, wavebase.BlockFile_Term)
	if err != nil || wfile == nil {
		return "", false
	}
	fileSize := wfile.Size
	if fileSize <= 0 {
		return "", false
	}
	offset := int64(0)
	readLen := fileSize
	truncated := false
	if fileSize > shellExecTailBytes {
		offset = fileSize - shellExecTailBytes
		readLen = shellExecTailBytes
		truncated = true
	}
	_, data, err := filestore.WFS.ReadAt(ctx, blockID, wavebase.BlockFile_Term, offset, readLen)
	if err != nil {
		return "", false
	}
	cleaned := stripAnsi(string(data))
	cleaned = repairUTF8(cleaned)
	return cleaned, truncated
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][AB012]|\x1b[\x20-\x2F]*[\x40-\x7E]`)

func stripAnsi(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func repairUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			b.WriteRune('�')
			i++
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func truncCmd(cmd string) string {
	if len(cmd) <= 60 {
		return cmd
	}
	return cmd[:57] + "..."
}
