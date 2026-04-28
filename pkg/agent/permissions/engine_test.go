// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import (
	"context"
	"sync"
	"testing"
)

// makeEngine builds an engine with the given session-scope rules and
// the standard adapter set, ready for table-driven Decide tests.
func makeEngine(t *testing.T, sessionRules []Rule) *Engine {
	t.Helper()
	store := NewRuleStore()
	for _, r := range sessionRules {
		store.AddSession("chat-1", r)
	}
	eng := NewEngine(store)
	RegisterDefaultAdapters(eng)
	return eng
}

func mustParse(t *testing.T, s string, b RuleBehavior) Rule {
	t.Helper()
	r, err := ParseRule(s, b, RuleSource{Scope: ScopeSession})
	if err != nil {
		t.Fatalf("ParseRule(%q): %v", s, err)
	}
	return r
}

func TestDecide_BenchPostureSkipsEverything(t *testing.T) {
	// Even with a deny rule and a dangerous shell command, bench
	// posture allows everything.
	rules := []Rule{
		mustParse(t, "shell_exec", RuleDeny),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "rm -rf /"},
		ChatId:   "chat-1",
		Posture:  PostureBench,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("bench should allow, got %v (reason %v)", d.Behavior, d.Reason)
	}
	if d.Reason.Detail != "bench" {
		t.Errorf("expected reason detail=bench, got %v", d.Reason.Detail)
	}
}

func TestDecide_DenyRuleWins(t *testing.T) {
	// Tool-level deny takes precedence over content-specific allow,
	// posture, and per-tool default.
	rules := []Rule{
		mustParse(t, "shell_exec", RuleDeny),
		mustParse(t, "shell_exec(prefix:npm)", RuleAllow),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "npm install"},
		ChatId:   "chat-1",
		Posture:  PostureBypass, // even bypass shouldn't override deny
	})
	if d.Behavior != RuleDeny {
		t.Errorf("expected deny, got %v (reason %v)", d.Behavior, d.Reason)
	}
}

func TestDecide_ContentRuleMatch(t *testing.T) {
	rules := []Rule{
		mustParse(t, "shell_exec(prefix:npm)", RuleAllow),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "npm install foo"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("expected allow, got %v (reason %v)", d.Behavior, d.Reason)
	}
	if d.Reason.Kind != ReasonRule {
		t.Errorf("expected reason=rule, got %v", d.Reason.Kind)
	}
}

func TestDecide_ContentRuleAskFiresSuggestions(t *testing.T) {
	rules := []Rule{
		mustParse(t, "shell_exec(prefix:git push)", RuleAsk),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "git push origin main"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAsk {
		t.Errorf("expected ask, got %v", d.Behavior)
	}
	if len(d.Suggestions) == 0 {
		t.Errorf("expected suggestions populated for Ask decision")
	}
}

func TestDecide_SafetyBeatsAllowRule(t *testing.T) {
	// `allow shell_exec` rule, but `rm -rf /` triggers the safety
	// layer (step 4) before we'd reach the tool-level allow (step 5).
	// Default posture — bypass intentionally skips safety in v2.
	rules := []Rule{
		mustParse(t, "shell_exec", RuleAllow),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "rm -rf /"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAsk {
		t.Errorf("expected ask (safety check), got %v", d.Behavior)
	}
	if !d.Reason.BypassImmune {
		t.Errorf("expected BypassImmune=true on safety reason")
	}
}

// TestDecide_BypassSkipsContentDeny locks in the contract that bypass
// also bypasses CONTENT-specific Deny rules (e.g. builtin
// `shell_exec(prefix:sudo)` RuleDeny). Only tool-level Deny (Content="")
// survives bypass. This mirrors Claude Code's --dangerously-skip-permissions:
// a partial Deny isn't a hard wall, only a whole-tool Deny is.
func TestDecide_BypassSkipsContentDeny(t *testing.T) {
	rules := []Rule{
		mustParse(t, "shell_exec(prefix:sudo)", RuleDeny),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "sudo apt install foo"},
		ChatId:   "chat-1",
		Posture:  PostureBypass,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("bypass should auto-allow even when content-Deny matches, got %v (reason %v)", d.Behavior, d.Reason)
	}
	if d.Reason.Detail != "bypass" {
		t.Errorf("expected reason detail=bypass, got %q", d.Reason.Detail)
	}
}

// TestDecide_BypassRespectsToolLevelDeny: the one exception to bypass's
// "let everything through" contract — tool-level (Content="") Deny rules
// still fire. The user (or a builtin) saying "never run this whole tool"
// is treated as inviolable.
func TestDecide_BypassRespectsToolLevelDeny(t *testing.T) {
	rules := []Rule{
		mustParse(t, "shell_exec", RuleDeny),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "ls"},
		ChatId:   "chat-1",
		Posture:  PostureBypass,
	})
	if d.Behavior != RuleDeny {
		t.Errorf("tool-level Deny should fire even under bypass, got %v (reason %v)", d.Behavior, d.Reason)
	}
}

// TestDecide_BypassAllowsSensitivePaths locks in the v2 contract:
// bypass posture is the user's explicit full-trust mode. .env writes,
// rm -rf, and other normally-prompted operations all auto-allow. Only
// tool-level Deny rules still apply (covered separately).
func TestDecide_BypassAllowsSensitivePaths(t *testing.T) {
	store := NewRuleStore()
	store.SetBuiltinRules(BuiltinRules())
	eng := NewEngine(store)
	RegisterDefaultAdapters(eng)

	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "edit_text_file",
		Input:    map[string]any{"filename": "/Users/me/.env"},
		ChatId:   "chat-1",
		Posture:  PostureBypass,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("bypass should allow .env writes, got %v (reason %v)", d.Behavior, d.Reason)
	}
	if d.Reason.Detail != "bypass" {
		t.Errorf("expected reason detail=bypass, got %q", d.Reason.Detail)
	}
}

func TestDecide_BypassPostureAllowsUnmatched(t *testing.T) {
	eng := makeEngine(t, nil)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "npm install"},
		ChatId:   "chat-1",
		Posture:  PostureBypass,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("bypass should allow, got %v", d.Behavior)
	}
	if d.Reason.Detail != "bypass" {
		t.Errorf("expected reason detail=bypass, got %q", d.Reason.Detail)
	}
}

func TestDecide_AcceptEditsInsideCwd(t *testing.T) {
	eng := makeEngine(t, nil)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "edit_text_file",
		Input:    map[string]any{"filename": "/Users/me/work/main.go"},
		ChatId:   "chat-1",
		Cwd:      "/Users/me/work",
		Posture:  PostureAcceptEdits,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("acceptEdits inside cwd should allow, got %v", d.Behavior)
	}
}

func TestDecide_AcceptEditsOutsideCwd(t *testing.T) {
	eng := makeEngine(t, nil)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "edit_text_file",
		Input:    map[string]any{"filename": "/Users/other/work/main.go"},
		ChatId:   "chat-1",
		Cwd:      "/Users/me/work",
		Posture:  PostureAcceptEdits,
	})
	// Falls through to per-tool default (Ask).
	if d.Behavior != RuleAsk {
		t.Errorf("acceptEdits outside cwd should fall through to ask, got %v", d.Behavior)
	}
}

func TestDecide_AcceptEditsDoesNotAffectShell(t *testing.T) {
	eng := makeEngine(t, nil)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "npm install"},
		ChatId:   "chat-1",
		Cwd:      "/Users/me/work",
		Posture:  PostureAcceptEdits,
	})
	// shell_exec is not a file edit; falls through to per-tool default (Ask).
	if d.Behavior != RuleAsk {
		t.Errorf("acceptEdits should leave shell_exec at ask, got %v", d.Behavior)
	}
}

func TestDecide_DefaultPostureRespectsAllowRule(t *testing.T) {
	rules := []Rule{
		mustParse(t, "shell_exec(prefix:git status)", RuleAllow),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "git status"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("default posture should respect allow rule, got %v", d.Behavior)
	}
}

func TestDecide_NoRuleNoAdapter_DefaultsToAsk(t *testing.T) {
	eng := makeEngine(t, nil) // no rules, no adapter for "weird_tool"
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "weird_tool",
		Input:    map[string]any{},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAsk {
		t.Errorf("unknown tool should default to ask, got %v", d.Behavior)
	}
}

func TestDecide_ReadFileDefaultsToAllow(t *testing.T) {
	eng := makeEngine(t, nil)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "read_text_file",
		Input:    map[string]any{"filename": "/tmp/foo"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("read_text_file should default to allow, got %v", d.Behavior)
	}
	if d.Reason.Kind != ReasonToolDefault {
		t.Errorf("expected reason=toolDefault, got %v", d.Reason.Kind)
	}
}

func TestDecide_BuiltinRulesAllowReads(t *testing.T) {
	store := NewRuleStore()
	store.SetBuiltinRules(BuiltinRules())
	eng := NewEngine(store)
	RegisterDefaultAdapters(eng)

	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "git status"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("builtin allow `git status` should fire, got %v (reason %v)", d.Behavior, d.Reason)
	}
}

// TestDecide_BuiltinRulesAskEnvWrite verifies the v2 contract: built-
// in `.env` write rules are Ask (not Deny). Under default/acceptEdits
// the user is prompted; bypass auto-allows (covered by
// TestDecide_BypassAllowsSensitivePaths).
func TestDecide_BuiltinRulesAskEnvWrite(t *testing.T) {
	store := NewRuleStore()
	store.SetBuiltinRules(BuiltinRules())
	eng := NewEngine(store)
	RegisterDefaultAdapters(eng)

	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "edit_text_file",
		Input:    map[string]any{"filename": "/Users/me/repo/.env"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAsk {
		t.Errorf("builtin ask .env should fire, got %v (reason %v)", d.Behavior, d.Reason)
	}
}

// TestDecide_ContentAllowDoesNotBypassSafety is the C1 regression
// test from the v2 code review. A content-specific Allow rule must
// NOT short-circuit before the safety layer; otherwise a rule like
// `shell_exec(prefix:echo)` would auto-allow `echo `rm -rf /`` (the
// shell substitutes the inner command). The fix is to defer Allow
// rules until step 6 — after safety in step 5.
func TestDecide_ContentAllowDoesNotBypassSafety(t *testing.T) {
	rules := []Rule{
		mustParse(t, "shell_exec(prefix:echo)", RuleAllow),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "echo `rm -rf /`"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAsk {
		t.Errorf("content-Allow should not bypass safety, got %v (reason %v)", d.Behavior, d.Reason)
	}
	if d.Reason.Kind != ReasonSafetyCheck {
		t.Errorf("expected safety reason, got %v", d.Reason.Kind)
	}
	if !d.Reason.BypassImmune {
		t.Errorf("expected BypassImmune=true on safety override")
	}
}

// TestDecide_PathAllowDoesNotBypassEnvSafety covers the file-tool
// analogue: a parent-dir Allow rule must not auto-approve writes to
// `.env` inside that dir.
func TestDecide_PathAllowDoesNotBypassEnvSafety(t *testing.T) {
	rules := []Rule{
		mustParse(t, "edit_text_file(/Users/me/work/**)", RuleAllow),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "edit_text_file",
		Input:    map[string]any{"filename": "/Users/me/work/.env"},
		ChatId:   "chat-1",
		Cwd:      "/Users/me/work",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAsk {
		t.Errorf("path-Allow should not bypass .env safety, got %v (reason %v)", d.Behavior, d.Reason)
	}
	if d.Reason.Kind != ReasonSafetyCheck {
		t.Errorf("expected safety reason, got %v", d.Reason.Kind)
	}
}

// TestDecide_ContentAllowStillFiresWhenSafe verifies the fix didn't
// regress the happy path: a content-Allow rule should still allow
// when safety doesn't trigger.
func TestDecide_ContentAllowStillFiresWhenSafe(t *testing.T) {
	rules := []Rule{
		mustParse(t, "shell_exec(prefix:npm)", RuleAllow),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "npm install foo"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAllow {
		t.Errorf("content-Allow should still fire when safety doesn't, got %v (reason %v)", d.Behavior, d.Reason)
	}
}

// TestDecide_ContentDenyStillShortCircuitsBeforeSafety: Deny rules
// MUST keep their early-return semantics. The C1 fix only deferred
// Allow; Deny should still win immediately.
func TestDecide_ContentDenyStillShortCircuitsBeforeSafety(t *testing.T) {
	rules := []Rule{
		mustParse(t, "shell_exec(prefix:echo)", RuleDeny),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "echo `rm -rf /`"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleDeny {
		t.Errorf("content-Deny should fire before safety, got %v (reason %v)", d.Behavior, d.Reason)
	}
	if d.Reason.Kind != ReasonRule {
		t.Errorf("expected reason=rule (not safety), got %v", d.Reason.Kind)
	}
}

func TestDecide_DefaultPostureContentRuleAskBeatsAllow(t *testing.T) {
	// More-specific ask rule should win over a less-specific allow,
	// thanks to the specificity sort in Load().
	rules := []Rule{
		mustParse(t, "shell_exec(prefix:git)", RuleAllow),
		mustParse(t, "shell_exec(prefix:git push)", RuleAsk),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "git push origin main"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAsk {
		t.Errorf("more-specific ask should beat broader allow, got %v", d.Behavior)
	}
}

// TestDecide_NoDenyRule_SafetyStillCatchesEnv is the S4 companion to
// TestDecide_BuiltinRulesDenyEnvWrite. With the deny rule removed,
// the safety layer (step 5) is the last line of defense before the
// allow rule (step 6) would kick in. This is exactly the
// configuration that would have surfaced C1 if it had been written
// before the bug fix.
func TestDecide_NoDenyRule_SafetyStillCatchesEnv(t *testing.T) {
	// Tool-level allow on edit_text_file, NO deny rule. Safety must
	// still catch the .env path under default/acceptEdits posture.
	// (Bypass intentionally skips safety in v2.)
	rules := []Rule{
		mustParse(t, "edit_text_file", RuleAllow),
	}
	eng := makeEngine(t, rules)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "edit_text_file",
		Input:    map[string]any{"filename": "/Users/me/work/.env"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Behavior != RuleAsk {
		t.Errorf("safety should catch .env even without deny rule, got %v (reason %v)", d.Behavior, d.Reason)
	}
	if d.Reason.Kind != ReasonSafetyCheck {
		t.Errorf("expected safety reason, got %v", d.Reason.Kind)
	}
}

// TestDecide_AcceptEditsRejectsParentTraversal verifies that
// `..` in target paths gets normalized by isInsideCwd's filepath.Clean
// — a path like /Users/me/work/../other/.env is recognized as
// OUTSIDE /Users/me/work and falls through to default behavior
// rather than being auto-allowed by acceptEdits.
func TestDecide_AcceptEditsRejectsParentTraversal(t *testing.T) {
	eng := makeEngine(t, nil)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "edit_text_file",
		Input:    map[string]any{"filename": "/Users/me/work/../other/main.go"},
		ChatId:   "chat-1",
		Cwd:      "/Users/me/work",
		Posture:  PostureAcceptEdits,
	})
	// The path resolves to /Users/me/other/main.go which is outside
	// /Users/me/work; should fall through to default Ask.
	if d.Behavior != RuleAsk {
		t.Errorf("traversal out of cwd should fall through to ask, got %v", d.Behavior)
	}
}

// TestDecide_SafetyAskCarriesSuggestions: when the safety layer
// fires, the FE prompt should still show "remember this" suggestions
// from the adapter so the user can opt-in (e.g. allow this exact
// .env file once). Step 5 must populate Suggestions when an adapter
// is present.
func TestDecide_SafetyAskCarriesSuggestions(t *testing.T) {
	eng := makeEngine(t, nil)
	d := eng.Decide(context.Background(), CheckRequest{
		ToolName: "edit_text_file",
		Input:    map[string]any{"filename": "/Users/me/.env"},
		ChatId:   "chat-1",
		Posture:  PostureDefault,
	})
	if d.Reason.Kind != ReasonSafetyCheck {
		t.Fatalf("test misconfigured: expected safety check, got %v", d.Reason.Kind)
	}
	if len(d.Suggestions) == 0 {
		t.Errorf("safety-triggered Ask should still populate Suggestions for the FE prompt")
	}
}

// TestDecide_ContextCanceled fails closed: a cancelled ctx must NOT
// silently auto-allow a tool call. Defensive against the day
// UserScopeBackend.Load goes async and a slow read is racing
// cancellation.
func TestDecide_ContextCanceled(t *testing.T) {
	eng := makeEngine(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := eng.Decide(ctx, CheckRequest{
		ToolName: "shell_exec",
		Input:    map[string]any{"command": "ls"},
		ChatId:   "chat-1",
		Posture:  PostureBypass,
	})
	if d.Behavior != RuleDeny {
		t.Errorf("canceled ctx should fail closed (Deny), got %v (reason %v)", d.Behavior, d.Reason)
	}
}

// TestDecide_ConcurrentSafe verifies the engine's adapter map can
// take parallel Decide calls — the RWMutex inside lookupAdapter
// makes this safe by design but a race-detector test catches any
// future regression where someone writes adapters during a Decide.
func TestDecide_ConcurrentSafe(t *testing.T) {
	rules := []Rule{
		mustParse(t, "shell_exec(prefix:npm)", RuleAllow),
	}
	eng := makeEngine(t, rules)
	var wg sync.WaitGroup
	const goroutines = 20
	const callsEach = 100
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsEach; j++ {
				_ = eng.Decide(context.Background(), CheckRequest{
					ToolName: "shell_exec",
					Input:    map[string]any{"command": "npm install"},
					ChatId:   "chat-1",
					Posture:  PostureDefault,
				})
			}
		}()
	}
	wg.Wait()
}
