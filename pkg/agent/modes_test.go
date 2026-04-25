// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

func TestLookupMode_ValidModes(t *testing.T) {
	for _, name := range []string{ModeAsk, ModePlan, ModeDo, ModeBench} {
		m, ok := LookupMode(name)
		if !ok {
			t.Fatalf("LookupMode(%q) returned ok=false", name)
		}
		if m.Name != name {
			t.Fatalf("LookupMode(%q).Name = %q", name, m.Name)
		}
	}
}

func TestLookupMode_EmptyDefaultsDo(t *testing.T) {
	m, ok := LookupMode("")
	if !ok {
		t.Fatal("LookupMode(\"\") returned ok=false")
	}
	if m.Name != ModeDo {
		t.Fatalf("expected do, got %q", m.Name)
	}
}

func TestLookupMode_Unknown(t *testing.T) {
	_, ok := LookupMode("invalid")
	if ok {
		t.Fatal("LookupMode(\"invalid\") should return ok=false")
	}
}

func TestResolveApproval_AskAutoAll(t *testing.T) {
	m, _ := LookupMode(ModeAsk)
	got := m.ResolveApproval("read_text_file", uctypes.ApprovalNeedsApproval)
	if got != uctypes.ApprovalAutoApproved {
		t.Fatalf("ask mode should auto-approve all, got %q", got)
	}
}

func TestResolveApproval_DoRequiresMutation(t *testing.T) {
	m, _ := LookupMode(ModeDo)
	mutations := []string{"write_text_file", "edit_text_file", "shell_exec", "create_block"}
	for _, tool := range mutations {
		got := m.ResolveApproval(tool, uctypes.ApprovalAutoApproved)
		if got != uctypes.ApprovalNeedsApproval {
			t.Fatalf("do mode should require approval for %q, got %q", tool, got)
		}
	}
}

func TestResolveApproval_DoAutoReads(t *testing.T) {
	m, _ := LookupMode(ModeDo)
	reads := []string{"read_text_file", "read_dir", "get_scrollback", "cmd_history"}
	for _, tool := range reads {
		got := m.ResolveApproval(tool, uctypes.ApprovalNeedsApproval)
		if got != uctypes.ApprovalAutoApproved {
			t.Fatalf("do mode should auto-approve %q, got %q", tool, got)
		}
	}
}

func TestResolveApproval_FallsBackToDefault(t *testing.T) {
	m, _ := LookupMode(ModeDo)
	got := m.ResolveApproval("unknown_tool", uctypes.ApprovalNeedsApproval)
	if got != uctypes.ApprovalNeedsApproval {
		t.Fatalf("unknown tool should fall back to default, got %q", got)
	}
}

func TestResolveApproval_NilMode(t *testing.T) {
	var m *Mode
	got := m.ResolveApproval("anything", uctypes.ApprovalNeedsApproval)
	if got != uctypes.ApprovalNeedsApproval {
		t.Fatalf("nil mode should return default, got %q", got)
	}
}

func TestModeToolNames(t *testing.T) {
	askMode, _ := LookupMode(ModeAsk)
	if len(askMode.ToolNames) != 6 {
		t.Fatalf("ask mode should have 6 tools, got %d", len(askMode.ToolNames))
	}

	planMode, _ := LookupMode(ModePlan)
	if len(planMode.ToolNames) != 7 {
		t.Fatalf("plan mode should have 7 tools, got %d", len(planMode.ToolNames))
	}

	doMode, _ := LookupMode(ModeDo)
	if len(doMode.ToolNames) != 19 {
		t.Fatalf("do mode should have 19 tools, got %d", len(doMode.ToolNames))
	}

	benchMode, _ := LookupMode(ModeBench)
	if len(benchMode.ToolNames) != 13 {
		t.Fatalf("bench mode should have 13 tools, got %d", len(benchMode.ToolNames))
	}
}

func TestModeAllowMutation(t *testing.T) {
	askMode, _ := LookupMode(ModeAsk)
	if askMode.AllowMutation {
		t.Fatal("ask mode should not allow mutation")
	}
	planMode, _ := LookupMode(ModePlan)
	if planMode.AllowMutation {
		t.Fatal("plan mode should not allow mutation")
	}
	doMode, _ := LookupMode(ModeDo)
	if !doMode.AllowMutation {
		t.Fatal("do mode should allow mutation")
	}
	benchMode, _ := LookupMode(ModeBench)
	if !benchMode.AllowMutation {
		t.Fatal("bench mode should allow mutation")
	}
	if !benchMode.Approval.AutoApproveAll {
		t.Fatal("bench mode should auto-approve all")
	}
}
