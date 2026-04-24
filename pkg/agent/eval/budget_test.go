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

func TestRunAIChat_StepBudgetStopsLoop(t *testing.T) {
	responses := make([]GoldenResponse, 100)
	for i := range responses {
		responses[i] = GoldenResponse{
			ToolCalls: []GoldenToolCall{{Name: "noop", Input: map[string]any{}}},
		}
	}
	backend := MakeMockBackend(responses)
	sseHandler := sse.MakeSSEHandlerCh(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).Context())
	chatOpts := uctypes.WaveChatOpts{
		ChatId:   "test-budget-" + uuid.New().String(),
		MaxSteps: 5,
		Config: uctypes.AIOptsType{
			APIType: "mock",
			Model:   "mock-model",
		},
	}

	metrics, err := aiusechat.RunAIChat(context.Background(), sseHandler, backend, chatOpts)
	if err != nil {
		t.Fatalf("RunAIChat failed: %v", err)
	}
	if metrics.RequestCount != 5 {
		t.Fatalf("expected 5 steps, got %d", metrics.RequestCount)
	}
	if !metrics.HadError {
		t.Fatal("expected HadError=true when budget exhausted")
	}
}

func TestRunAIChat_ZeroMaxStepsIsUnlimited(t *testing.T) {
	responses := []GoldenResponse{
		{ToolCalls: []GoldenToolCall{{Name: "noop", Input: map[string]any{}}}},
		{ToolCalls: []GoldenToolCall{{Name: "noop", Input: map[string]any{}}}},
		{Text: "done"},
	}
	backend := MakeMockBackend(responses)
	sseHandler := sse.MakeSSEHandlerCh(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).Context())
	chatOpts := uctypes.WaveChatOpts{
		ChatId:   "test-unlimited-" + uuid.New().String(),
		MaxSteps: 0,
		Config: uctypes.AIOptsType{
			APIType: "mock",
			Model:   "mock-model",
		},
	}

	metrics, err := aiusechat.RunAIChat(context.Background(), sseHandler, backend, chatOpts)
	if err != nil {
		t.Fatalf("RunAIChat failed: %v", err)
	}
	if metrics.RequestCount != 3 {
		t.Fatalf("expected 3 steps (unlimited), got %d", metrics.RequestCount)
	}
}

func TestRunAIChat_BudgetOfOneRunsOneStep(t *testing.T) {
	responses := make([]GoldenResponse, 50)
	for i := range responses {
		responses[i] = GoldenResponse{
			ToolCalls: []GoldenToolCall{{Name: "noop", Input: map[string]any{}}},
		}
	}
	backend := MakeMockBackend(responses)
	sseHandler := sse.MakeSSEHandlerCh(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).Context())
	chatOpts := uctypes.WaveChatOpts{
		ChatId:   "test-budget-one-" + uuid.New().String(),
		MaxSteps: 1,
		Config: uctypes.AIOptsType{
			APIType: "mock",
			Model:   "mock-model",
		},
	}

	metrics, err := aiusechat.RunAIChat(context.Background(), sseHandler, backend, chatOpts)
	if err != nil {
		t.Fatalf("RunAIChat failed: %v", err)
	}
	if metrics.RequestCount != 1 {
		t.Fatalf("expected 1 step, got %d", metrics.RequestCount)
	}
}
