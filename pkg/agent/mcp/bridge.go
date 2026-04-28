// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

const ToolPrefix = "mcp__"

func MCPToolName(serverName string, toolName string) string {
	return ToolPrefix + serverName + "__" + toolName
}

func ParseMCPToolName(fullName string) (serverName string, toolName string, ok bool) {
	if !strings.HasPrefix(fullName, ToolPrefix) {
		return "", "", false
	}
	rest := fullName[len(ToolPrefix):]
	idx := strings.Index(rest, "__")
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+2:], true
}

type ToolCallFn func(ctx context.Context, toolName string, args map[string]any) (*mcplib.CallToolResult, error)

func MCPToolToDefinition(serverName string, tool mcplib.Tool, callFn ToolCallFn) uctypes.ToolDefinition {
	fullName := MCPToolName(serverName, tool.Name)
	inputSchema := mcpInputSchemaToMap(tool)

	return uctypes.ToolDefinition{
		Name:             fullName,
		DisplayName:      fmt.Sprintf("[%s] %s", serverName, tool.Name),
		Description:      tool.Description,
		ShortDescription: fmt.Sprintf("MCP tool from %s server", serverName),
		ToolLogName:      fmt.Sprintf("mcp:%s:%s", serverName, tool.Name),
		InputSchema:      inputSchema,
		ToolTextCallback: func(input any) (string, error) {
			args := inputToMap(input)
			result, err := callFn(context.Background(), tool.Name, args)
			if err != nil {
				return "", fmt.Errorf("MCP tool %s error: %w", fullName, err)
			}
			return extractTextFromResult(result), nil
		},
		ToolApproval: func(any) string {
			return uctypes.ApprovalNeedsApproval
		},
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			return fmt.Sprintf("MCP: %s.%s", serverName, tool.Name)
		},
	}
}

func mcpInputSchemaToMap(tool mcplib.Tool) map[string]any {
	toolJSON, err := json.Marshal(tool)
	if err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var toolMap map[string]any
	if err := json.Unmarshal(toolJSON, &toolMap); err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if schema, ok := toolMap["inputSchema"].(map[string]any); ok {
		return schema
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func inputToMap(input any) map[string]any {
	if input == nil {
		return nil
	}
	if m, ok := input.(map[string]any); ok {
		return m
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func extractTextFromResult(result *mcplib.CallToolResult) string {
	if result == nil {
		return ""
	}
	if result.IsError {
		for _, c := range result.Content {
			if tc, ok := c.(mcplib.TextContent); ok {
				return fmt.Sprintf("Error: %s", tc.Text)
			}
		}
		return "Error: MCP tool returned an error"
	}
	var parts []string
	for _, c := range result.Content {
		switch ct := c.(type) {
		case mcplib.TextContent:
			parts = append(parts, ct.Text)
		case mcplib.ImageContent:
			parts = append(parts, fmt.Sprintf("[image: %s]", ct.MIMEType))
		default:
			parts = append(parts, "[unsupported content type]")
		}
	}
	return strings.Join(parts, "\n")
}
