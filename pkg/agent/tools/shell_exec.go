// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	Background  bool   `json:"background,omitempty"`
}

type shellExecOutput struct {
	BlockID      string `json:"block_id"`
	ExitCode     int    `json:"exit_code"`
	DurationMs   int64  `json:"duration_ms"`
	StdoutTail   string `json:"stdout_tail"`
	Truncated    bool   `json:"truncated"`
	TimedOut     bool   `json:"timed_out"`
	SpilloverLog string `json:"spillover_log,omitempty"`
	Background   bool   `json:"background,omitempty"`
}

// ShellExec runs a shell command. Default mode is HEADLESS — output is
// captured and returned to the agent, no terminal block opens, no user
// prompt. For long-running tasks (background:true) a hidden cmd-block
// is created so the user can opt in to watch it live via the "Open
// block" affordance on the tool-use card; the agent doesn't wait for
// user input either way.
func ShellExec(tabID, defaultBlockID, defaultCwd, defaultConnection string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "shell_exec",
		DisplayName: "Shell Execute",
		Description: "Run a shell command. By default runs headless and returns stdout/stderr. Pass background:true for long-running processes (dev servers, watchers) — the command runs detached and the user can attach a viewer block on demand. If output is truncated, the full output is saved to a spillover log file whose path is returned — use `read_text_file` on it to access the full output.",
		ToolLogName: "agent:shell_exec",
		Prompt: `shell_exec: Runs a shell command. Default is HEADLESS — output is captured and returned to you, no terminal block opens, no user prompt.
- Set "background": true for processes that don't terminate on their own: dev servers (npm run dev, vite), watchers, file servers, anything you want to keep running while the agent moves on. Returns immediately with a block_id; the user can click "Open block" in the tool-use card to watch it.
- For long but FINITE commands (npm install, pytest, cargo build): just call shell_exec normally — headless captures the output and the agent continues. Don't use background just because something takes a while; reserve it for processes that genuinely keep running.
- Avoid cat/head/tail/sed/awk/echo for file ops — use read_text_file, edit_text_file, write_text_file, or search instead. They round-trip less and the diff is reviewable.
- Quote paths that contain spaces. Prefer absolute paths or rely on the agent's cwd; don't "cd <dir> && cmd" unless cd is explicitly required.
- Chain dependent commands with && so a failure stops the chain. Use ; only when later commands should run regardless of failure. Avoid newlines inside the cmd string.
- An exit code of 127 means "command not found" — don't retry the same command after a 127. Check what's actually installed first (e.g. shell_exec "command -v <name>").
- Never bypass safety checks like --no-verify, --force, --no-gpg-sign unless the user explicitly asked for it.`,
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
				"background": map[string]any{
					"type":        "boolean",
					"default":     false,
					"description": "Run in the background. Returns immediately with the block_id without waiting for completion. The block is hidden by default; the user can attach a viewer via the 'Open block' button on the tool-use card.",
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
					if out.Background {
						return fmt.Sprintf("started %q in background", truncCmd(parsed.Cmd))
					}
					if out.TimedOut {
						return fmt.Sprintf("ran %q — timed out after %ds", truncCmd(parsed.Cmd), parsed.TimeoutSec)
					}
					return fmt.Sprintf("ran %q — exit %d in %dms", truncCmd(parsed.Cmd), out.ExitCode, out.DurationMs)
				}
			}
			return fmt.Sprintf("running %q", truncCmd(parsed.Cmd))
		},
		ToolVerifyInput: func(input any, toolUseData *uctypes.UIMessageDataToolUse) error {
			parsed, err := parseShellExecInput(input)
			if err != nil {
				return err
			}
			if dangerous, reason := IsDangerousCommand(parsed.Cmd); dangerous {
				if toolUseData != nil {
					toolUseData.Approval = uctypes.ApprovalNeedsApproval
					toolUseData.ToolDesc = fmt.Sprintf("DANGEROUS: %s — %q", reason, truncCmd(parsed.Cmd))
				}
			}
			return nil
		},
		ToolAnyCallback: func(input any, toolUseData *uctypes.UIMessageDataToolUse) (any, error) {
			parsed, err := parseShellExecInput(input)
			if err != nil {
				return nil, err
			}
			// Default + finite commands run headless: output captured,
			// returned to agent, no UI block. Background commands get a
			// hidden cmd-block (process runs but block isn't laid out
			// in the tab) so the user can opt in via "Open block".
			if !parsed.Background || tabID == "" {
				return runHeadlessShell(parsed, defaultCwd)
			}
			out, err := runShellExec(context.Background(), parsed, tabID, defaultBlockID, defaultCwd, defaultConnection)
			if err != nil {
				return runHeadlessShell(parsed, defaultCwd)
			}
			if toolUseData != nil {
				toolUseData.BlockId = out.BlockID
				// Background runs created the block but didn't put it
				// in the layout. The FE uses BlockHidden to render the
				// "Open block" button.
				toolUseData.BlockHidden = parsed.Background
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
	// Hidden background blocks self-delete on process exit so a
	// "fire-and-forget" run that the user never opens doesn't leave
	// orphan rows in the DB. ShowBlockCommand flips this back to
	// false when the user clicks "Open block", so an opened block
	// keeps its post-mortem output visible.
	if params.Background {
		meta[waveobj.MetaKey_CmdCloseOnExit] = true
	}

	blockDef := &waveobj.BlockDef{Meta: meta}
	block, err := wcore.CreateBlock(ctx, tabID, blockDef, nil)
	if err != nil {
		return nil, fmt.Errorf("create cmd block: %w", err)
	}
	blockID := block.OID

	// Background runs create the block but don't put it in the layout —
	// the process runs invisibly and the user opts in via the "Open
	// block" affordance on the tool-use card. Foreground (synchronous)
	// runs still use the layout because the agent waits anyway and the
	// user expects to see the output as it streams.
	if !params.Background {
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
	}
	wps.Broker.SendUpdateEvents(waveobj.ContextGetUpdatesRtn(ctx))

	if err := blockcontroller.ResyncController(ctx, tabID, blockID, nil, false); err != nil {
		return nil, fmt.Errorf("start controller: %w", err)
	}

	if params.Background {
		return &shellExecOutput{
			BlockID:    blockID,
			ExitCode:   -1,
			StdoutTail: "started in background — process is running headless. The user can click 'Open block' on the tool-use card to attach a viewer. Use get_scrollback to inspect output programmatically.",
			Background: true,
		}, nil
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

	var spilloverLog string
	if truncated {
		fullOutput := readBlockFull(ctx, blockID)
		if fullOutput != "" {
			spillFile, spillErr := writeSpillover(fullOutput)
			if spillErr == nil {
				spilloverLog = spillFile
			}
		}
	}

	return &shellExecOutput{
		BlockID:      blockID,
		ExitCode:     exitCode,
		DurationMs:   durationMs,
		StdoutTail:   tail,
		Truncated:    truncated,
		TimedOut:     timedOut,
		SpilloverLog: spilloverLog,
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

const spilloverMaxBytes = 512 * 1024

func readBlockFull(ctx context.Context, blockID string) string {
	wfile, err := filestore.WFS.Stat(ctx, blockID, wavebase.BlockFile_Term)
	if err != nil || wfile == nil || wfile.Size <= 0 {
		return ""
	}
	readLen := wfile.Size
	if readLen > spilloverMaxBytes {
		readLen = spilloverMaxBytes
	}
	_, data, err := filestore.WFS.ReadAt(ctx, blockID, wavebase.BlockFile_Term, 0, readLen)
	if err != nil {
		return ""
	}
	return stripAnsi(repairUTF8(string(data)))
}

func writeSpillover(content string) (string, error) {
	dir := filepath.Join(os.TempDir(), "crest-spillover")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "shell-*.log")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func truncCmd(cmd string) string {
	if len(cmd) <= 60 {
		return cmd
	}
	return cmd[:57] + "..."
}
