// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"strings"
	"testing"
)

func TestBuildTerminalContext_Full(t *testing.T) {
	sess := &Session{
		ChatID:      "test-chat-id",
		TabID:       "test-tab-id",
		BlockID:     "test-block-id",
		Mode:        ModeDo,
		Cwd:         "/home/user/project",
		Connection:  "ssh:myserver",
		LastCommand: "go test ./...",
		RecentCmds:  []string{"ls -la", "cd project"},
	}

	out := BuildTerminalContext(sess)
	if !strings.Contains(out, "<terminal_context>") {
		t.Fatal("expected <terminal_context> tag")
	}
	if !strings.Contains(out, "/home/user/project") {
		t.Fatal("expected cwd in output")
	}
	if !strings.Contains(out, "ssh:myserver") {
		t.Fatal("expected connection in output")
	}
	if !strings.Contains(out, "go test ./...") {
		t.Fatal("expected last_command in output")
	}
	if !strings.Contains(out, "ls -la") {
		t.Fatal("expected recent_cmds in output")
	}
}

func TestBuildTerminalContext_Minimal(t *testing.T) {
	sess := &Session{
		ChatID:  "id",
		TabID:   "tab",
		BlockID: "block",
		Mode:    ModeAsk,
	}

	out := BuildTerminalContext(sess)
	if !strings.Contains(out, "<terminal_context>") {
		t.Fatal("expected <terminal_context> tag even with minimal session")
	}
	if strings.Contains(out, "connection:") {
		t.Fatal("no connection should be in output when empty")
	}
}

func TestBuildTerminalContext_NilSession(t *testing.T) {
	out := BuildTerminalContext(nil)
	if out != "" {
		t.Fatalf("nil session should return empty string, got %q", out)
	}
}
