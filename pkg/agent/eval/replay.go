// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/s-zx/crest/pkg/agent"
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/web/sse"
)

func LoadGoldenTranscript(path string) (*GoldenTranscript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read golden transcript: %w", err)
	}
	var transcript GoldenTranscript
	if err := json.Unmarshal(data, &transcript); err != nil {
		return nil, fmt.Errorf("failed to parse golden transcript: %w", err)
	}
	return &transcript, nil
}

func setupWorkspace(t *testing.T, setup GoldenSetup) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range setup.Files {
		fullPath := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create directory for %s: %v", name, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", name, err)
		}
	}
	return dir
}

func collectAllResponses(turns []GoldenTurn, cwd string) []GoldenResponse {
	var all []GoldenResponse
	for _, turn := range turns {
		for _, resp := range turn.Responses {
			expanded := expandResponse(resp, cwd)
			all = append(all, expanded)
		}
	}
	return all
}

func expandResponse(resp GoldenResponse, cwd string) GoldenResponse {
	out := GoldenResponse{Text: resp.Text}
	for _, tc := range resp.ToolCalls {
		expanded := GoldenToolCall{Name: tc.Name, Input: make(map[string]any)}
		for k, v := range tc.Input {
			if s, ok := v.(string); ok {
				expanded.Input[k] = strings.ReplaceAll(s, "{{CWD}}", cwd)
			} else {
				expanded.Input[k] = v
			}
		}
		out.ToolCalls = append(out.ToolCalls, expanded)
	}
	return out
}

func RunGoldenTest(t *testing.T, transcript *GoldenTranscript) {
	t.Helper()

	cwd := setupWorkspace(t, transcript.Setup)

	if !agent.ValidMode(transcript.Mode) {
		t.Fatalf("unknown mode: %s", transcript.Mode)
	}
	modeName := agent.NormalizeMode(transcript.Mode)

	allResponses := collectAllResponses(transcript.Turns, cwd)
	mockBackend := MakeMockBackend(allResponses)

	sess := &agent.Session{
		ChatID:  uuid.New().String(),
		TabID:   "eval-tab",
		BlockID: "eval-block",
		Mode:    modeName,
		Cwd:     cwd,
	}

	tools := agent.ToolsForSession(sess)
	for i := range tools {
		tools[i].ToolApproval = func(any) string { return uctypes.ApprovalAutoApproved }
	}
	systemPrompt := agent.SystemPromptByKey(modeName)

	chatOpts := uctypes.WaveChatOpts{
		ChatId:       "eval:" + sess.ChatID,
		ClientId:     "eval-client",
		Config:       uctypes.AIOptsType{APIType: "mock", Model: "mock"},
		Tools:        tools,
		SystemPrompt: systemPrompt,
	}

	for _, turn := range transcript.Turns {
		userMsg := &uctypes.AIMessage{
			MessageId: uuid.New().String(),
			Parts: []uctypes.AIMessagePart{
				{Type: "text", Text: turn.User},
			},
		}

		nativeMsg, err := mockBackend.ConvertAIMessageToNativeChatMessage(*userMsg)
		if err != nil {
			t.Fatalf("failed to convert user message: %v", err)
		}
		_ = nativeMsg

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/eval", nil)
		sseHandler := sse.MakeSSEHandlerCh(w, r.Context())

		ctx := context.Background()
		_, err = aiusechat.RunAIChat(ctx, sseHandler, mockBackend, chatOpts)
		sseHandler.Close()
		if err != nil {
			t.Fatalf("RunAIChat failed: %v", err)
		}
	}

	checkAssertions(t, transcript.Assertions, mockBackend, cwd)
}

func checkAssertions(t *testing.T, assertions GoldenAssertions, mock *MockBackend, cwd string) {
	t.Helper()

	if len(assertions.ToolsCalled) > 0 {
		for _, expected := range assertions.ToolsCalled {
			found := false
			for _, called := range mock.ToolsCalled {
				if called == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected tool %q to be called, but it wasn't. Called: %v", expected, mock.ToolsCalled)
			}
		}
	}

	if len(assertions.FinalTextContains) > 0 {
		for _, substr := range assertions.FinalTextContains {
			if !strings.Contains(mock.LastText, substr) {
				t.Errorf("expected final text to contain %q, got %q", substr, mock.LastText)
			}
		}
	}

	for _, path := range assertions.FilesCreated {
		fullPath := filepath.Join(cwd, path)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			t.Errorf("expected file %q to be created, but it doesn't exist", path)
		}
	}

	for path, expectedContent := range assertions.FilesContain {
		fullPath := filepath.Join(cwd, path)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Errorf("expected file %q to exist and contain %q, but: %v", path, expectedContent, err)
			continue
		}
		if !strings.Contains(string(data), expectedContent) {
			t.Errorf("file %q should contain %q, got %q", path, expectedContent, string(data))
		}
	}
}

func RunAllGoldenTests(t *testing.T, testdataDir string) {
	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Fatalf("failed to read testdata dir: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".golden.json") {
			continue
		}
		t.Run(strings.TrimSuffix(e.Name(), ".golden.json"), func(t *testing.T) {
			transcript, err := LoadGoldenTranscript(filepath.Join(testdataDir, e.Name()))
			if err != nil {
				t.Fatalf("failed to load transcript: %v", err)
			}
			RunGoldenTest(t, transcript)
		})
	}
}
