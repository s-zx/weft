// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"testing"
)

func TestParseBrowserNavigateInput(t *testing.T) {
	input := map[string]any{"block_id": "abc12345", "url": "https://example.com"}
	parsed, err := parseBrowserInput[browserNavigateInput](input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.BlockId != "abc12345" {
		t.Errorf("BlockId = %q, want %q", parsed.BlockId, "abc12345")
	}
	if parsed.Url != "https://example.com" {
		t.Errorf("Url = %q", parsed.Url)
	}
}

func TestParseBrowserNavigateInput_Nil(t *testing.T) {
	_, err := parseBrowserInput[browserNavigateInput](nil)
	if err != nil {
		t.Fatalf("nil input should parse to zero struct, got error: %v", err)
	}
}

func TestParseBrowserSelectorInput(t *testing.T) {
	input := map[string]any{"block_id": "xyz", "selector": "h1.title"}
	parsed, err := parseBrowserInput[browserSelectorInput](input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.BlockId != "xyz" {
		t.Errorf("BlockId = %q", parsed.BlockId)
	}
	if parsed.Selector != "h1.title" {
		t.Errorf("Selector = %q", parsed.Selector)
	}
}

func TestParseBrowserSelectorInput_OptionalSelector(t *testing.T) {
	input := map[string]any{"block_id": "abc"}
	parsed, err := parseBrowserInput[browserSelectorInput](input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Selector != "" {
		t.Errorf("Selector should be empty when not provided, got %q", parsed.Selector)
	}
}

func TestParseBrowserBlockIdInput(t *testing.T) {
	input := map[string]any{"block_id": "12345678"}
	parsed, err := parseBrowserInput[browserBlockIdInput](input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.BlockId != "12345678" {
		t.Errorf("BlockId = %q", parsed.BlockId)
	}
}

func TestBrowserNavigateToolDefinition(t *testing.T) {
	td := BrowserNavigate("tab-123", func(any) string { return "auto-approved" })
	if td.Name != "browser.navigate" {
		t.Errorf("Name = %q", td.Name)
	}
	if td.ToolLogName != "browser:navigate" {
		t.Errorf("ToolLogName = %q", td.ToolLogName)
	}
	if td.ToolTextCallback == nil {
		t.Error("ToolTextCallback is nil")
	}
	if td.ToolApproval == nil {
		t.Error("ToolApproval is nil")
	}
}

func TestBrowserReadTextToolDefinition(t *testing.T) {
	td := BrowserReadText("tab-123", func(any) string { return "needs-approval" })
	if td.Name != "browser.read_text" {
		t.Errorf("Name = %q", td.Name)
	}
}

func TestBrowserClickToolDefinition(t *testing.T) {
	td := BrowserClick("tab-123", func(any) string { return "needs-approval" })
	if td.Name != "browser.click" {
		t.Errorf("Name = %q", td.Name)
	}
}

func TestBrowserScreenshotToolDefinition(t *testing.T) {
	td := BrowserScreenshot("tab-123", func(any) string { return "needs-approval" })
	if td.Name != "browser.screenshot" {
		t.Errorf("Name = %q", td.Name)
	}
	if len(td.RequiredCapabilities) == 0 {
		t.Error("screenshot should require image capability")
	}
}

func TestBrowserToolCallDesc(t *testing.T) {
	td := BrowserNavigate("tab-123", func(any) string { return "auto" })
	desc := td.ToolCallDesc(map[string]any{"block_id": "abc", "url": "https://example.com"}, nil, nil)
	if desc == "" {
		t.Error("ToolCallDesc returned empty string")
	}
}
