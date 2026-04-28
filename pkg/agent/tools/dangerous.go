// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"regexp"
	"strings"
)

type dangerousPattern struct {
	re     *regexp.Regexp
	reason string
}

// `>\s*/dev/[a-z]` would match the extremely common `> /dev/null` (and
// `/dev/stdout`, `/dev/stderr`, `/dev/tty`, `/dev/zero`, `/dev/urandom`,
// `/dev/random`, `/dev/fd/*`). Flagging those as dangerous floods the user
// with approval prompts for innocent commands and trains them to click
// through, weakening the signal for actually-dangerous writes. We allow-list
// the well-known safe pseudo-devices and only fire on writes to anything
// else under /dev/.
var safeDevTargets = regexp.MustCompile(`^(null|stdout|stderr|tty|zero|u?random|fd/\d+)\b`)

var dangerousPatterns = []dangerousPattern{
	{regexp.MustCompile(`\brm\s.*-[a-z]*r[a-z]*f|\brm\s.*-[a-z]*f[a-z]*r`), "recursive force delete (rm -rf)"},
	{regexp.MustCompile(`\bgit\s+push\s.*(-f\b|--force\b|--force-with-lease\b)`), "force push"},
	{regexp.MustCompile(`\bgit\s+reset\s+--hard\b`), "hard reset (discards uncommitted changes)"},
	{regexp.MustCompile(`\bgit\s+clean\s.*(-[a-z]*f|--force\b)`), "git clean -f (removes untracked files)"},
	{regexp.MustCompile(`\bgit\s+checkout\s+(--\s+)?\.\s*$`), "git checkout . (discards all changes)"},
	{regexp.MustCompile(`\|\s*(sh|bash|zsh|dash|ksh)\b`), "pipe to shell"},
	{regexp.MustCompile(`\b(sh|bash|zsh|dash|ksh|source|eval)\s+<\(`), "process-substitution into shell/eval"},
	{regexp.MustCompile(`(^|[\s;&|])\.\s+<\(`), "process-substitution into . (source)"},
	{regexp.MustCompile(`\beval\s+["'$]?\s*(\$\(|` + "`" + `)`), "eval of command substitution"},
	{regexp.MustCompile(`\bcurl\b.*\|\s*sudo\b`), "curl piped to sudo"},
	{regexp.MustCompile(`\bdd\s+.*\bof=/dev/`), "dd write to device"},
	{regexp.MustCompile(`\bmkfs\b`), "format filesystem"},
	{regexp.MustCompile(`\b(shutdown|reboot|halt|poweroff)\b`), "system power command"},
	{regexp.MustCompile(`\bkill\s+(-[a-z0-9]+\s+)*-?(9|sigkill|kill)\s+1\b`), "kill PID 1 (init)"},
	{regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`), "fork bomb"},
	{regexp.MustCompile(`\bchmod\s+(-[a-z]+\s+)*777\b`), "chmod 777 (world-writable)"},
}

var devRedirectRE = regexp.MustCompile(`>\s*/dev/([a-z][a-z0-9/]*)`)

func IsDangerousCommand(cmd string) (bool, string) {
	normalized := strings.ToLower(strings.TrimSpace(cmd))
	for _, p := range dangerousPatterns {
		if p.re.MatchString(normalized) {
			return true, p.reason
		}
	}
	if m := devRedirectRE.FindStringSubmatch(normalized); m != nil {
		if !safeDevTargets.MatchString(m[1]) {
			return true, "redirect to device file"
		}
	}
	return false, ""
}
