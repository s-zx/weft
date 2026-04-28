// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import (
	"fmt"
	"strings"
)

// ParseRule turns a wire-format string into a Rule. The two accepted
// shapes are:
//
//	"shell_exec"               // tool-only, matches any call to shell_exec
//	"shell_exec(prefix:npm)"   // tool + content matcher
//
// The content portion is opaque to the parser — its grammar is owned by
// the per-tool PermissionAdapter (e.g. `prefix:` for shell, glob path
// for file tools). The parser only handles the outer envelope.
//
// Escapes inside the content: `\)` for a literal `)`, `\\` for `\`. A
// trailing `)` without its match returns an error so typos in
// settings.json don't silently neuter a rule.
//
// Behavior and Source are caller-provided because the wire format
// doesn't carry them — they come from which list ("allow" / "deny" /
// "ask") the string was found in, and which file was loaded.
func ParseRule(s string, behavior RuleBehavior, source RuleSource) (Rule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rule{}, fmt.Errorf("empty rule string")
	}

	openIdx := strings.IndexByte(s, '(')
	if openIdx < 0 {
		// Tool-only form: "shell_exec" or "*" or "mcp__server__*".
		toolName := s
		if err := validateToolName(toolName); err != nil {
			return Rule{}, err
		}
		return Rule{
			Behavior: behavior,
			ToolName: toolName,
			Content:  "",
			Source:   source,
		}, nil
	}

	toolName := strings.TrimSpace(s[:openIdx])
	if err := validateToolName(toolName); err != nil {
		return Rule{}, err
	}

	// Walk the remainder honoring backslash escapes so a content
	// pattern like `shell_exec(echo \))` parses correctly.
	rest := s[openIdx+1:]
	var content strings.Builder
	closed := false
	i := 0
	for i < len(rest) {
		c := rest[i]
		if c == '\\' && i+1 < len(rest) {
			next := rest[i+1]
			if next == ')' || next == '\\' {
				content.WriteByte(next)
				i += 2
				continue
			}
		}
		if c == ')' {
			closed = true
			i++
			break
		}
		content.WriteByte(c)
		i++
	}
	if !closed {
		return Rule{}, fmt.Errorf("unterminated content (missing `)`): %q", s)
	}
	if i < len(rest) {
		// Anything after the closing paren is invalid — catch typos
		// like "shell_exec(prefix:npm) extra".
		trailing := strings.TrimSpace(rest[i:])
		if trailing != "" {
			return Rule{}, fmt.Errorf("trailing content after `)`: %q", s)
		}
	}

	return Rule{
		Behavior: behavior,
		ToolName: toolName,
		Content:  content.String(),
		Source:   source,
	}, nil
}

// validateToolName rejects obvious nonsense at parse time. We don't
// know the full set of legal tool names here (tools register at
// runtime), but `*` and `mcp__server__*` shapes plus a permissive
// identifier check catches every real-world typo.
func validateToolName(name string) error {
	if name == "" {
		return fmt.Errorf("empty tool name")
	}
	if name == "*" {
		return nil
	}
	for i, c := range name {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_':
		case c == '*' && i == len(name)-1:
			// Trailing `*` allowed for `mcp__server__*` shape.
		case c == '.' || c == '-':
			// Some tools use `browser.navigate`-style names.
		default:
			return fmt.Errorf("invalid character %q in tool name %q", c, name)
		}
	}
	return nil
}

// String renders a Rule back to wire form. Round-trips with ParseRule
// for any rule that came from ParseRule. Behavior and Source are
// intentionally not in the wire form — they're recovered from where
// the string is stored.
func (r Rule) String() string {
	if r.Content == "" {
		return r.ToolName
	}
	// Escape `)` and `\` inside the content so the parser can recover
	// the original. Other characters pass through.
	var escaped strings.Builder
	escaped.Grow(len(r.Content))
	for i := 0; i < len(r.Content); i++ {
		c := r.Content[i]
		if c == ')' || c == '\\' {
			escaped.WriteByte('\\')
		}
		escaped.WriteByte(c)
	}
	return r.ToolName + "(" + escaped.String() + ")"
}

// Matches reports whether this rule's tool name matches the given
// concrete tool name. It does NOT consult the content matcher — the
// caller is expected to do that via the tool's PermissionAdapter.
//
// Wildcard handling:
//   - "*" matches any tool.
//   - "mcp__server__*" matches any MCP tool from `server`.
//   - exact match otherwise.
func (r Rule) Matches(toolName string) bool {
	if r.ToolName == "*" {
		return true
	}
	if strings.HasSuffix(r.ToolName, "*") {
		prefix := strings.TrimSuffix(r.ToolName, "*")
		return strings.HasPrefix(toolName, prefix)
	}
	return r.ToolName == toolName
}

// Specificity returns a coarse ranking used to break ties when
// multiple rules match the same call. Higher score = more specific =
// wins. Used by the engine's decision pipeline to pick "more specific
// content patterns beat broader ones" within a single scope.
func (r Rule) Specificity() int {
	score := 0
	if r.ToolName == "*" {
		score += 0
	} else if strings.HasSuffix(r.ToolName, "*") {
		score += 5
	} else {
		score += 10
	}
	if r.Content != "" {
		// Length isn't a perfect proxy for specificity, but for
		// path globs and shell prefixes it correlates strongly:
		// `prefix:git push` (15) > `prefix:git` (10) > `*` (1).
		score += len(r.Content)
	}
	return score
}
