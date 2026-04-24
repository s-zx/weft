// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/waveobj"
	"github.com/s-zx/crest/pkg/wcore"
	"github.com/s-zx/crest/pkg/wps"
)

type createBlockInput struct {
	View          string `json:"view"`
	Cmd           string `json:"cmd,omitempty"`
	Cwd           string `json:"cwd,omitempty"`
	Connection    string `json:"connection,omitempty"`
	Url           string `json:"url,omitempty"`
	File          string `json:"file,omitempty"`
	TargetAction  string `json:"target_action,omitempty"`
	TargetBlockID string `json:"target_block_id,omitempty"`
	Focused       bool   `json:"focused,omitempty"`
}

type createBlockOutput struct {
	BlockID string `json:"block_id"`
}

// CreateBlock lets the agent open a new Crest block (terminal, preview, web)
// adjacent to the session's terminal. It mirrors the subset of
// wshserver.CreateBlockCommand that makes sense for agent-initiated blocks:
// the block is created server-side, a layout action is queued, and any pending
// wave-object updates are flushed to the frontend.
func CreateBlock(tabID, defaultTargetBlockID, defaultConnection string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "create_block",
		DisplayName: "Create Block",
		Description: "Open a new block on the current tab. Supports view=\"term\" (new terminal), view=\"preview\" (file preview), view=\"web\" (browser). Positions itself next to the agent's terminal by default via splitdown.",
		ToolLogName: "agent:create_block",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"view": map[string]any{
					"type":        "string",
					"enum":        []string{"term", "preview", "web"},
					"description": "Block view type.",
				},
				"cmd": map[string]any{
					"type":        "string",
					"description": "Command to run (term view only). If omitted, opens an interactive shell.",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory (term view).",
				},
				"connection": map[string]any{
					"type":        "string",
					"description": "Remote connection name (optional). Defaults to the agent's current connection.",
				},
				"url": map[string]any{
					"type":        "string",
					"description": "URL (web view only).",
				},
				"file": map[string]any{
					"type":        "string",
					"description": "Absolute path (preview view only).",
				},
				"target_action": map[string]any{
					"type":        "string",
					"enum":        []string{"splitdown", "splitright", "splitup", "splitleft", "insert"},
					"default":     "splitdown",
					"description": "Where the new block appears relative to the agent's terminal.",
				},
				"target_block_id": map[string]any{
					"type":        "string",
					"description": "Override the split target; defaults to the agent's terminal block.",
				},
				"focused": map[string]any{
					"type":        "boolean",
					"description": "Focus the new block after creation.",
				},
			},
			"required":             []string{"view"},
			"additionalProperties": false,
		},
		ToolCallDesc: func(input any, _ any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, err := parseCreateBlockInput(input)
			if err != nil {
				return fmt.Sprintf("create_block (invalid input: %v)", err)
			}
			return fmt.Sprintf("creating %s block", parsed.View)
		},
		ToolAnyCallback: func(input any, toolUseData *uctypes.UIMessageDataToolUse) (any, error) {
			parsed, err := parseCreateBlockInput(input)
			if err != nil {
				return nil, err
			}
			out, err := runCreateBlock(context.Background(), parsed, tabID, defaultTargetBlockID, defaultConnection)
			if err != nil {
				return nil, err
			}
			if toolUseData != nil {
				toolUseData.BlockId = out.BlockID
			}
			return out, nil
		},
		ToolApproval: approval,
	}
}

func parseCreateBlockInput(input any) (*createBlockInput, error) {
	params := &createBlockInput{}
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}
	if err := utilfn.ReUnmarshal(params, input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	params.View = strings.TrimSpace(params.View)
	if params.View == "" {
		return nil, fmt.Errorf("view is required")
	}
	switch params.View {
	case "term", "preview", "web":
	default:
		return nil, fmt.Errorf("unsupported view %q", params.View)
	}
	if params.View == "web" && params.Url == "" {
		return nil, fmt.Errorf("url is required for web view")
	}
	if params.View == "preview" && params.File == "" {
		return nil, fmt.Errorf("file is required for preview view")
	}
	if params.TargetAction == "" {
		params.TargetAction = "splitdown"
	}
	return params, nil
}

func runCreateBlock(ctx context.Context, params *createBlockInput, tabID, defaultTargetBlockID, defaultConnection string) (*createBlockOutput, error) {
	if tabID == "" {
		return nil, fmt.Errorf("agent session has no tab context")
	}
	ctx = waveobj.ContextWithUpdates(ctx)

	meta := waveobj.MetaMapType{
		waveobj.MetaKey_View: params.View,
	}
	connection := params.Connection
	if connection == "" {
		connection = defaultConnection
	}
	if connection != "" {
		meta[waveobj.MetaKey_Connection] = connection
	}
	switch params.View {
	case "term":
		meta[waveobj.MetaKey_Controller] = "shell"
		if params.Cmd != "" {
			meta[waveobj.MetaKey_Controller] = "cmd"
			meta[waveobj.MetaKey_Cmd] = params.Cmd
			meta[waveobj.MetaKey_CmdRunOnStart] = true
		}
		if params.Cwd != "" {
			meta[waveobj.MetaKey_CmdCwd] = params.Cwd
		}
	case "preview":
		meta[waveobj.MetaKey_File] = params.File
	case "web":
		meta[waveobj.MetaKey_Url] = params.Url
	}

	blockDef := &waveobj.BlockDef{Meta: meta}
	block, err := wcore.CreateBlock(ctx, tabID, blockDef, nil)
	if err != nil {
		return nil, fmt.Errorf("create block: %w", err)
	}

	target := params.TargetBlockID
	if target == "" {
		target = defaultTargetBlockID
	}

	layoutAction, err := buildLayoutAction(block.OID, target, params.TargetAction, params.Focused)
	if err != nil {
		return nil, err
	}
	if err := wcore.QueueLayoutActionForTab(ctx, tabID, *layoutAction); err != nil {
		return nil, fmt.Errorf("queue layout action: %w", err)
	}

	wps.Broker.SendUpdateEvents(waveobj.ContextGetUpdatesRtn(ctx))
	return &createBlockOutput{BlockID: block.OID}, nil
}

func buildLayoutAction(newBlockID, targetBlockID, targetAction string, focused bool) (*waveobj.LayoutActionData, error) {
	if targetBlockID == "" {
		return &waveobj.LayoutActionData{
			ActionType: wcore.LayoutActionDataType_Insert,
			BlockId:    newBlockID,
			Focused:    focused,
		}, nil
	}
	switch targetAction {
	case "splitright":
		return &waveobj.LayoutActionData{
			ActionType:    wcore.LayoutActionDataType_SplitHorizontal,
			BlockId:       newBlockID,
			TargetBlockId: targetBlockID,
			Position:      "after",
			Focused:       focused,
		}, nil
	case "splitleft":
		return &waveobj.LayoutActionData{
			ActionType:    wcore.LayoutActionDataType_SplitHorizontal,
			BlockId:       newBlockID,
			TargetBlockId: targetBlockID,
			Position:      "before",
			Focused:       focused,
		}, nil
	case "splitup":
		return &waveobj.LayoutActionData{
			ActionType:    wcore.LayoutActionDataType_SplitVertical,
			BlockId:       newBlockID,
			TargetBlockId: targetBlockID,
			Position:      "before",
			Focused:       focused,
		}, nil
	case "splitdown", "":
		return &waveobj.LayoutActionData{
			ActionType:    wcore.LayoutActionDataType_SplitVertical,
			BlockId:       newBlockID,
			TargetBlockId: targetBlockID,
			Position:      "after",
			Focused:       focused,
		}, nil
	case "insert":
		return &waveobj.LayoutActionData{
			ActionType: wcore.LayoutActionDataType_Insert,
			BlockId:    newBlockID,
			Focused:    focused,
		}, nil
	}
	return nil, fmt.Errorf("unsupported target_action %q", targetAction)
}
