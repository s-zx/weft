// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package eval

type GoldenTranscript struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Mode        string              `json:"mode"`
	Setup       GoldenSetup         `json:"setup"`
	Turns       []GoldenTurn        `json:"turns"`
	Assertions  GoldenAssertions    `json:"assertions"`
}

type GoldenSetup struct {
	Files map[string]string `json:"files"`
}

type GoldenTurn struct {
	User      string            `json:"user"`
	Responses []GoldenResponse  `json:"responses"`
}

type GoldenResponse struct {
	Text      string            `json:"text"`
	ToolCalls []GoldenToolCall  `json:"tool_calls,omitempty"`
}

type GoldenToolCall struct {
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type GoldenAssertions struct {
	ToolsCalled       []string `json:"tools_called,omitempty"`
	FinalTextContains []string `json:"final_text_contains,omitempty"`
	FilesCreated      []string `json:"files_created,omitempty"`
	FilesContain      map[string]string `json:"files_contain,omitempty"`
}
