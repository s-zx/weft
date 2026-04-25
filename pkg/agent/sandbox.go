// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Worktree struct {
	Name       string
	Path       string
	BranchName string
	RepoRoot   string
}

func MakeWorktree(cwd string, name string) (*Worktree, error) {
	repoRoot, err := gitRepoRoot(cwd)
	if err != nil {
		return nil, fmt.Errorf("not a git repo: %w", err)
	}

	if name == "" {
		name = randomWorktreeName()
	}

	branchName := "worktree-" + name
	worktreeDir := filepath.Join(repoRoot, ".crest", "worktrees", name)

	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0755); err != nil {
		return nil, fmt.Errorf("mkdir failed: %w", err)
	}

	cmd := exec.Command("git", "worktree", "add", "-b", branchName, worktreeDir)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree add: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	log.Printf("worktree: created %s (branch %s)\n", worktreeDir, branchName)
	return &Worktree{
		Name:       name,
		Path:       worktreeDir,
		BranchName: branchName,
		RepoRoot:   repoRoot,
	}, nil
}

func (w *Worktree) HasChanges() bool {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = w.Path
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}

func (w *Worktree) Remove() error {
	cmd := exec.Command("git", "worktree", "remove", "--force", w.Path)
	cmd.Dir = w.RepoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("worktree remove: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	cmd = exec.Command("git", "branch", "-D", w.BranchName)
	cmd.Dir = w.RepoRoot
	cmd.CombinedOutput()
	log.Printf("worktree: removed %s\n", w.Path)
	return nil
}

func gitRepoRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var worktreeAdjectives = []string{"bright", "calm", "cool", "crisp", "dark", "fast", "keen", "quick", "warm", "wise"}
var worktreeNouns = []string{"brook", "cliff", "crane", "dawn", "dusk", "frost", "leaf", "pine", "reef", "wind"}

func randomWorktreeName() string {
	a := worktreeAdjectives[rand.Intn(len(worktreeAdjectives))]
	n := worktreeNouns[rand.Intn(len(worktreeNouns))]
	return a + "-" + n
}
