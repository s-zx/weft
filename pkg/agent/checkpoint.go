// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"fmt"
	"log"
	"os"
	"sync"
)

type FileChange struct {
	Path       string `json:"path"`
	BackupPath string `json:"backuppath,omitempty"`
	IsNew      bool   `json:"isnew"`
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
	cs.checkpoints[chatId] = append(cps, Checkpoint{ID: checkpointId, Changes: []FileChange{change}})
}

func (cs *CheckpointStore) RewindTo(chatId string, checkpointId string) (int, error) {
	cs.lock.Lock()
	defer cs.lock.Unlock()

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

	restored := 0
	for i := len(cps) - 1; i > targetIdx; i-- {
		for _, change := range cps[i].Changes {
			if change.IsNew {
				if err := os.Remove(change.Path); err != nil && !os.IsNotExist(err) {
					log.Printf("checkpoint: failed to remove created file %s: %v\n", change.Path, err)
				} else {
					restored++
				}
			} else if change.BackupPath != "" {
				backup, err := os.ReadFile(change.BackupPath)
				if err != nil {
					log.Printf("checkpoint: failed to read backup %s: %v\n", change.BackupPath, err)
					continue
				}
				if err := os.WriteFile(change.Path, backup, 0644); err != nil {
					log.Printf("checkpoint: failed to restore %s: %v\n", change.Path, err)
					continue
				}
				restored++
			}
		}
	}

	cs.checkpoints[chatId] = cps[:targetIdx+1]
	return restored, nil
}

func (cs *CheckpointStore) RewindLast(chatId string) (int, error) {
	cs.lock.Lock()
	cps := cs.checkpoints[chatId]
	if len(cps) < 2 {
		cs.lock.Unlock()
		return 0, fmt.Errorf("no previous checkpoint to rewind to")
	}
	targetId := cps[len(cps)-2].ID
	cs.lock.Unlock()
	return cs.RewindTo(chatId, targetId)
}

func (cs *CheckpointStore) GetCheckpoints(chatId string) []Checkpoint {
	cs.lock.Lock()
	defer cs.lock.Unlock()
	result := make([]Checkpoint, len(cs.checkpoints[chatId]))
	copy(result, cs.checkpoints[chatId])
	return result
}
