// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const headlessShellMaxOutput = 64 * 1024

type headlessShellOutput struct {
	ExitCode     int    `json:"exit_code"`
	DurationMs   int64  `json:"duration_ms"`
	Stdout       string `json:"stdout"`
	Stderr       string `json:"stderr"`
	Truncated    bool   `json:"truncated"`
	TimedOut     bool   `json:"timed_out"`
	SpilloverLog string `json:"spillover_log,omitempty"`
}

func runHeadlessShell(params *shellExecInput, defaultCwd string) (*headlessShellOutput, error) {
	cwd := params.Cwd
	if cwd == "" {
		cwd = defaultCwd
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(params.TimeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", params.Cmd)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if params.Background {
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("start: %w", err)
		}
		return &headlessShellOutput{
			ExitCode: -1,
			Stdout:   fmt.Sprintf("started in background (pid %d)", cmd.Process.Pid),
		}, nil
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	startTime := time.Now()

	err := cmd.Run()
	durationMs := time.Since(startTime).Milliseconds()

	timedOut := ctx.Err() != nil
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if timedOut {
			exitCode = -1
		} else {
			return nil, fmt.Errorf("exec: %w", err)
		}
	}

	stdoutStr := stdout.String()
	stderrStr := stderr.String()
	truncated := false

	if len(stdoutStr) > headlessShellMaxOutput {
		truncated = true
		half := headlessShellMaxOutput / 2
		stdoutStr = stdoutStr[:half] + "\n...[truncated]...\n" + stdoutStr[len(stdoutStr)-half:]
	}
	if len(stderrStr) > headlessShellMaxOutput {
		stderrStr = stderrStr[:headlessShellMaxOutput/2] + "\n...[truncated]...\n" + stderrStr[len(stderrStr)-headlessShellMaxOutput/2:]
	}

	stdoutStr = repairUTF8(stdoutStr)
	stderrStr = repairUTF8(stderrStr)

	var spilloverLog string
	if truncated {
		spillFile, spillErr := writeSpillover(stdout.String())
		if spillErr == nil {
			spilloverLog = spillFile
		}
	}

	return &headlessShellOutput{
		ExitCode:     exitCode,
		DurationMs:   durationMs,
		Stdout:       strings.TrimRight(stdoutStr, "\n"),
		Stderr:       strings.TrimRight(stderrStr, "\n"),
		Truncated:    truncated,
		TimedOut:     timedOut,
		SpilloverLog: spilloverLog,
	}, nil
}
