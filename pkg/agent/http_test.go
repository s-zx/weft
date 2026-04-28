// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postJSON(t *testing.T, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/post-agent-message", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	PostAgentMessageHandler(w, req)
	return w
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/post-agent-message", nil)
	w := httptest.NewRecorder()
	PostAgentMessageHandler(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandler_EmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/post-agent-message", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	PostAgentMessageHandler(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty chatid, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_InvalidChatID(t *testing.T) {
	w := postJSON(t, map[string]any{"chatid": "not-a-uuid"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad UUID, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_InvalidMode(t *testing.T) {
	w := postJSON(t, map[string]any{
		"chatid": "550e8400-e29b-41d4-a716-446655440000",
		"mode":   "invalid",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad mode, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_MalformedJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/post-agent-message", bytes.NewReader([]byte("{bad json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	PostAgentMessageHandler(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d", w.Code)
	}
}
