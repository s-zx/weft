// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/s-zx/crest/pkg/filebackup"
)

const MaxCheckpointsPerChat = 100

type FileChange struct {
	Path        string `json:"path"`
	BackupPath  string `json:"backuppath,omitempty"`
	IsNew       bool   `json:"isnew"`
	ContentHash string `json:"contenthash,omitempty"`
}

// CurrentFileHash returns the SHA-256 hex digest of the file at path. Returns
// "" if the file is missing or unreadable; rewind treats "" as "expected
// missing" so a deleted file with stored ""-hash still rewinds cleanly.
func CurrentFileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type Checkpoint struct {
	ID      string       `json:"id"`
	Changes []FileChange `json:"changes"`
}

type CheckpointStore struct {
	lock        sync.Mutex
	checkpoints map[string][]Checkpoint
}

var DefaultCheckpointStore = &CheckpointStore{
	checkpoints: make(map[string][]Checkpoint),
}

func (cs *CheckpointStore) AddCheckpoint(chatId string, cp Checkpoint) {
	cs.lock.Lock()
	defer cs.lock.Unlock()
	cs.checkpoints[chatId] = append(cs.checkpoints[chatId], cp)
}

func (cs *CheckpointStore) RecordFileChange(chatId string, checkpointId string, change FileChange) {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	cps := cs.checkpoints[chatId]
	for i := len(cps) - 1; i >= 0; i-- {
		if cps[i].ID == checkpointId {
			cps[i].Changes = append(cps[i].Changes, change)
			return
		}
	}
	cps = append(cps, Checkpoint{ID: checkpointId, Changes: []FileChange{change}})
	if len(cps) > MaxCheckpointsPerChat {
		cps = cps[len(cps)-MaxCheckpointsPerChat:]
	}
	cs.checkpoints[chatId] = cps
}

func (cs *CheckpointStore) RewindTo(chatId string, checkpointId string) (int, error) {
	cs.lock.Lock()
	defer cs.lock.Unlock()
	return cs.rewindToLocked(chatId, checkpointId)
}

func (cs *CheckpointStore) RewindLast(chatId string) (int, error) {
	cs.lock.Lock()
	defer cs.lock.Unlock()
	cps := cs.checkpoints[chatId]
	if len(cps) < 2 {
		return 0, fmt.Errorf("no previous checkpoint to rewind to")
	}
	return cs.rewindToLocked(chatId, cps[len(cps)-2].ID)
}

// rewindToLocked must be called with cs.lock held. For each path touched between
// the target checkpoint and the latest one we keep two facts:
//   - the *first-recorded* FileChange (its BackupPath captures the pre-turn
//     original; later writes only have intermediate-state backups)
//   - the *latest-recorded* ContentHash (the on-disk content the agent wrote
//     last; that's what we expect to find when we go to restore)
//
// Mixing them caused a silent-skip bug: dedup kept the first change's
// ContentHash, but the file on disk reflected the second write — guard then
// fired "modified externally" for legitimate multi-write turns.
func (cs *CheckpointStore) rewindToLocked(chatId string, checkpointId string) (int, error) {
	cps := cs.checkpoints[chatId]
	if len(cps) == 0 {
		return 0, fmt.Errorf("no checkpoints for chat %s", chatId)
	}

	targetIdx := -1
	for i, cp := range cps {
		if cp.ID == checkpointId {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return 0, fmt.Errorf("checkpoint %s not found", checkpointId)
	}

	type pathState struct {
		first      FileChange
		latestHash string
	}
	states := make(map[string]*pathState)
	var order []string
	for i := targetIdx + 1; i < len(cps); i++ {
		for _, change := range cps[i].Changes {
			s, ok := states[change.Path]
			if !ok {
				states[change.Path] = &pathState{first: change, latestHash: change.ContentHash}
				order = append(order, change.Path)
				continue
			}
			s.latestHash = change.ContentHash
		}
	}

	restored := 0
	for _, path := range order {
		s := states[path]
		change := s.first
		if s.latestHash != "" && CurrentFileHash(path) != s.latestHash {
			log.Printf("checkpoint: skipping %s (modified externally since agent change)\n", path)
			continue
		}
		if change.IsNew {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Printf("checkpoint: failed to remove created file %s: %v\n", path, err)
				continue
			}
			restored++
			continue
		}
		if change.BackupPath == "" {
			continue
		}
		if err := filebackup.RestoreBackup(change.BackupPath, path); err != nil {
			log.Printf("checkpoint: failed to restore %s: %v\n", path, err)
			continue
		}
		restored++
	}

	cs.checkpoints[chatId] = cps[:targetIdx+1]
	return restored, nil
}

func (cs *CheckpointStore) GetCheckpoints(chatId string) []Checkpoint {
	cs.lock.Lock()
	defer cs.lock.Unlock()
	result := make([]Checkpoint, len(cs.checkpoints[chatId]))
	copy(result, cs.checkpoints[chatId])
	return result
}
