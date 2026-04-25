// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/s-zx/crest/pkg/filebackup"
)

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestRewind_MultiWritePerTurn_RestoresOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foo.txt")

	v0 := []byte("original\n")
	v1 := []byte("after first write\n")
	v2 := []byte("after second write\n")

	if err := os.WriteFile(path, v0, 0644); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	backup1, err := filebackup.MakeFileBackup(path)
	if err != nil {
		t.Fatalf("backup1: %v", err)
	}
	if err := os.WriteFile(path, v1, 0644); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	backup2, err := filebackup.MakeFileBackup(path)
	if err != nil {
		t.Fatalf("backup2: %v", err)
	}
	if err := os.WriteFile(path, v2, 0644); err != nil {
		t.Fatalf("write v2: %v", err)
	}

	cs := &CheckpointStore{checkpoints: make(map[string][]Checkpoint)}
	chatId := "test-chat"

	cs.AddCheckpoint(chatId, Checkpoint{ID: "cp0"})

	cs.RecordFileChange(chatId, "cp1", FileChange{
		Path: path, BackupPath: backup1, IsNew: false, ContentHash: hashOf(v1),
	})
	cs.RecordFileChange(chatId, "cp1", FileChange{
		Path: path, BackupPath: backup2, IsNew: false, ContentHash: hashOf(v2),
	})

	restored, err := cs.RewindLast(chatId)
	if err != nil {
		t.Fatalf("RewindLast: %v", err)
	}
	if restored != 1 {
		t.Fatalf("expected 1 file restored, got %d", restored)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after rewind: %v", err)
	}
	if string(got) != string(v0) {
		t.Fatalf("rewind dropped to wrong state — want %q, got %q", v0, got)
	}
}

func TestRewind_ExternalEditSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foo.txt")

	v0 := []byte("original\n")
	v1 := []byte("agent wrote this\n")
	tampered := []byte("user changed this externally\n")

	if err := os.WriteFile(path, v0, 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	backup1, err := filebackup.MakeFileBackup(path)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := os.WriteFile(path, v1, 0644); err != nil {
		t.Fatalf("agent write: %v", err)
	}

	cs := &CheckpointStore{checkpoints: make(map[string][]Checkpoint)}
	chatId := "test-chat"
	cs.AddCheckpoint(chatId, Checkpoint{ID: "cp0"})
	cs.RecordFileChange(chatId, "cp1", FileChange{
		Path: path, BackupPath: backup1, IsNew: false, ContentHash: hashOf(v1),
	})

	if err := os.WriteFile(path, tampered, 0644); err != nil {
		t.Fatalf("user edit: %v", err)
	}

	restored, err := cs.RewindLast(chatId)
	if err != nil {
		t.Fatalf("RewindLast: %v", err)
	}
	if restored != 0 {
		t.Fatalf("expected 0 restored (external edit), got %d", restored)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(tampered) {
		t.Fatalf("user edits clobbered: got %q", got)
	}
}

func TestRewind_NewFileRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	v1 := []byte("created by agent\n")
	if err := os.WriteFile(path, v1, 0644); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	cs := &CheckpointStore{checkpoints: make(map[string][]Checkpoint)}
	chatId := "test-chat"
	cs.AddCheckpoint(chatId, Checkpoint{ID: "cp0"})
	cs.RecordFileChange(chatId, "cp1", FileChange{
		Path: path, BackupPath: "", IsNew: true, ContentHash: hashOf(v1),
	})

	restored, err := cs.RewindLast(chatId)
	if err != nil {
		t.Fatalf("RewindLast: %v", err)
	}
	if restored != 1 {
		t.Fatalf("expected 1 removed, got %d", restored)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("new file should be gone, stat err = %v", err)
	}
}

func TestRecordFileChange_CapEnforced(t *testing.T) {
	cs := &CheckpointStore{checkpoints: make(map[string][]Checkpoint)}
	chatId := "test-chat"
	for i := 0; i < MaxCheckpointsPerChat+50; i++ {
		cs.RecordFileChange(chatId, string(rune('a'+i)), FileChange{Path: "/x"})
	}
	got := cs.GetCheckpoints(chatId)
	if len(got) != MaxCheckpointsPerChat {
		t.Fatalf("cap not enforced: got %d, want %d", len(got), MaxCheckpointsPerChat)
	}
}
