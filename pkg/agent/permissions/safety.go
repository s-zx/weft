// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import (
	"path/filepath"
	"strings"
)

// SafetyResult is what CheckSafety returns. Triggered=true means the
// engine MUST prompt the user regardless of posture (except `bench`,
// which skips the safety layer entirely). Reason is shown in the prompt
// so the user understands why a looser posture didn't auto-approve.
type SafetyResult struct {
	Triggered bool
	Reason    string
}

// safetyResultPass is the "no safety concern" result, factored out so
// the call sites read consistently. (Returning it doesn't share state
// — Go copies the struct value on return.)
var safetyResultPass = SafetyResult{Triggered: false}

// CheckSafety inspects a tool call and returns a non-passing result
// when the call matches any bypass-immune pattern (see §6 of the design
// doc). The engine calls this between the rules layer and the posture
// fallback so safety wins over `acceptEdits` and `bypass` postures.
//
// Tool-name normalization: callers should pass the canonical name
// (e.g. "edit_text_file", not "agent:edit_text_file"). The engine
// already operates on canonical names by the time it reaches here.
func CheckSafety(toolName string, input map[string]any) SafetyResult {
	switch toolName {
	case "shell_exec":
		return checkShellSafety(input)
	case "edit_text_file", "write_text_file", "multi_edit":
		return checkFileSafety(input)
	default:
		return safetyResultPass
	}
}

// dangerousShellPatterns lists the bypass-immune shell commands. Each
// entry is matched as a substring against the lowercased command (for
// destructive patterns where any occurrence is unsafe) or as a prefix
// (for whole-command-style entries like `sudo`).
//
// Substring matches are intentionally fuzzy — `rm -rf /` should fire
// against `cd /tmp && rm -rf /var/something/important` too. The cost
// of a false positive (one extra prompt) is far below the cost of a
// false negative (silent destructive action).
var dangerousShellSubstrings = []struct {
	pat    string
	reason string
}{
	{"rm -rf /", "destructive: `rm -rf /` deletes from root"},
	{"rm -rf ~", "destructive: `rm -rf ~` wipes the user's home"},
	{"rm -rf $home", "destructive: `rm -rf $HOME` wipes the user's home"},
	{":(){:|:&};:", "fork bomb"},
	{":(){ :|:& };:", "fork bomb"},
	{"git push --force", "destructive: force-push can rewrite shared history"},
	{"git push -f ", "destructive: force-push can rewrite shared history"},
	{"git push --force-with-lease ", "force-push (lease-protected) — confirm"},
}

// pipeIntoShell catches the "curl/wget … | sh" idiom regardless of
// the URL or flags between the fetcher and the pipe. False-positives
// here cost an extra prompt (e.g. `curl --version | tee log`); false
// negatives let an attacker-controlled script run unprompted, so we
// err loose. Two contains() calls beat the alternative regex for
// readability.
func detectPipeIntoShell(low string) bool {
	hasFetcher := strings.Contains(low, "curl ") || strings.Contains(low, "wget ")
	if !hasFetcher {
		return false
	}
	return strings.Contains(low, "| sh") ||
		strings.Contains(low, "|sh") ||
		strings.Contains(low, "| bash") ||
		strings.Contains(low, "|bash")
}

var dangerousShellPrefixes = []struct {
	pat    string
	reason string
}{
	{"sudo ", "privilege escalation: command runs with root"},
	{"sudo\t", "privilege escalation: command runs with root"},
}

// checkShellSafety extracts the command from input["command"] and runs
// it through the patterns above. Missing/empty command returns pass —
// the tool's own validation will reject a malformed call.
func checkShellSafety(input map[string]any) SafetyResult {
	cmd, _ := input["command"].(string)
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return safetyResultPass
	}
	low := strings.ToLower(cmd)

	for _, e := range dangerousShellSubstrings {
		if strings.Contains(low, e.pat) {
			return SafetyResult{Triggered: true, Reason: e.reason}
		}
	}
	for _, e := range dangerousShellPrefixes {
		if strings.HasPrefix(low, e.pat) || low == strings.TrimSpace(e.pat) {
			return SafetyResult{Triggered: true, Reason: e.reason}
		}
	}
	if detectPipeIntoShell(low) {
		return SafetyResult{Triggered: true, Reason: "untrusted exec: piping curl/wget into a shell"}
	}
	return safetyResultPass
}

// dangerousFilePathRules lists bypass-immune file-edit targets. Each
// rule is one of three shapes:
//
//   - dirSegment: any path containing /<segment>/ — used for `.git`,
//     `.ssh`, etc. Catches both /repo/.git/HEAD and /home/me/.ssh/key
//     without false-positiving on a literal file named ".git".
//   - basenameExact: the basename equals the pattern — used for shell
//     dotfiles (`.bashrc`, `.zshrc`).
//   - basenameContains: the basename contains the pattern — used for
//     `.env` (catches .env, .env.local, .env.production) and
//     credential-ish names (catches credentials.json, my-credentials,
//     secret-key.txt).
type filePathRule struct {
	kind    string
	pattern string
	reason  string
}

const (
	filePathDirSegment      = "dirSegment"
	filePathBasenameExact   = "basenameExact"
	filePathBasenameContain = "basenameContain"
)

var dangerousFilePathRules = []filePathRule{
	{filePathDirSegment, ".git", "version-control internals (.git/)"},
	{filePathDirSegment, ".crest", "agent-internal storage (.crest/)"},
	{filePathDirSegment, ".ssh", "SSH config and keys"},
	{filePathDirSegment, ".aws", "AWS credentials"},
	{filePathDirSegment, ".gnupg", "GPG keyring"},
	{filePathBasenameExact, ".bashrc", "shell config"},
	{filePathBasenameExact, ".zshrc", "shell config"},
	{filePathBasenameExact, ".profile", "shell config"},
	{filePathBasenameExact, ".bash_profile", "shell config"},
	{filePathBasenameContain, ".env", "environment / secrets file"},
	{filePathBasenameContain, "credentials", "filename suggests credentials"},
	{filePathBasenameContain, "secret", "filename suggests secrets"},
}

// checkFileSafety pulls the target path out of input and matches it
// against dangerousFilePathRules. Both `filename` (used by the file
// tools today) and `path` (defensive — some MCP tools use this name)
// are checked.
func checkFileSafety(input map[string]any) SafetyResult {
	path := extractFilePath(input)
	if path == "" {
		return safetyResultPass
	}
	low := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	for _, rule := range dangerousFilePathRules {
		switch rule.kind {
		case filePathDirSegment:
			seg := "/" + rule.pattern + "/"
			segEnd := "/" + rule.pattern // catches paths ending in the segment
			if strings.Contains(low, seg) || strings.HasSuffix(low, segEnd) {
				return SafetyResult{Triggered: true, Reason: rule.reason}
			}
		case filePathBasenameExact:
			if base == rule.pattern {
				return SafetyResult{Triggered: true, Reason: rule.reason}
			}
		case filePathBasenameContain:
			if strings.Contains(base, rule.pattern) {
				return SafetyResult{Triggered: true, Reason: rule.reason}
			}
		}
	}
	return safetyResultPass
}

func extractFilePath(input map[string]any) string {
	if input == nil {
		return ""
	}
	if v, ok := input["filename"].(string); ok && v != "" {
		return v
	}
	if v, ok := input["path"].(string); ok && v != "" {
		return v
	}
	return ""
}
