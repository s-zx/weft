// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

package cmdblock

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const DefaultHistoryLimit = 2000

// LoadShellHistory reads the user's shell history file for the given shell
// type and returns the last `limit` deduped lines in chronological order
// (oldest first so ArrowUp walks backward). An empty shell string or an
// unreadable history file yields an empty slice — the caller gracefully
// falls back to in-session history.
func LoadShellHistory(shell string, limit int) []string {
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	path := resolveHistoryPath(shell)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []string
	seen := make(map[string]int)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line == "" {
			continue
		}
		// zsh extended-history prefix: ": <ts>:<duration>;<cmd>"
		if strings.HasPrefix(line, ":") {
			if idx := strings.Index(line, ";"); idx >= 0 {
				line = line[idx+1:]
			}
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if prev, ok := seen[line]; ok {
			out[prev] = ""
		}
		seen[line] = len(out)
		out = append(out, line)
	}
	// strip empties left by dedup
	compact := out[:0]
	for _, s := range out {
		if s != "" {
			compact = append(compact, s)
		}
	}
	if len(compact) > limit {
		compact = compact[len(compact)-limit:]
	}
	return compact
}

func resolveHistoryPath(shell string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if env := os.Getenv("HISTFILE"); env != "" {
		return env
	}
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zsh_history")
	case "bash":
		return filepath.Join(home, ".bash_history")
	case "fish":
		// fish uses its own db format — skip for now, caller falls back to session history
		return ""
	case "":
		// best-effort: if .zsh_history exists, use it; otherwise try .bash_history.
		for _, name := range []string{".zsh_history", ".bash_history"} {
			p := filepath.Join(home, name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
