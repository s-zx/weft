// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/wavebase"
)

const (
	writePlanDirName    = ".crest-plans"
	writePlanMaxBytes   = 512 * 1024
	writePlanSlugMax    = 60
	writePlanDefaultCwd = ""
)

type writePlanInput struct {
	Title    string `json:"title"`
	Content  string `json:"content"`
	Slug     string `json:"slug,omitempty"`
	OpenView bool   `json:"open_preview,omitempty"`
}

type writePlanOutput struct {
	Path    string `json:"path"`
	BlockID string `json:"block_id,omitempty"`
}

// WritePlan writes a markdown plan to <cwd>/.crest-plans/<slug>.md and, when
// requested, opens a preview block next to the agent's terminal. Plan mode
// uses this as its sole mutation tool so the ApprovalPolicy gates path rather
// than file contents.
func WritePlan(tabID, defaultTargetBlockID, defaultCwd, defaultConnection string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "write_plan",
		DisplayName: "Write Plan",
		Description: "Write a markdown plan to <cwd>/.crest-plans/<slug>.md. Plans are short, actionable design documents the user can review before work begins.",
		ToolLogName: "agent:write_plan",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Human-readable plan title (first heading).",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Full markdown body. Keep under 512KB.",
				},
				"slug": map[string]any{
					"type":        "string",
					"description": "Optional filename slug. Derived from title when omitted.",
				},
				"open_preview": map[string]any{
					"type":        "boolean",
					"description": "Open a preview block for the plan immediately after writing. Default false.",
				},
			},
			"required":             []string{"title", "content"},
			"additionalProperties": false,
		},
		ToolCallDesc: func(input any, _ any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, err := parseWritePlanInput(input)
			if err != nil {
				return fmt.Sprintf("write_plan (invalid input: %v)", err)
			}
			return fmt.Sprintf("writing plan %q", parsed.Title)
		},
		ToolAnyCallback: func(input any, toolUseData *uctypes.UIMessageDataToolUse) (any, error) {
			parsed, err := parseWritePlanInput(input)
			if err != nil {
				return nil, err
			}
			out, err := runWritePlan(context.Background(), parsed, tabID, defaultTargetBlockID, defaultCwd, defaultConnection)
			if err != nil {
				return nil, err
			}
			if toolUseData != nil && out.BlockID != "" {
				toolUseData.BlockId = out.BlockID
			}
			return out, nil
		},
		ToolApproval: approval,
	}
}

func parseWritePlanInput(input any) (*writePlanInput, error) {
	params := &writePlanInput{}
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}
	if err := utilfn.ReUnmarshal(params, input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	params.Title = strings.TrimSpace(params.Title)
	if params.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if params.Content == "" {
		return nil, fmt.Errorf("content is required")
	}
	if len(params.Content) > writePlanMaxBytes {
		return nil, fmt.Errorf("content exceeds %d bytes", writePlanMaxBytes)
	}
	if params.Slug == "" {
		params.Slug = slugify(params.Title)
	} else {
		params.Slug = slugify(params.Slug)
	}
	if params.Slug == "" {
		params.Slug = fmt.Sprintf("plan-%d", time.Now().Unix())
	}
	return params, nil
}

func runWritePlan(ctx context.Context, params *writePlanInput, tabID, defaultTargetBlockID, defaultCwd, defaultConnection string) (*writePlanOutput, error) {
	cwd := defaultCwd
	if cwd == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve cwd: %w", err)
		}
		cwd = home
	}
	expanded, err := wavebase.ExpandHomeDir(cwd)
	if err != nil {
		return nil, fmt.Errorf("expand cwd: %w", err)
	}
	if !filepath.IsAbs(expanded) {
		return nil, fmt.Errorf("cwd must be absolute, got %q", expanded)
	}
	planDir := filepath.Join(expanded, writePlanDirName)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return nil, fmt.Errorf("create plan dir: %w", err)
	}
	planPath := filepath.Join(planDir, params.Slug+".md")
	body := ensureHeading(params.Title, params.Content)
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("write plan: %w", err)
	}
	out := &writePlanOutput{Path: planPath}

	if params.OpenView && tabID != "" {
		createParams := &createBlockInput{
			View:          "preview",
			File:          planPath,
			TargetAction:  "splitdown",
			TargetBlockID: defaultTargetBlockID,
			Focused:       false,
			Connection:    defaultConnection,
		}
		blockOut, err := runCreateBlock(ctx, createParams, tabID, defaultTargetBlockID, defaultConnection)
		if err != nil {
			return nil, fmt.Errorf("plan written but preview open failed: %w (path=%s)", err, planPath)
		}
		out.BlockID = blockOut.BlockID
	}
	return out, nil
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > writePlanSlugMax {
		out = strings.TrimRight(out[:writePlanSlugMax], "-")
	}
	return out
}

func ensureHeading(title, content string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if strings.HasPrefix(trimmed, "# ") {
		return content
	}
	return "# " + title + "\n\n" + content
}
