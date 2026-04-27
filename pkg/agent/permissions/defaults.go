// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

// BuiltinRules returns the §9 default rule set baked into the binary.
// Lives at the lowest precedence (ScopeBuiltin) so users can override
// any single rule by adding their own at a higher scope. They become
// "real" persisted rules in settings.json only when the user changes
// them via the UI; until then they're just a safe floor.
//
// Two principles drove the list:
//
//   - **Don't make users approve obviously safe reads** — read_text_file,
//     search, todo_read, etc. on `*` because there's nothing destructive
//     about a read.
//   - **Don't auto-approve anything that mutates state**, even something
//     as tame as `npm install`. The user might be in a tree where they
//     don't want anything installed; let them opt in once via the
//     approval prompt's "remember this" suggestion.
//
// The shell allow list (`prefix:git status`, `prefix:ls`, etc.) is
// pure-read shell commands — running these prompt-free matches what a
// user would expect in any interactive shell session.
func BuiltinRules() []Rule {
	src := RuleSource{Scope: ScopeBuiltin}
	specs := []struct {
		s string
		b RuleBehavior
	}{
		// Reads — every tool that surfaces information without writing.
		{"read_text_file", RuleAllow},
		{"read_dir", RuleAllow},
		{"search", RuleAllow},
		{"get_scrollback", RuleAllow},
		{"cmd_history", RuleAllow},
		{"todo_read", RuleAllow},
		{"todo_write", RuleAllow}, // todo state lives in chat-local storage; safe
		// Pure-read shell commands. Note we use prefix: so "git status -v"
		// also matches; the parsed first-word matters, not the whole
		// command string.
		//
		// Deliberately omitted: `prefix:cat` and `prefix:echo`. They look
		// safe but aren't:
		//   - `cat /etc/passwd`, `cat ~/.ssh/id_rsa`, `cat .env` would
		//     auto-approve secret exfiltration. The shell file-tool
		//     safety list applies to write tools, not to shell reads.
		//   - `echo $(rm -rf /)` and `echo \`rm -rf /\`` rely on shell
		//     substitution; the safety substring matcher anchors on
		//     "rm -rf /" which doesn't appear inside an echo string,
		//     and the Allow rule sits below safety in the pipeline so
		//     safety wins anyway — but the cost of the prompt is one
		//     click vs. the cost of an auto-approved exfil is a leak.
		// `read_text_file` covers the legitimate "agent reads a file"
		// path with proper bypass-immune safety hooks.
		{"shell_exec(prefix:git status)", RuleAllow},
		{"shell_exec(prefix:git diff)", RuleAllow},
		{"shell_exec(prefix:git log)", RuleAllow},
		{"shell_exec(prefix:git show)", RuleAllow},
		{"shell_exec(prefix:git branch)", RuleAllow},
		{"shell_exec(prefix:ls)", RuleAllow},
		{"shell_exec(prefix:pwd)", RuleAllow},

		// Hard denies — these mirror parts of the bypass-immune safety
		// list, but as RuleDeny they reject without prompting at all.
		// The safety layer would just ASK; deny rules block outright.
		{"shell_exec(prefix:sudo)", RuleDeny},
		{"shell_exec(prefix:rm -rf /)", RuleDeny},
		{"shell_exec(prefix:rm -rf ~)", RuleDeny},
		// File writes to obvious-secret paths. These are also bypass-
		// immune (would prompt anyway under acceptEdits/bypass), but
		// having them as deny rules means the model sees a clean
		// "denied" rather than waiting on a prompt that the user has
		// to dismiss every time.
		{"edit_text_file(**/.env)", RuleDeny},
		{"edit_text_file(**/.env.*)", RuleDeny},
		{"edit_text_file(**/.ssh/**)", RuleDeny},
		{"write_text_file(**/.env)", RuleDeny},
		{"write_text_file(**/.env.*)", RuleDeny},
		{"write_text_file(**/.ssh/**)", RuleDeny},
		{"multi_edit(**/.env)", RuleDeny},
		{"multi_edit(**/.env.*)", RuleDeny},
		{"multi_edit(**/.ssh/**)", RuleDeny},
	}

	rules := make([]Rule, 0, len(specs))
	for _, sp := range specs {
		r, err := ParseRule(sp.s, sp.b, src)
		if err != nil {
			// Bug in this file — caught by the test that round-trips
			// every spec. We don't want to swallow the error silently
			// in production though, so panic to surface it loudly.
			panic("permissions: bad builtin rule " + sp.s + ": " + err.Error())
		}
		rules = append(rules, r)
	}
	return rules
}
