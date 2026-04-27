// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

//go:embed prompts/*.md
var promptFS embed.FS

var (
	sharedHeader = mustLoadPrompt("shared_header.md")
	askPrompt    = mustLoadPrompt("ask.md")
	planPrompt   = mustLoadPrompt("plan.md")
	doPrompt     = mustLoadPrompt("do.md")
	benchPrompt  = mustLoadPrompt("bench.md")
)

func mustLoadPrompt(name string) string {
	data, err := promptFS.ReadFile("prompts/" + name)
	if err != nil {
		panic(fmt.Sprintf("agent: failed to load prompt %s: %v", name, err))
	}
	return strings.TrimSpace(string(data))
}

// SystemPromptByKey returns the system-prompt parts for the given
// API mode string: the shared header plus the mode-specific prompt.
// Terminal context is appended separately via BuildTerminalContext
// so it updates per request.
//
// Per docs/permissions-v2-design.md §10, the bundled prompt set
// doesn't change in v2 — the Mode struct is gone but the four prompt
// files (ask.md / plan.md / do.md / bench.md) stay shipped and keyed
// off the API mode string. Empty / unknown mode names map to "do"
// (the everyday-coding prompt) via NormalizeMode.
func SystemPromptByKey(name string) []string {
	switch NormalizeMode(name) {
	case ModeAsk:
		return []string{sharedHeader, askPrompt}
	case ModePlan:
		return []string{sharedHeader, planPrompt}
	case ModeBench:
		return []string{sharedHeader, benchPrompt}
	default:
		return []string{sharedHeader, doPrompt}
	}
}

// BuildToolPromptSection returns the per-tool usage guidance block for the
// tools enabled this turn. Keeps tool prompts in a single deterministic block
// (alphabetical by name) so the prompt prefix stays cache-stable across turns
// even when the tool list is constructed in different orders. Tools without
// a Prompt field contribute nothing — Description alone is what the API sees.
func BuildToolPromptSection(tools []uctypes.ToolDefinition) string {
	prompts := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.Prompt == "" {
			continue
		}
		prompts = append(prompts, strings.TrimSpace(t.Prompt))
	}
	if len(prompts) == 0 {
		return ""
	}
	sort.Strings(prompts)
	var b strings.Builder
	b.WriteString("<tool_prompts>\n")
	for _, p := range prompts {
		b.WriteString(p)
		b.WriteString("\n\n")
	}
	b.WriteString("</tool_prompts>")
	return b.String()
}
