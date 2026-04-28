// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"testing"
)

func TestParseShellExecInput_Valid(t *testing.T) {
	input := map[string]any{"cmd": "echo hi", "timeout_sec": float64(30)}
	parsed, err := parseShellExecInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Cmd != "echo hi" {
		t.Fatalf("got cmd %q", parsed.Cmd)
	}
	if parsed.TimeoutSec != 30 {
		t.Fatalf("got timeout %d", parsed.TimeoutSec)
	}
}

func TestParseShellExecInput_EmptyCmd(t *testing.T) {
	input := map[string]any{"cmd": ""}
	_, err := parseShellExecInput(input)
	if err == nil {
		t.Fatal("expected error for empty cmd")
	}
}

func TestParseShellExecInput_NilInput(t *testing.T) {
	_, err := parseShellExecInput(nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestParseShellExecInput_DefaultTimeout(t *testing.T) {
	input := map[string]any{"cmd": "ls"}
	parsed, err := parseShellExecInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TimeoutSec != shellExecDefaultTimeout {
		t.Fatalf("expected default timeout %d, got %d", shellExecDefaultTimeout, parsed.TimeoutSec)
	}
}

func TestParseShellExecInput_ClampMaxTimeout(t *testing.T) {
	input := map[string]any{"cmd": "ls", "timeout_sec": float64(9999)}
	parsed, err := parseShellExecInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TimeoutSec != shellExecMaxTimeout {
		t.Fatalf("expected clamped to %d, got %d", shellExecMaxTimeout, parsed.TimeoutSec)
	}
}

func TestStripAnsi(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello world", "hello world"},
		{"sgr_color", "\x1b[31mred\x1b[0m", "red"},
		{"cursor_move", "\x1b[2Jhello", "hello"},
		{"osc_title", "\x1b]0;My Title\x07rest", "rest"},
		{"mixed", "\x1b[1m\x1b[32mBOLD GREEN\x1b[0m normal", "BOLD GREEN normal"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripAnsi(tt.input)
			if got != tt.want {
				t.Fatalf("stripAnsi(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRepairUTF8(t *testing.T) {
	valid := "hello 世界"
	if repairUTF8(valid) != valid {
		t.Fatal("valid UTF-8 should pass through")
	}

	broken := "hello\x80world"
	repaired := repairUTF8(broken)
	if repaired != "hello�world" {
		t.Fatalf("broken byte should become replacement char, got %q", repaired)
	}
}

func TestTruncCmd(t *testing.T) {
	short := "echo hi"
	if truncCmd(short) != short {
		t.Fatal("short cmd should pass through")
	}
	long := "this is a very long command that definitely exceeds the sixty character limit we set"
	got := truncCmd(long)
	if len(got) > 60 {
		t.Fatalf("truncated should be <= 60, got %d", len(got))
	}
	if got[len(got)-3:] != "..." {
		t.Fatal("truncated should end with ...")
	}
}

func TestParseCmdHistoryInput_Defaults(t *testing.T) {
	input := map[string]any{}
	parsed, err := parseCmdHistoryInput(input, "default-block")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.BlockID != "default-block" {
		t.Fatalf("expected default block, got %q", parsed.BlockID)
	}
	if parsed.Limit != cmdHistoryDefaultLimit {
		t.Fatalf("expected default limit %d, got %d", cmdHistoryDefaultLimit, parsed.Limit)
	}
}

func TestParseCmdHistoryInput_NoBlockID(t *testing.T) {
	input := map[string]any{}
	_, err := parseCmdHistoryInput(input, "")
	if err == nil {
		t.Fatal("expected error when no block_id and no default")
	}
}

func TestParseCmdHistoryInput_ClampLimit(t *testing.T) {
	input := map[string]any{"limit": float64(999)}
	parsed, err := parseCmdHistoryInput(input, "b")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Limit != cmdHistoryMaxLimit {
		t.Fatalf("expected clamped to %d, got %d", cmdHistoryMaxLimit, parsed.Limit)
	}
}

func TestParseCreateBlockInput_Valid(t *testing.T) {
	input := map[string]any{"view": "term"}
	parsed, err := parseCreateBlockInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.View != "term" {
		t.Fatalf("expected term, got %q", parsed.View)
	}
	if parsed.TargetAction != "splitdown" {
		t.Fatalf("expected default splitdown, got %q", parsed.TargetAction)
	}
}

func TestParseCreateBlockInput_InvalidView(t *testing.T) {
	input := map[string]any{"view": "invalid"}
	_, err := parseCreateBlockInput(input)
	if err == nil {
		t.Fatal("expected error for invalid view")
	}
}

func TestParseCreateBlockInput_WebRequiresUrl(t *testing.T) {
	input := map[string]any{"view": "web"}
	_, err := parseCreateBlockInput(input)
	if err == nil {
		t.Fatal("expected error for web without url")
	}
}

func TestParseCreateBlockInput_PreviewRequiresFile(t *testing.T) {
	input := map[string]any{"view": "preview"}
	_, err := parseCreateBlockInput(input)
	if err == nil {
		t.Fatal("expected error for preview without file")
	}
}

func TestParseFocusBlockInput_Empty(t *testing.T) {
	input := map[string]any{}
	_, err := parseFocusBlockInput(input)
	if err == nil {
		t.Fatal("expected error for missing block_id")
	}
}

func TestParseWritePlanInput_Valid(t *testing.T) {
	input := map[string]any{"title": "My Plan", "content": "Some content"}
	parsed, err := parseWritePlanInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Slug != "my-plan" {
		t.Fatalf("expected slug my-plan, got %q", parsed.Slug)
	}
}

func TestParseWritePlanInput_EmptyTitle(t *testing.T) {
	input := map[string]any{"title": "", "content": "x"}
	_, err := parseWritePlanInput(input)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"  add retry to RunAIChat  ", "add-retry-to-runaichat"},
		{"CamelCase Test", "camelcase-test"},
		{"---dashes---", "dashes"},
		{"a/b/c", "a-b-c"},
	}
	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Fatalf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
