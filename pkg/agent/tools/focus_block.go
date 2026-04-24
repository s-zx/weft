// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/wshrpc"
	"github.com/s-zx/crest/pkg/wshrpc/wshclient"
	"github.com/s-zx/crest/pkg/wshutil"
)

type focusBlockInput struct {
	BlockID string `json:"block_id"`
}

// FocusBlock sends setblockfocus to the tab's frontend route so the agent can
// steer the user's attention to a specific block (typically one it just
// created or already knows about).
func FocusBlock(tabID string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "focus_block",
		DisplayName: "Focus Block",
		Description: "Focus a specific block on the current tab.",
		ToolLogName: "agent:focus_block",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Block id to focus.",
				},
			},
			"required":             []string{"block_id"},
			"additionalProperties": false,
		},
		ToolCallDesc: func(input any, _ any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, err := parseFocusBlockInput(input)
			if err != nil {
				return fmt.Sprintf("focus_block (invalid input: %v)", err)
			}
			return fmt.Sprintf("focusing block %s", parsed.BlockID)
		},
		ToolAnyCallback: func(input any, _ *uctypes.UIMessageDataToolUse) (any, error) {
			parsed, err := parseFocusBlockInput(input)
			if err != nil {
				return nil, err
			}
			return runFocusBlock(context.Background(), parsed, tabID)
		},
		ToolApproval: approval,
	}
}

func parseFocusBlockInput(input any) (*focusBlockInput, error) {
	params := &focusBlockInput{}
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}
	if err := utilfn.ReUnmarshal(params, input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if params.BlockID == "" {
		return nil, fmt.Errorf("block_id is required")
	}
	return params, nil
}

func runFocusBlock(_ context.Context, params *focusBlockInput, tabID string) (map[string]any, error) {
	if tabID == "" {
		return nil, fmt.Errorf("agent session has no tab context")
	}
	rpcClient := wshclient.GetBareRpcClient()
	err := wshclient.SetBlockFocusCommand(
		rpcClient,
		params.BlockID,
		&wshrpc.RpcOpts{Route: wshutil.MakeTabRouteId(tabID)},
	)
	if err != nil {
		return nil, fmt.Errorf("set block focus: %w", err)
	}
	return map[string]any{"block_id": params.BlockID, "focused": true}, nil
}
