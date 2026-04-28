// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/web/sse"
)

func TestRunAIChat_AuditLogPopulated(t *testing.T) {
	responses := []GoldenResponse{
		{ToolCalls: []GoldenToolCall{{Name: "tool_a", Input: map[string]any{"key": "val"}}}},
		{ToolCalls: []GoldenToolCall{{Name: "tool_b", Input: map[string]any{}}}},
		{Text: "done"},
	}
	backend := MakeMockBackend(responses)
	sseHandler := sse.MakeSSEHandlerCh(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).Context())
	chatOpts := uctypes.WaveChatOpts{
		ChatId: "test-audit-" + uuid.New().String(),
		Config: uctypes.AIOptsType{
			APIType: "mock",
			Model:   "mock-model",
		},
	}

	metrics, err := aiusechat.RunAIChat(context.Background(), sseHandler, backend, chatOpts)
	if err != nil {
		t.Fatalf("RunAIChat failed: %v", err)
	}
	if len(metrics.AuditLog) != 2 {
		t.Fatalf("expected 2 audit events, got %d", len(metrics.AuditLog))
	}
	if metrics.AuditLog[0].ToolName != "tool_a" {
		t.Fatalf("first event tool = %q, want tool_a", metrics.AuditLog[0].ToolName)
	}
	if metrics.AuditLog[1].ToolName != "tool_b" {
		t.Fatalf("second event tool = %q, want tool_b", metrics.AuditLog[1].ToolName)
	}
	for i, ev := range metrics.AuditLog {
		if ev.Timestamp <= 0 {
			t.Fatalf("event %d: timestamp should be positive", i)
		}
		if ev.ChatId != chatOpts.ChatId {
			t.Fatalf("event %d: chatId = %q, want %q", i, ev.ChatId, chatOpts.ChatId)
		}
		if ev.Outcome != "error" {
			t.Fatalf("event %d: outcome = %q, want error (tool not found)", i, ev.Outcome)
		}
	}
}
