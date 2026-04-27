// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fakeUserScope is an in-memory UserScopeBackend for tests.
type fakeUserScope struct {
	cfg *AIPermissionsConfig
}

func (f *fakeUserScope) Load() (*AIPermissionsConfig, error) { return f.cfg, nil }
func (f *fakeUserScope) Save(cfg *AIPermissionsConfig) error  { f.cfg = cfg; return nil }

func TestRuleStore_LoadEmpty(t *testing.T) {
	s := NewRuleStore()
	rules, err := s.Load(context.Background(), "chat-1", "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("empty store should return no rules, got %d", len(rules))
	}
}

func TestRuleStore_SessionRules(t *testing.T) {
	s := NewRuleStore()
	r1, _ := ParseRule("shell_exec(prefix:npm)", RuleAllow, RuleSource{})
	r2, _ := ParseRule("shell_exec(prefix:git push)", RuleAsk, RuleSource{})
	s.AddSession("chat-1", r1)
	s.AddSession("chat-1", r2)

	rules, err := s.Load(context.Background(), "chat-1", "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(rules) != 2 {
		t.Errorf("got %d rules, want 2", len(rules))
	}
	for _, r := range rules {
		if r.Source.Scope != ScopeSession {
			t.Errorf("session rule has wrong scope: %v", r.Source.Scope)
		}
	}

	// Different chat sees nothing.
	rules2, _ := s.Load(context.Background(), "chat-2", "")
	if len(rules2) != 0 {
		t.Errorf("chat-2 should not see chat-1 rules, got %d", len(rules2))
	}

	// ClearSession drops them.
	s.ClearSession("chat-1")
	rules3, _ := s.Load(context.Background(), "chat-1", "")
	if len(rules3) != 0 {
		t.Errorf("after ClearSession got %d rules, want 0", len(rules3))
	}
}

func TestRuleStore_BuiltinRules(t *testing.T) {
	s := NewRuleStore()
	r, _ := ParseRule("read_text_file", RuleAllow, RuleSource{Scope: ScopeBuiltin})
	s.SetBuiltinRules([]Rule{r})

	rules, _ := s.Load(context.Background(), "chat-1", "")
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Source.Scope != ScopeBuiltin {
		t.Errorf("expected builtin scope, got %v", rules[0].Source.Scope)
	}
}

func TestRuleStore_ProjectFiles(t *testing.T) {
	dir := t.TempDir()
	crestDir := filepath.Join(dir, ".crest")
	if err := os.MkdirAll(crestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sharedJSON := `{"allow": ["shell_exec(prefix:npm test)"], "deny": ["edit_text_file(**/.env)"]}`
	if err := os.WriteFile(filepath.Join(crestDir, "permissions.json"), []byte(sharedJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	localJSON := `{"allow": ["shell_exec(prefix:make)"]}`
	if err := os.WriteFile(filepath.Join(crestDir, "permissions.local.json"), []byte(localJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewRuleStore()
	rules, err := s.Load(context.Background(), "chat-1", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 3", len(rules))
	}

	// Higher-precedence scopes appear first after sortRulesForMatching.
	// Order: sharedProject > localProject (per RuleScope const ordering).
	gotScopes := make([]RuleScope, len(rules))
	for i, r := range rules {
		gotScopes[i] = r.Source.Scope
	}
	// We expect shared rules before local rules. There are 2 shared, 1 local.
	for i, sc := range gotScopes {
		if i < 2 && sc != ScopeSharedProject {
			t.Errorf("rule[%d] expected sharedProject, got %v", i, sc)
		}
		if i == 2 && sc != ScopeLocalProject {
			t.Errorf("rule[%d] expected localProject, got %v", i, sc)
		}
	}
}

func TestRuleStore_UserScopeBackend(t *testing.T) {
	s := NewRuleStore()
	backend := &fakeUserScope{cfg: &AIPermissionsConfig{
		Allow:          []string{"shell_exec(prefix:git status)"},
		DefaultPosture: "bypass",
	}}
	s.SetUserScopeBackend(backend)

	rules, _ := s.Load(context.Background(), "chat-1", "")
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Source.Scope != ScopeUser {
		t.Errorf("expected user scope, got %v", rules[0].Source.Scope)
	}

	if got := s.LoadDefaultPosture(); got != PostureBypass {
		t.Errorf("LoadDefaultPosture = %v, want %v", got, PostureBypass)
	}
}

func TestRuleStore_LoadDefaultPosture_Fallback(t *testing.T) {
	s := NewRuleStore()
	// No backend wired.
	if got := s.LoadDefaultPosture(); got != PostureAcceptEdits {
		t.Errorf("default with no backend = %v, want acceptEdits", got)
	}

	// Backend with empty/invalid value.
	s.SetUserScopeBackend(&fakeUserScope{cfg: &AIPermissionsConfig{DefaultPosture: ""}})
	if got := s.LoadDefaultPosture(); got != PostureAcceptEdits {
		t.Errorf("empty DefaultPosture should fall back to acceptEdits, got %v", got)
	}
	s.SetUserScopeBackend(&fakeUserScope{cfg: &AIPermissionsConfig{DefaultPosture: "garbage"}})
	if got := s.LoadDefaultPosture(); got != PostureAcceptEdits {
		t.Errorf("invalid DefaultPosture should fall back to acceptEdits, got %v", got)
	}
}

func TestRuleStore_PersistProjectFile(t *testing.T) {
	dir := t.TempDir()
	s := NewRuleStore()
	r, _ := ParseRule("shell_exec(prefix:npm)", RuleAllow, RuleSource{})
	if err := s.Persist(ScopeSharedProject, dir, []Rule{r}); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	// File should exist with the expected contents.
	data, err := os.ReadFile(filepath.Join(dir, ".crest", "permissions.json"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !contains(string(data), "shell_exec(prefix:npm)") {
		t.Errorf("written file does not contain rule: %s", data)
	}

	// Re-load to confirm round-trip.
	rules, _ := s.Load(context.Background(), "chat-1", dir)
	if len(rules) != 1 {
		t.Fatalf("after persist+load got %d rules, want 1", len(rules))
	}
	if rules[0].ToolName != "shell_exec" || rules[0].Content != "prefix:npm" {
		t.Errorf("round-trip mangled rule: %+v", rules[0])
	}
}

func TestRuleStore_PersistRequiresCwd(t *testing.T) {
	s := NewRuleStore()
	r, _ := ParseRule("shell_exec(prefix:npm)", RuleAllow, RuleSource{})
	if err := s.Persist(ScopeSharedProject, "", []Rule{r}); err == nil {
		t.Errorf("Persist(sharedProject, cwd=\"\") should fail")
	}
	if err := s.Persist(ScopeLocalProject, "", []Rule{r}); err == nil {
		t.Errorf("Persist(localProject, cwd=\"\") should fail")
	}
	if err := s.Persist(ScopeSession, "", []Rule{r}); err == nil {
		t.Errorf("Persist(session, ...) should fail (use AddSession instead)")
	}
}
