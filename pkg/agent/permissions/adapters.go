// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import (
	"path/filepath"
	"strings"
)

// shellExecAdapter handles shell_exec. The matcher syntax is either an
// exact command (matches "git status" exactly) or `prefix:<cmd>`
// (matches anything starting with "<cmd>" or "<cmd> "). Suggestions
// produce three ladder rungs: the exact command, the first-token
// prefix, and the whole tool.
type shellExecAdapter struct{}

// ShellExecAdapter returns the adapter for shell_exec. Function rather
// than var so the adapter has no exported zero-value other consumers
// could mutate; the engine just registers it once.
func ShellExecAdapter() Adapter { return shellExecAdapter{} }

func (shellExecAdapter) MatchContent(input map[string]any, pattern string) bool {
	cmd, _ := input["command"].(string)
	if strings.HasPrefix(pattern, "prefix:") {
		return MatchPrefix(cmd, pattern)
	}
	return MatchExact(cmd, pattern)
}

func (shellExecAdapter) SuggestRules(input map[string]any) []Rule {
	cmd, _ := input["command"].(string)
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}
	rules := make([]Rule, 0, 3)
	// Most specific: the exact command.
	rules = append(rules, Rule{ToolName: "shell_exec", Content: cmd})
	// Mid: prefix on the first token (so "npm install foo" → "prefix:npm").
	if parts := strings.Fields(cmd); len(parts) > 0 {
		rules = append(rules, Rule{ToolName: "shell_exec", Content: "prefix:" + parts[0]})
	}
	// Loosest: whole tool.
	rules = append(rules, Rule{ToolName: "shell_exec"})
	return rules
}

func (shellExecAdapter) DefaultBehavior() RuleBehavior        { return RuleAsk }
func (shellExecAdapter) IsFileEdit() bool                     { return false }
func (shellExecAdapter) TargetPath(_ map[string]any) string   { return "" }

// fileEditAdapter handles edit_text_file, write_text_file, and
// multi_edit. Same matcher (path glob) and suggestion shape; the
// only per-tool variation is which name appears in suggestions.
// Carrying toolName as a field lets us reuse the implementation
// across three adapters without an "is this an edit?" branch.
type fileEditAdapter struct {
	toolName string
}

// FileEditAdapter constructs a file-edit adapter for the given tool.
// Pass "edit_text_file", "write_text_file", or "multi_edit".
func FileEditAdapter(toolName string) Adapter {
	return fileEditAdapter{toolName: toolName}
}

func (fileEditAdapter) MatchContent(input map[string]any, pattern string) bool {
	path := extractFilePath(input)
	return MatchGlob(path, pattern)
}

func (a fileEditAdapter) SuggestRules(input map[string]any) []Rule {
	path := extractFilePath(input)
	if path == "" {
		return nil
	}
	rules := make([]Rule, 0, 3)
	// Exact path.
	rules = append(rules, Rule{ToolName: a.toolName, Content: path})
	// Parent dir glob — `<dir>/**` so subsequent edits in the same
	// directory auto-allow without re-prompting.
	if dir := filepath.Dir(path); dir != "" && dir != "." && dir != "/" {
		rules = append(rules, Rule{ToolName: a.toolName, Content: dir + "/**"})
	}
	// Whole tool.
	rules = append(rules, Rule{ToolName: a.toolName})
	return rules
}

func (fileEditAdapter) DefaultBehavior() RuleBehavior      { return RuleAsk }
func (fileEditAdapter) IsFileEdit() bool                   { return true }
func (fileEditAdapter) TargetPath(input map[string]any) string {
	return extractFilePath(input)
}

// readFileAdapter handles read_text_file (and read_dir if we ever
// register it — same shape). Reads default to allow; the adapter
// exists mainly so user-defined deny rules can still reference
// `read_text_file(**/.env)` and have content matching work.
type readFileAdapter struct {
	toolName string
}

// ReadFileAdapter constructs a read adapter for the given tool name.
func ReadFileAdapter(toolName string) Adapter {
	return readFileAdapter{toolName: toolName}
}

func (readFileAdapter) MatchContent(input map[string]any, pattern string) bool {
	path := extractFilePath(input)
	return MatchGlob(path, pattern)
}

func (a readFileAdapter) SuggestRules(input map[string]any) []Rule {
	path := extractFilePath(input)
	if path == "" {
		return nil
	}
	rules := make([]Rule, 0, 3)
	rules = append(rules, Rule{ToolName: a.toolName, Content: path})
	if dir := filepath.Dir(path); dir != "" && dir != "." && dir != "/" {
		rules = append(rules, Rule{ToolName: a.toolName, Content: dir + "/**"})
	}
	rules = append(rules, Rule{ToolName: a.toolName})
	return rules
}

func (readFileAdapter) DefaultBehavior() RuleBehavior      { return RuleAllow }
func (readFileAdapter) IsFileEdit() bool                   { return false }
func (readFileAdapter) TargetPath(input map[string]any) string {
	return extractFilePath(input)
}

// RegisterDefaultAdapters wires the built-in tool adapters onto the
// given engine. Called once during agent setup. The list is the
// minimum needed for the engine to be useful out of the box; tools
// without adapters fall through to the per-tool default (Ask) and
// rely on user-supplied allow rules to auto-approve.
func RegisterDefaultAdapters(eng *Engine) {
	eng.RegisterAdapter("shell_exec", ShellExecAdapter())
	eng.RegisterAdapter("edit_text_file", FileEditAdapter("edit_text_file"))
	eng.RegisterAdapter("write_text_file", FileEditAdapter("write_text_file"))
	eng.RegisterAdapter("multi_edit", FileEditAdapter("multi_edit"))
	eng.RegisterAdapter("read_text_file", ReadFileAdapter("read_text_file"))
}
