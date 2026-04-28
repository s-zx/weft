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
	"regexp"
	"strings"
)

const worktreeMaxNameLen = 64

// worktreeNameRE accepts only safe characters: ASCII letters, digits,
// '-' and '_'. This blocks path traversal (`..`, `/`, `\`), git ref
// metacharacters (`~^:?*[`), shell/CLI metacharacters, and any leading `-`
// that could be misinterpreted as a flag by `git worktree add`.
var worktreeNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type Worktree struct {
	Name       string
	Path       string
	BranchName string
	RepoRoot   string
}

func validateWorktreeName(name string) error {
	if name == "" {
		return fmt.Errorf("worktree name is empty")
	}
	if len(name) > worktreeMaxNameLen {
		return fmt.Errorf("worktree name too long (max %d)", worktreeMaxNameLen)
	}
	if !worktreeNameRE.MatchString(name) {
		return fmt.Errorf("worktree name must match [A-Za-z0-9][A-Za-z0-9_-]*")
	}
	return nil
}

func MakeWorktree(cwd string, name string) (*Worktree, error) {
	repoRoot, err := gitRepoRoot(cwd)
	if err != nil {
		return nil, fmt.Errorf("not a git repo: %w", err)
	}

	if name == "" {
		name = randomWorktreeName()
	}
	if err := validateWorktreeName(name); err != nil {
		return nil, err
	}

	branchName := "worktree-" + name
	worktreesRoot := filepath.Join(repoRoot, ".crest", "worktrees")
	worktreeDir := filepath.Join(worktreesRoot, name)

	// Defense in depth: even after validation, ensure filepath.Join didn't
	// resolve outside .crest/worktrees (e.g. via symlinks in repoRoot).
	absRoot, err := filepath.Abs(worktreesRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve worktrees root: %w", err)
	}
	absDir, err := filepath.Abs(worktreeDir)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree dir: %w", err)
	}
	if !strings.HasPrefix(absDir+string(filepath.Separator), absRoot+string(filepath.Separator)) {
		return nil, fmt.Errorf("worktree path escapes .crest/worktrees")
	}

	if _, err := os.Stat(worktreeDir); err == nil {
		return nil, fmt.Errorf("worktree %q already exists", name)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat worktree dir: %w", err)
	}

	if err := os.MkdirAll(worktreesRoot, 0755); err != nil {
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

// HasUnpushedCommits reports whether the worktree branch has commits that are
// reachable only from this branch (i.e. not on any other local branch or
// remote-tracking ref). `git status --porcelain` only catches dirty-tree state,
// so a user who committed but never pushed would otherwise lose work when
// `:worktree exit` runs `git branch -D`. Returns true on any error, since the
// safe default is to ask the user to force.
func (w *Worktree) HasUnpushedCommits() bool {
	if w.BranchName == "" {
		return false
	}
	args := []string{
		"rev-list", "--count", w.BranchName,
		"--not",
		"--exclude=refs/heads/" + w.BranchName,
		"--branches", "--remotes",
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = w.Path
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	count := strings.TrimSpace(string(out))
	return count != "" && count != "0"
}

// validateRemovePath ensures path points inside the repo's
// .crest/worktrees/ directory and rejects symlink/traversal escapes. The
// HTTP handler accepts a caller-supplied path, so a missing check here would
// let any client make `git worktree remove --force` (and `branch -D`) wipe
// arbitrary directories on the host.
func (w *Worktree) validateRemovePath() error {
	if w.RepoRoot == "" {
		return fmt.Errorf("repo root is required for remove")
	}
	if w.Path == "" {
		return fmt.Errorf("worktree path is required")
	}
	repoRoot, err := gitRepoRoot(w.RepoRoot)
	if err != nil {
		return fmt.Errorf("resolve repo root: %w", err)
	}
	absRoot, err := filepath.Abs(filepath.Join(repoRoot, ".crest", "worktrees"))
	if err != nil {
		return fmt.Errorf("resolve worktrees root: %w", err)
	}
	absPath, err := filepath.Abs(w.Path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	if !strings.HasPrefix(absPath+string(filepath.Separator), absRoot+string(filepath.Separator)) {
		return fmt.Errorf("path is not under .crest/worktrees")
	}
	return nil
}

func (w *Worktree) Remove() error {
	if err := w.validateRemovePath(); err != nil {
		return err
	}
	cmd := exec.Command("git", "worktree", "remove", "--force", w.Path)
	cmd.Dir = w.RepoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("worktree remove: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if w.BranchName != "" {
		cmd = exec.Command("git", "branch", "-D", "--", w.BranchName)
		cmd.Dir = w.RepoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("worktree: branch -D %s failed: %v\n%s\n", w.BranchName, err, strings.TrimSpace(string(out)))
		}
	}
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
	suffix := rand.Intn(10000)
	return fmt.Sprintf("%s-%s-%04d", a, n, suffix)
}
