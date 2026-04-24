// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"fmt"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

func TestMCPToolName(t *testing.T) {
	tests := []struct {
		server string
		tool   string
		want   string
	}{
		{"filesystem", "read_file", "mcp__filesystem__read_file"},
		{"my-server", "do_thing", "mcp__my-server__do_thing"},
	}
	for _, tt := range tests {
		got := MCPToolName(tt.server, tt.tool)
		if got != tt.want {
			t.Errorf("MCPToolName(%q, %q) = %q, want %q", tt.server, tt.tool, got, tt.want)
		}
	}
}

func TestParseMCPToolName(t *testing.T) {
	tests := []struct {
		fullName   string
		wantServer string
		wantTool   string
		wantOK     bool
	}{
		{"mcp__filesystem__read_file", "filesystem", "read_file", true},
		{"mcp__my-server__do_thing", "my-server", "do_thing", true},
		{"read_text_file", "", "", false},
		{"mcp__nodelimiter", "", "", false},
	}
	for _, tt := range tests {
		server, tool, ok := ParseMCPToolName(tt.fullName)
		if ok != tt.wantOK || server != tt.wantServer || tool != tt.wantTool {
			t.Errorf("ParseMCPToolName(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.fullName, server, tool, ok, tt.wantServer, tt.wantTool, tt.wantOK)
		}
	}
}

func TestMCPToolToDefinition(t *testing.T) {
	tool := mcplib.Tool{
		Name:        "echo",
		Description: "Echoes input back",
		InputSchema: mcplib.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"message": map[string]any{"type": "string", "description": "Message to echo"},
			},
			Required: []string{"message"},
		},
	}

	callFn := func(_ context.Context, name string, args map[string]any) (*mcplib.CallToolResult, error) {
		msg, _ := args["message"].(string)
		return &mcplib.CallToolResult{
			Content: []mcplib.Content{
				mcplib.TextContent{Type: "text", Text: "echo: " + msg},
			},
		}, nil
	}

	td := MCPToolToDefinition("test-server", tool, callFn)

	if td.Name != "mcp__test-server__echo" {
		t.Errorf("Name = %q, want %q", td.Name, "mcp__test-server__echo")
	}
	if td.Description != "Echoes input back" {
		t.Errorf("Description = %q", td.Description)
	}
	if td.ToolLogName != "mcp:test-server:echo" {
		t.Errorf("ToolLogName = %q", td.ToolLogName)
	}
	if td.ToolApproval == nil {
		t.Fatal("ToolApproval is nil")
	}
	if td.ToolApproval(nil) != uctypes.ApprovalNeedsApproval {
		t.Errorf("ToolApproval should always return needs-approval")
	}

	result, err := td.ToolTextCallback(map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("ToolTextCallback error: %v", err)
	}
	if result != "echo: hello" {
		t.Errorf("ToolTextCallback result = %q, want %q", result, "echo: hello")
	}
}

func TestMCPToolToDefinition_Error(t *testing.T) {
	tool := mcplib.Tool{
		Name:        "fail",
		Description: "Always fails",
		InputSchema: mcplib.ToolInputSchema{Type: "object", Properties: map[string]any{}},
	}

	callFn := func(_ context.Context, name string, args map[string]any) (*mcplib.CallToolResult, error) {
		return nil, fmt.Errorf("connection lost")
	}

	td := MCPToolToDefinition("broken", tool, callFn)
	_, err := td.ToolTextCallback(nil)
	if err == nil {
		t.Fatal("expected error from ToolTextCallback")
	}
}

func TestMCPToolToDefinition_IsError(t *testing.T) {
	tool := mcplib.Tool{
		Name:        "maybe",
		Description: "Might fail",
		InputSchema: mcplib.ToolInputSchema{Type: "object", Properties: map[string]any{}},
	}

	callFn := func(_ context.Context, name string, args map[string]any) (*mcplib.CallToolResult, error) {
		return &mcplib.CallToolResult{
			IsError: true,
			Content: []mcplib.Content{
				mcplib.TextContent{Type: "text", Text: "something went wrong"},
			},
		}, nil
	}

	td := MCPToolToDefinition("srv", tool, callFn)
	result, err := td.ToolTextCallback(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Error: something went wrong" {
		t.Errorf("result = %q, want error text", result)
	}
}

func TestExtractTextFromResult(t *testing.T) {
	tests := []struct {
		name   string
		result *mcplib.CallToolResult
		want   string
	}{
		{"nil", nil, ""},
		{"single text", &mcplib.CallToolResult{
			Content: []mcplib.Content{mcplib.TextContent{Type: "text", Text: "hello"}},
		}, "hello"},
		{"multi text", &mcplib.CallToolResult{
			Content: []mcplib.Content{
				mcplib.TextContent{Type: "text", Text: "line1"},
				mcplib.TextContent{Type: "text", Text: "line2"},
			},
		}, "line1\nline2"},
		{"image", &mcplib.CallToolResult{
			Content: []mcplib.Content{mcplib.ImageContent{Type: "image", MIMEType: "image/png"}},
		}, "[image: image/png]"},
		{"error result", &mcplib.CallToolResult{
			IsError: true,
			Content: []mcplib.Content{mcplib.TextContent{Type: "text", Text: "bad"}},
		}, "Error: bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTextFromResult(tt.result)
			if got != tt.want {
				t.Errorf("extractTextFromResult() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInputToMap(t *testing.T) {
	m := inputToMap(map[string]any{"key": "value"})
	if m["key"] != "value" {
		t.Errorf("direct map passthrough failed")
	}
	if inputToMap(nil) != nil {
		t.Errorf("nil input should return nil")
	}
}
