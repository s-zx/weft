// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

// fileReadTracker remembers the last mtime+size we saw for each (chatId, path)
// the agent has read. The write/edit tools consult it before clobbering a file:
// if the file changed externally between the agent's read and its write, we
// refuse the write and tell the model to re-read.
//
// Keyed by chatId so different concurrent chats can edit the same file without
// poisoning each other's tracking.
type fileReadTracker struct {
	mu      sync.Mutex
	entries map[string]map[string]fileSig // chatId -> path -> sig
}

type fileSig struct {
	modTime time.Time
	size    int64
}

var globalFileTracker = &fileReadTracker{entries: make(map[string]map[string]fileSig)}

func (t *fileReadTracker) record(chatId, absPath string) {
	if chatId == "" || absPath == "" {
		return
	}
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.entries[chatId]
	if m == nil {
		m = make(map[string]fileSig)
		t.entries[chatId] = m
	}
	m[absPath] = fileSig{modTime: info.ModTime(), size: info.Size()}
}

// checkUnchanged returns nil if the file's current sig matches what we
// recorded last time, or an error message describing the divergence.
// Returns nil for files we have never read — those are either new files
// (write_text_file) or the model is editing blind, in which case we trust
// it (Claude Code does the same; the safety net only covers the read→edit
// flow, which is the common case where stale state actually bites).
func (t *fileReadTracker) checkUnchanged(chatId, absPath string) error {
	if chatId == "" || absPath == "" {
		return nil
	}
	t.mu.Lock()
	prev, ok := t.entries[chatId][absPath]
	t.mu.Unlock()
	if !ok {
		return nil
	}
	info, err := os.Stat(absPath)
	if err != nil {
		// File disappeared since the read — the edit will fail anyway,
		// but a clearer message helps the model diagnose.
		return fmt.Errorf("file %s no longer exists (was read at %s); re-read or write fresh", absPath, prev.modTime.Format(time.RFC3339))
	}
	if info.ModTime().Equal(prev.modTime) && info.Size() == prev.size {
		return nil
	}
	return fmt.Errorf("file %s was modified externally since you last read it (was %s/%d bytes, now %s/%d bytes); re-read with read_text_file before editing to avoid clobbering changes",
		absPath,
		prev.modTime.Format(time.RFC3339), prev.size,
		info.ModTime().Format(time.RFC3339), info.Size())
}

func recordFileRead(chatId, absPath string) {
	globalFileTracker.record(chatId, absPath)
}

func checkFileUnchanged(chatId, absPath string) error {
	return globalFileTracker.checkUnchanged(chatId, absPath)
}

// MtimeCheckHook returns a BeforeToolHook that refuses a write/edit when the
// target file has been modified externally since the agent's last read.
// Skips the check when the file does not yet exist (new-file write) and
// when the input does not carry a "filename" field.
func MtimeCheckHook(chatId string) uctypes.BeforeToolHook {
	return func(_ context.Context, h uctypes.HookContext) *uctypes.AIToolResult {
		p := extractFilenameFromInput(h.ToolCall.Input)
		if p == "" {
			return nil
		}
		if _, err := os.Stat(p); err != nil {
			// New-file write — no prior read needed. The underlying tool
			// handles its own "directory exists" / "is allowed" validation.
			return nil
		}
		if err := checkFileUnchanged(chatId, p); err != nil {
			return &uctypes.AIToolResult{
				ToolName:  h.ToolCall.Name,
				ToolUseID: h.ToolCall.ID,
				ErrorText: err.Error(),
				ErrorType: uctypes.ErrorTypeStaleFile,
			}
		}
		return nil
	}
}

// MtimeRecordHook returns an AfterToolHook that refreshes the recorded
// mtime+size for the file the tool just operated on. Runs only on success
// (skipped if the result already carries an error). Used by read (record
// after successful read) and by write/edit (record after successful write
// so the next edit sees the agent's own write as the latest known state).
func MtimeRecordHook(chatId string) uctypes.AfterToolHook {
	return func(_ context.Context, h uctypes.HookContext, result *uctypes.AIToolResult) {
		if result == nil || result.ErrorText != "" {
			return
		}
		if p := extractFilenameFromInput(h.ToolCall.Input); p != "" {
			recordFileRead(chatId, p)
		}
	}
}
