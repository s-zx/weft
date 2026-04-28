// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

func getTestdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testdata")
}

func TestGoldenTranscripts(t *testing.T) {
	RunAllGoldenTests(t, getTestdataDir())
}

func TestLoadGoldenTranscript(t *testing.T) {
	path := filepath.Join(getTestdataDir(), "ask-read-file.golden.json")
	transcript, err := LoadGoldenTranscript(path)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}
	if transcript.Name != "ask-read-file" {
		t.Errorf("Name = %q", transcript.Name)
	}
	if transcript.Mode != "ask" {
		t.Errorf("Mode = %q", transcript.Mode)
	}
	if len(transcript.Turns) != 1 {
		t.Errorf("expected 1 turn, got %d", len(transcript.Turns))
	}
	if len(transcript.Turns[0].Responses) != 2 {
		t.Errorf("expected 2 responses, got %d", len(transcript.Turns[0].Responses))
	}
}

func TestMockBackendDequeue(t *testing.T) {
	responses := []GoldenResponse{
		{Text: "first"},
		{Text: "second"},
	}
	mock := MakeMockBackend(responses)
	if mock.responseIdx != 0 {
		t.Fatal("should start at 0")
	}

	mock.RunChatStep(nil, nil, uctypesZero(), nil)
	if mock.LastText != "first" {
		t.Errorf("LastText = %q", mock.LastText)
	}

	mock.RunChatStep(nil, nil, uctypesZero(), nil)
	if mock.LastText != "second" {
		t.Errorf("LastText = %q", mock.LastText)
	}
}

func uctypesZero() uctypes.WaveChatOpts {
	return uctypes.WaveChatOpts{}
}
