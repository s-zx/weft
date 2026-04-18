// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

package cmdblock

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GitInfo summarizes the git state of a working directory so the frontend
// status bar can show pills like "main", "240 • +2697 -0".
type GitInfo struct {
	IsRepo       bool
	Branch       string
	ChangedFiles int
	Additions    int
	Deletions    int
	Ahead        int
	Behind       int
}

// LookupGitInfo shells out to git from cwd. Each command is wrapped in a
// short timeout because large monorepos can make `git diff --numstat` slow;
// we'd rather return partial info than block the RPC.
func LookupGitInfo(ctx context.Context, cwd string) (*GitInfo, error) {
	if cwd == "" {
		return &GitInfo{}, nil
	}
	out, err := runGit(ctx, cwd, 500*time.Millisecond, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		return &GitInfo{IsRepo: false}, nil
	}
	info := &GitInfo{IsRepo: true}

	if branch, err := runGit(ctx, cwd, 500*time.Millisecond, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(branch)
		if info.Branch == "HEAD" {
			if sha, err := runGit(ctx, cwd, 500*time.Millisecond, "rev-parse", "--short", "HEAD"); err == nil {
				info.Branch = strings.TrimSpace(sha)
			}
		}
	}

	if status, err := runGit(ctx, cwd, 750*time.Millisecond, "status", "--porcelain"); err == nil {
		changed := 0
		for _, line := range strings.Split(status, "\n") {
			if strings.TrimSpace(line) != "" {
				changed++
			}
		}
		info.ChangedFiles = changed
	}

	// Only ask for line counts if there's anything changed — the diff
	// against a clean tree is an empty walk but still takes a few ms we
	// can skip.
	if info.ChangedFiles > 0 {
		if numstat, err := runGit(ctx, cwd, 1500*time.Millisecond, "diff", "--numstat", "HEAD"); err == nil {
			adds, dels := sumNumstat(numstat)
			info.Additions = adds
			info.Deletions = dels
		}
	}

	if aheadBehind, err := runGit(ctx, cwd, 500*time.Millisecond, "rev-list", "--left-right", "--count", "@{u}...HEAD"); err == nil {
		parts := strings.Fields(strings.TrimSpace(aheadBehind))
		if len(parts) == 2 {
			if n, err := strconv.Atoi(parts[0]); err == nil {
				info.Behind = n
			}
			if n, err := strconv.Atoi(parts[1]); err == nil {
				info.Ahead = n
			}
		}
	}

	return info, nil
}

func runGit(ctx context.Context, cwd string, timeout time.Duration, args ...string) (string, error) {
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(subCtx, "git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func sumNumstat(text string) (adds, dels int) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// binary files show "-  -  path"; skip those.
		if a, err := strconv.Atoi(fields[0]); err == nil {
			adds += a
		}
		if d, err := strconv.Atoi(fields[1]); err == nil {
			dels += d
		}
	}
	return
}
