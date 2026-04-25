// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/util/utilfn"
)

const (
	searchDefaultMaxResults = 50
	searchMaxMaxResults     = 200
	searchMaxLineLen        = 300
)

type searchInput struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path,omitempty"`
	Glob          string `json:"glob,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
	IncludeHidden bool   `json:"include_hidden,omitempty"`
}

type rgMatch struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		LineNumber int `json:"line_number"`
		Lines      struct {
			Text string `json:"text"`
		} `json:"lines"`
	} `json:"data"`
}

func Search(approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "search",
		DisplayName: "Search",
		Description: "Fast regex search across files using ripgrep. Returns matching lines with file paths and line numbers. Supports glob filters and hidden files. Use for finding code patterns, function definitions, imports, configuration values, etc.",
		ToolLogName: "agent:search",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern to search for.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search in. Defaults to the current working directory.",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "Glob filter, e.g. \"*.go\" or \"*.{ts,tsx}\".",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     searchMaxMaxResults,
					"default":     searchDefaultMaxResults,
					"description": "Maximum number of matching lines to return. Default 50, max 200.",
				},
				"include_hidden": map[string]any{
					"type":        "boolean",
					"default":     false,
					"description": "Include hidden files and directories in the search.",
				},
			},
			"required":             []string{"pattern"},
			"additionalProperties": false,
		},
		Parallel: true,
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, err := parseSearchInput(input)
			if err != nil {
				return fmt.Sprintf("search (invalid: %v)", err)
			}
			dir := parsed.Path
			if dir == "" {
				dir = "."
			}
			if output != nil {
				return fmt.Sprintf("searched for %q in %s", parsed.Pattern, dir)
			}
			return fmt.Sprintf("searching for %q in %s", parsed.Pattern, dir)
		},
		ToolTextCallback: func(input any) (string, error) {
			parsed, err := parseSearchInput(input)
			if err != nil {
				return "", err
			}
			return runSearch(parsed)
		},
		ToolApproval: approval,
	}
}

func parseSearchInput(input any) (*searchInput, error) {
	params := &searchInput{}
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}
	if err := utilfn.ReUnmarshal(params, input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	params.Pattern = strings.TrimSpace(params.Pattern)
	if params.Pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if params.MaxResults <= 0 {
		params.MaxResults = searchDefaultMaxResults
	}
	if params.MaxResults > searchMaxMaxResults {
		params.MaxResults = searchMaxMaxResults
	}
	return params, nil
}

func runSearch(params *searchInput) (string, error) {
	result, err := runRipgrep(params)
	if err != nil {
		result, err = runGrepFallback(params)
		if err != nil {
			return "", err
		}
	}
	if result == "" {
		return "No matches found.", nil
	}
	return result, nil
}

func runRipgrep(params *searchInput) (string, error) {
	args := []string{
		"--json",
		"--max-count", fmt.Sprintf("%d", params.MaxResults),
	}
	if params.Glob != "" {
		args = append(args, "--glob", params.Glob)
	}
	if params.IncludeHidden {
		args = append(args, "--hidden")
	}
	args = append(args, params.Pattern)
	if params.Path != "" {
		args = append(args, params.Path)
	}

	cmd := exec.Command("rg", args...)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("ripgrep failed: %w", err)
	}

	return parseRipgrepJSON(string(output), params.MaxResults), nil
}

func parseRipgrepJSON(output string, maxResults int) string {
	var sb strings.Builder
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if count >= maxResults {
			break
		}
		line := scanner.Text()
		var m rgMatch
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if m.Type != "match" {
			continue
		}
		content := strings.TrimRight(m.Data.Lines.Text, "\n\r")
		content = utilfn.TruncateString(content, searchMaxLineLen)
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(fmt.Sprintf("%s:%d: %s", m.Data.Path.Text, m.Data.LineNumber, content))
		count++
	}
	return sb.String()
}

func runGrepFallback(params *searchInput) (string, error) {
	args := []string{"-rn"}
	if params.Glob != "" {
		args = append(args, "--include="+params.Glob)
	}
	args = append(args, params.Pattern)

	searchPath := params.Path
	if searchPath == "" {
		searchPath = "."
	}
	args = append(args, searchPath)

	cmd := exec.Command("grep", args...)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("grep failed: %w", err)
	}

	return truncGrepOutput(string(output), params.MaxResults), nil
}

func truncGrepOutput(output string, maxResults int) string {
	var sb strings.Builder
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if count >= maxResults {
			break
		}
		line := utilfn.TruncateString(scanner.Text(), searchMaxLineLen)
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(line)
		count++
	}
	return sb.String()
}
