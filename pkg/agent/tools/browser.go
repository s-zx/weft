// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/waveobj"
	"github.com/s-zx/crest/pkg/wcore"
	"github.com/s-zx/crest/pkg/wshrpc"
	"github.com/s-zx/crest/pkg/wshrpc/wshclient"
	"github.com/s-zx/crest/pkg/wshutil"
	"github.com/s-zx/crest/pkg/wstore"
)

type browserBlockIdInput struct {
	BlockId string `json:"block_id"`
}

type browserNavigateInput struct {
	BlockId string `json:"block_id"`
	Url     string `json:"url"`
}

type browserSelectorInput struct {
	BlockId  string `json:"block_id"`
	Selector string `json:"selector"`
}

func parseBrowserInput[T any](input any) (*T, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse input: %w", err)
	}
	return &result, nil
}

func resolveWebBlockInfo(tabID string, blockIdPrefix string) (fullBlockId string, workspaceId string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fullBlockId, err = wcore.ResolveBlockIdFromPrefix(ctx, tabID, blockIdPrefix)
	if err != nil {
		return "", "", fmt.Errorf("block not found: %w", err)
	}
	rpcClient := wshclient.GetBareRpcClient()
	info, err := wshclient.BlockInfoCommand(rpcClient, fullBlockId, &wshrpc.RpcOpts{Timeout: 5000})
	if err != nil {
		return "", "", fmt.Errorf("failed to get block info: %w", err)
	}
	return fullBlockId, info.WorkspaceId, nil
}

func BrowserNavigate(tabID string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "browser.navigate",
		DisplayName: "Navigate Web Block",
		Description: "Navigate a web browser block to a new URL. The block must be a web block (view type 'web').",
		ToolLogName: "browser:navigate",
		Prompt: `browser.navigate: Navigates an existing web block to a new URL.
- The block must already exist as a web block — create one with create_block view="web" first if needed.
- "block_id" accepts a prefix; the agent resolves it to the full ID.
- Use browser.* family ONLY when the user is interacting with a real web page (forms, login flows, JS-rendered content). For static content / docs, web_fetch is faster and cheaper.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Block ID (or prefix) of the web block to navigate",
				},
				"url": map[string]any{
					"type":        "string",
					"description": "URL to navigate to",
				},
			},
			"required":             []string{"block_id", "url"},
			"additionalProperties": false,
		},
		ToolApproval: approval,
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, _ := parseBrowserInput[browserNavigateInput](input)
			if parsed != nil {
				return fmt.Sprintf("navigating web block %s to %s", parsed.BlockId, parsed.Url)
			}
			return "navigating web block"
		},
		ToolTextCallback: func(input any) (string, error) {
			parsed, err := parseBrowserInput[browserNavigateInput](input)
			if err != nil {
				return "", err
			}
			if parsed.BlockId == "" {
				return "", fmt.Errorf("block_id is required")
			}
			if parsed.Url == "" {
				return "", fmt.Errorf("url is required")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			fullBlockId, err := wcore.ResolveBlockIdFromPrefix(ctx, tabID, parsed.BlockId)
			if err != nil {
				return "", fmt.Errorf("block not found: %w", err)
			}

			blockORef := waveobj.MakeORef(waveobj.OType_Block, fullBlockId)
			meta := map[string]any{"url": parsed.Url}
			if err := wstore.UpdateObjectMeta(ctx, blockORef, meta, false); err != nil {
				return "", fmt.Errorf("failed to update web block URL: %w", err)
			}
			wcore.SendWaveObjUpdate(blockORef)
			return fmt.Sprintf("Navigated block %s to %s", fullBlockId[:8], parsed.Url), nil
		},
	}
}

func BrowserReadText(tabID string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "browser.read_text",
		DisplayName: "Read Web Page Text",
		Description: "Read the text content of a web block's page. Optionally specify a CSS selector to read a specific element. Returns the inner HTML text.",
		ToolLogName: "browser:readtext",
		Prompt: `browser.read_text: Reads inner text from a web block's rendered DOM.
- Use after the page has loaded — JS-rendered content is fully resolved (unlike web_fetch which sees only the initial HTML).
- "selector" defaults to "body". Provide a specific selector to scope output (e.g. "main", "#article", ".post-body") so you don't pay for full-page tokens.
- For static documentation pages, prefer web_fetch — it doesn't require a live web block.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Block ID (or prefix) of the web block",
				},
				"selector": map[string]any{
					"type":        "string",
					"description": "CSS selector to read (default: 'body')",
				},
			},
			"required":             []string{"block_id"},
			"additionalProperties": false,
		},
		ToolApproval: approval,
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, _ := parseBrowserInput[browserSelectorInput](input)
			if parsed != nil {
				sel := parsed.Selector
				if sel == "" {
					sel = "body"
				}
				return fmt.Sprintf("reading %q from web block %s", sel, parsed.BlockId)
			}
			return "reading web page text"
		},
		ToolTextCallback: func(input any) (string, error) {
			parsed, err := parseBrowserInput[browserSelectorInput](input)
			if err != nil {
				return "", err
			}
			if parsed.BlockId == "" {
				return "", fmt.Errorf("block_id is required")
			}
			selector := parsed.Selector
			if selector == "" {
				selector = "body"
			}

			fullBlockId, workspaceId, err := resolveWebBlockInfo(tabID, parsed.BlockId)
			if err != nil {
				return "", err
			}

			rpcClient := wshclient.GetBareRpcClient()
			results, err := wshclient.WebSelectorCommand(rpcClient, wshrpc.CommandWebSelectorData{
				WorkspaceId: workspaceId,
				BlockId:     fullBlockId,
				TabId:       tabID,
				Selector:    selector,
				Opts:        &wshrpc.WebSelectorOpts{Inner: true},
			}, &wshrpc.RpcOpts{Route: wshutil.ElectronRoute, Timeout: 10000})
			if err != nil {
				return "", fmt.Errorf("failed to read web page: %w", err)
			}
			if len(results) == 0 {
				return "", fmt.Errorf("selector %q matched no elements", selector)
			}
			return results[0], nil
		},
	}
}

func BrowserClick(tabID string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "browser.click",
		DisplayName: "Click Web Element",
		Description: "Click an element in a web block's page by CSS selector.",
		ToolLogName: "browser:click",
		Prompt: `browser.click: Clicks an element in a web block by CSS selector.
- Provide a selector specific enough to match exactly one element. Vague selectors that match many elements click the first one (which may not be what you want).
- After clicking, wait/read again before deciding the next step — the click may have navigated, opened a modal, or changed the DOM.
- Do NOT click anything that triggers an alert / confirm / prompt dialog — modal dialogs block the page and the agent loses control. Read the page first; if you see a button that opens a confirm, ask the user.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Block ID (or prefix) of the web block",
				},
				"selector": map[string]any{
					"type":        "string",
					"description": "CSS selector of the element to click",
				},
			},
			"required":             []string{"block_id", "selector"},
			"additionalProperties": false,
		},
		ToolApproval: approval,
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, _ := parseBrowserInput[browserSelectorInput](input)
			if parsed != nil {
				return fmt.Sprintf("clicking %q in web block %s", parsed.Selector, parsed.BlockId)
			}
			return "clicking web element"
		},
		ToolTextCallback: func(input any) (string, error) {
			parsed, err := parseBrowserInput[browserSelectorInput](input)
			if err != nil {
				return "", err
			}
			if parsed.BlockId == "" {
				return "", fmt.Errorf("block_id is required")
			}
			if parsed.Selector == "" {
				return "", fmt.Errorf("selector is required")
			}

			fullBlockId, workspaceId, err := resolveWebBlockInfo(tabID, parsed.BlockId)
			if err != nil {
				return "", err
			}

			rpcClient := wshclient.GetBareRpcClient()
			_, err = wshclient.WebClickCommand(rpcClient, wshrpc.CommandWebClickData{
				WorkspaceId: workspaceId,
				BlockId:     fullBlockId,
				TabId:       tabID,
				Selector:    parsed.Selector,
			}, &wshrpc.RpcOpts{Route: wshutil.ElectronRoute, Timeout: 10000})
			if err != nil {
				return "", fmt.Errorf("failed to click element: %w", err)
			}
			return fmt.Sprintf("Clicked element matching %q", parsed.Selector), nil
		},
	}
}

func BrowserScreenshot(tabID string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:             "browser.screenshot",
		DisplayName:      "Screenshot Web Block",
		Description:      "Capture a screenshot of a web block's page content and return it as a base64-encoded PNG.",
		ToolLogName:      "browser:screenshot",
		RequiredCapabilities: []string{uctypes.AICapabilityImages},
		Prompt: `browser.screenshot: Captures a PNG of the current web block.
- Only available on multimodal models (the tool registration enforces this).
- Use when you need to SEE the rendered layout — visual regressions, confirming a UI bug the user described, finding an element you can't easily select.
- For getting text out of a page, prefer browser.read_text — it's cheaper than image tokens.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Block ID (or prefix) of the web block to screenshot",
				},
			},
			"required":             []string{"block_id"},
			"additionalProperties": false,
		},
		ToolApproval: approval,
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, _ := parseBrowserInput[browserBlockIdInput](input)
			if parsed != nil {
				return fmt.Sprintf("capturing screenshot of web block %s", parsed.BlockId)
			}
			return "capturing web screenshot"
		},
		ToolTextCallback: func(input any) (string, error) {
			parsed, err := parseBrowserInput[browserBlockIdInput](input)
			if err != nil {
				return "", err
			}
			if parsed.BlockId == "" {
				return "", fmt.Errorf("block_id is required")
			}

			fullBlockId, workspaceId, err := resolveWebBlockInfo(tabID, parsed.BlockId)
			if err != nil {
				return "", err
			}

			rpcClient := wshclient.GetBareRpcClient()
			base64PNG, err := wshclient.WebScreenshotCommand(rpcClient, wshrpc.CommandWebScreenshotData{
				WorkspaceId: workspaceId,
				BlockId:     fullBlockId,
				TabId:       tabID,
			}, &wshrpc.RpcOpts{Route: wshutil.ElectronRoute, Timeout: 15000})
			if err != nil {
				return "", fmt.Errorf("failed to capture screenshot: %w", err)
			}
			return base64PNG, nil
		},
	}
}
