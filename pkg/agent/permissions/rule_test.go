// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import "testing"

func TestParseRule(t *testing.T) {
	cases := []struct {
		in        string
		wantTool  string
		wantContent string
		wantErr   bool
	}{
		{"shell_exec", "shell_exec", "", false},
		{"*", "*", "", false},
		{"mcp__filesystem__*", "mcp__filesystem__*", "", false},
		{"shell_exec(prefix:npm)", "shell_exec", "prefix:npm", false},
		{"edit_text_file(/Users/me/work/**)", "edit_text_file", "/Users/me/work/**", false},
		{"shell_exec(echo \\))", "shell_exec", "echo )", false},     // escaped paren
		{"shell_exec(a\\\\b)", "shell_exec", "a\\b", false},           // escaped backslash
		{"  shell_exec(prefix:git)  ", "shell_exec", "prefix:git", false}, // surrounding ws
		{"", "", "", true},
		{"shell_exec(", "", "", true},          // unterminated
		{"shell_exec(prefix:npm) extra", "", "", true}, // trailing content
		{"bad name(x)", "", "", true},          // space in tool name
	}
	for _, tc := range cases {
		got, err := ParseRule(tc.in, RuleAllow, RuleSource{Scope: ScopeSession})
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseRule(%q): want error, got %+v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRule(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got.ToolName != tc.wantTool {
			t.Errorf("ParseRule(%q): tool got %q want %q", tc.in, got.ToolName, tc.wantTool)
		}
		if got.Content != tc.wantContent {
			t.Errorf("ParseRule(%q): content got %q want %q", tc.in, got.Content, tc.wantContent)
		}
	}
}

func TestRuleString_RoundTrip(t *testing.T) {
	inputs := []string{
		"shell_exec",
		"*",
		"mcp__filesystem__*",
		"shell_exec(prefix:npm)",
		"edit_text_file(/Users/me/work/**)",
		"shell_exec(echo \\))",
		"shell_exec(a\\\\b)",
	}
	for _, in := range inputs {
		r, err := ParseRule(in, RuleAllow, RuleSource{})
		if err != nil {
			t.Fatalf("ParseRule(%q) failed: %v", in, err)
		}
		out := r.String()
		// Strip surrounding whitespace from input for comparison.
		in2, err := ParseRule(out, RuleAllow, RuleSource{})
		if err != nil {
			t.Fatalf("re-parse of %q failed: %v", out, err)
		}
		if in2.ToolName != r.ToolName || in2.Content != r.Content {
			t.Errorf("round-trip %q -> %q -> %+v lost data", in, out, in2)
		}
	}
}

func TestRuleMatches(t *testing.T) {
	cases := []struct {
		ruleTool string
		callTool string
		want     bool
	}{
		{"shell_exec", "shell_exec", true},
		{"shell_exec", "edit_text_file", false},
		{"*", "anything", true},
		{"*", "shell_exec", true},
		{"mcp__filesystem__*", "mcp__filesystem__read", true},
		{"mcp__filesystem__*", "mcp__filesystem__write", true},
		{"mcp__filesystem__*", "mcp__other__read", false},
	}
	for _, tc := range cases {
		r := Rule{ToolName: tc.ruleTool}
		if got := r.Matches(tc.callTool); got != tc.want {
			t.Errorf("Rule{%q}.Matches(%q) = %v want %v", tc.ruleTool, tc.callTool, got, tc.want)
		}
	}
}

func TestRuleSpecificity(t *testing.T) {
	// More-specific rules should sort higher. Just check ordering is
	// consistent with intuition; absolute scores are implementation
	// detail.
	wildcard := Rule{ToolName: "*"}
	mcpStar := Rule{ToolName: "mcp__filesystem__*"}
	exact := Rule{ToolName: "shell_exec"}
	exactWithContent := Rule{ToolName: "shell_exec", Content: "prefix:npm"}
	exactLongerContent := Rule{ToolName: "shell_exec", Content: "prefix:npm install"}

	if !(exact.Specificity() > mcpStar.Specificity()) {
		t.Errorf("exact tool should beat mcp__server__*")
	}
	if !(mcpStar.Specificity() > wildcard.Specificity()) {
		t.Errorf("mcp__server__* should beat bare *")
	}
	if !(exactWithContent.Specificity() > exact.Specificity()) {
		t.Errorf("exact + content should beat exact alone")
	}
	if !(exactLongerContent.Specificity() > exactWithContent.Specificity()) {
		t.Errorf("longer content should beat shorter content")
	}
}
