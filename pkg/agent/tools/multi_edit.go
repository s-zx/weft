// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/filebackup"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/wavebase"
)

type multiEditInput struct {
	Filename string   `json:"filename"`
	Edits    []editOp `json:"edits"`
}

type editOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type multiEditOutput struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	EditsApplied int    `json:"edits_applied"`
}

func parseMultiEditInput(input any) (*multiEditInput, error) {
	params := &multiEditInput{}
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}
	if err := utilfn.ReUnmarshal(params, input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if params.Filename == "" {
		return nil, fmt.Errorf("filename is required")
	}
	if len(params.Edits) == 0 {
		return nil, fmt.Errorf("edits is required")
	}
	for i, edit := range params.Edits {
		if edit.OldString == "" {
			return nil, fmt.Errorf("edit[%d]: old_string must not be empty", i)
		}
	}
	return params, nil
}

func applyMultiEdits(content string, edits []editOp) (string, error) {
	for i, edit := range edits {
		if !strings.Contains(content, edit.OldString) {
			return "", fmt.Errorf("edit[%d]: old_string not found in file", i)
		}
		if edit.ReplaceAll {
			content = strings.ReplaceAll(content, edit.OldString, edit.NewString)
		} else {
			content = strings.Replace(content, edit.OldString, edit.NewString, 1)
		}
	}
	return content, nil
}

func MultiEdit(approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "multi_edit",
		DisplayName: "Multi Edit",
		Description: "Apply multiple search-and-replace edits to a single file atomically. All edits succeed or none are applied. More efficient than multiple edit_text_file calls.",
		ToolLogName: "agent:multi_edit",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename": map[string]any{"type": "string", "description": "Absolute path to the file to edit."},
				"edits": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string": map[string]any{"type": "string", "description": "Exact text to find."},
							"new_string": map[string]any{"type": "string", "description": "Text to replace it with."},
							"replace_all": map[string]any{"type": "boolean", "default": false, "description": "Replace all occurrences."},
						},
						"required": []string{"old_string", "new_string"},
					},
					"description": "Ordered list of edits. Applied sequentially in memory, then written atomically.",
				},
			},
			"required": []string{"filename", "edits"},
		},
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, err := parseMultiEditInput(input)
			if err != nil {
				return fmt.Sprintf("multi_edit (invalid: %v)", err)
			}
			if output != nil {
				return fmt.Sprintf("edited %q (%d edits)", parsed.Filename, len(parsed.Edits))
			}
			return fmt.Sprintf("editing %q", parsed.Filename)
		},
		ToolVerifyInput: func(input any, toolUseData *uctypes.UIMessageDataToolUse) error {
			parsed, err := parseMultiEditInput(input)
			if err != nil {
				return err
			}
			expandedPath, err := wavebase.ExpandHomeDir(parsed.Filename)
			if err != nil {
				return fmt.Errorf("failed to expand path: %w", err)
			}
			if !filepath.IsAbs(expandedPath) {
				return fmt.Errorf("path must be absolute, got relative path: %s", parsed.Filename)
			}
			fileInfo, err := os.Stat(expandedPath)
			if err != nil {
				return fmt.Errorf("cannot stat file: %w", err)
			}
			if fileInfo.IsDir() {
				return fmt.Errorf("path is a directory, not a file")
			}
			if toolUseData != nil {
				toolUseData.InputFileName = expandedPath

				original, err := os.ReadFile(expandedPath)
				if err == nil {
					modified, applyErr := applyMultiEdits(string(original), parsed.Edits)
					if applyErr == nil {
						toolUseData.OriginalContent = string(original)
						toolUseData.ModifiedContent = modified
					}
				}
			}
			return nil
		},
		ToolAnyCallback: func(input any, toolUseData *uctypes.UIMessageDataToolUse) (any, error) {
			parsed, err := parseMultiEditInput(input)
			if err != nil {
				return nil, err
			}
			expandedPath, err := wavebase.ExpandHomeDir(parsed.Filename)
			if err != nil {
				return nil, fmt.Errorf("failed to expand path: %w", err)
			}
			if !filepath.IsAbs(expandedPath) {
				return nil, fmt.Errorf("path must be absolute, got relative path: %s", parsed.Filename)
			}
			fileInfo, err := os.Stat(expandedPath)
			if err != nil {
				return nil, fmt.Errorf("cannot stat file: %w", err)
			}
			if fileInfo.IsDir() {
				return nil, fmt.Errorf("path is a directory, not a file")
			}

			original, err := os.ReadFile(expandedPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			modified, err := applyMultiEdits(string(original), parsed.Edits)
			if err != nil {
				return nil, err
			}

			backupPath, err := filebackup.MakeFileBackup(expandedPath)
			if err != nil {
				return nil, fmt.Errorf("failed to create backup: %w", err)
			}
			if toolUseData != nil {
				toolUseData.InputFileName = expandedPath
				toolUseData.WriteBackupFileName = backupPath
			}

			err = os.WriteFile(expandedPath, []byte(modified), fileInfo.Mode().Perm())
			if err != nil {
				return nil, fmt.Errorf("failed to write file: %w", err)
			}

			return &multiEditOutput{
				Success:      true,
				Message:      fmt.Sprintf("Successfully applied %d edits to %s", len(parsed.Edits), parsed.Filename),
				EditsApplied: len(parsed.Edits),
			}, nil
		},
		ToolApproval: approval,
	}
}
