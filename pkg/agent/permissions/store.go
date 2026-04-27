// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// AIPermissionsConfig is the on-disk JSON shape — same in user
// settings, sharedProject, and localProject files. The defaultPosture
// field is only honored when loaded from the user scope; project files
// don't override the user's posture preference.
//
// DUPLICATED IN: pkg/wconfig/settingsconfig.go. Keep field set in
// sync; pkg/agent/permissions_userscope.go translates between the
// two structs field-by-field and silently drops anything that exists
// on only one side.
type AIPermissionsConfig struct {
	Allow          []string `json:"allow,omitempty"`
	Deny           []string `json:"deny,omitempty"`
	Ask            []string `json:"ask,omitempty"`
	DefaultPosture string   `json:"defaultPosture,omitempty"`
}

// RuleStore loads and persists rules across the five scopes. The
// concrete struct here handles session (in-memory) and project files
// directly; user-scope load/save is delegated to a UserScopeBackend
// callback so this package doesn't import wconfig (and isn't pulled
// into wconfig's transitive deps).
type RuleStore struct {
	mu sync.Mutex

	// session: chatId → rules added via approval prompt during the run.
	session map[string][]Rule

	// builtin rules baked into the binary (lowest precedence). Set
	// once at init; never mutated after.
	builtin []Rule

	// userScope is an optional plug-in for reading/writing the user's
	// global settings.json. Production wiring lives in step 7 of the
	// design doc; tests can pass an in-memory implementation.
	userScope UserScopeBackend
}

// UserScopeBackend is the seam for user-scope rule storage. The store
// calls Load to fetch the current user-scope config and Save to
// persist updates. Returning (nil, nil) from Load is fine and means
// "no user-scope rules yet."
type UserScopeBackend interface {
	Load() (*AIPermissionsConfig, error)
	Save(cfg *AIPermissionsConfig) error
}

// NewRuleStore returns a store with empty builtin and session lists
// and no user-scope backend wired. Callers (typically in
// pkg/agent/registry.go or main agent setup) install the builtin
// defaults via SetBuiltinRules and the user backend via
// SetUserScopeBackend.
func NewRuleStore() *RuleStore {
	return &RuleStore{
		session: make(map[string][]Rule),
	}
}

// SetBuiltinRules replaces the in-binary default rule set. Intended
// for one-shot init at agent startup; not safe to call concurrently
// with Load.
func (s *RuleStore) SetBuiltinRules(rules []Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]Rule, len(rules))
	copy(cp, rules)
	s.builtin = cp
}

// SetUserScopeBackend installs the user-scope backend (typically
// wconfig-backed). nil disables user-scope loading; useful in tests.
func (s *RuleStore) SetUserScopeBackend(b UserScopeBackend) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userScope = b
}

// AddSession appends a rule to the session scope for the given
// chat. The rule's Source is overwritten to ensure ScopeSession.
func (s *RuleStore) AddSession(chatId string, rule Rule) {
	rule.Source = RuleSource{Scope: ScopeSession}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session[chatId] = append(s.session[chatId], rule)
}

// ClearSession drops all session-scope rules for the given chat.
// Called when a chat ends so the next chat starts clean.
func (s *RuleStore) ClearSession(chatId string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.session, chatId)
}

// Load returns all rules visible to a (chatId, cwd) tuple, sorted by
// (scope precedence desc, specificity desc) so the caller can walk
// them in match order. Errors from individual scope loads are logged
// to err but don't abort — a malformed project file shouldn't block
// the user scope from working.
//
// ctx is honored at the boundaries between scope reads. Today the
// only blocking I/O is the project-file read (sub-millisecond on
// local FS); when UserScopeBackend.Load goes async / network in the
// future, ctx becomes load-bearing.
//
// Order produced: cliArg (highest, not yet wired) → user →
// sharedProject → localProject → session → builtin (lowest). Within
// each scope, more-specific rules first.
func (s *RuleStore) Load(ctx context.Context, chatId, cwd string) ([]Rule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	builtin := append([]Rule(nil), s.builtin...)
	session := append([]Rule(nil), s.session[chatId]...)
	userBackend := s.userScope
	s.mu.Unlock()

	var rules []Rule
	var firstErr error
	captureErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// User scope: settings.json via the backend.
	if userBackend != nil {
		cfg, err := userBackend.Load()
		captureErr(err)
		if cfg != nil {
			rules = append(rules, parseConfig(cfg, RuleSource{Scope: ScopeUser})...)
		}
	}

	// sharedProject: <cwd>/.crest/permissions.json (committed).
	if cwd != "" {
		shared := filepath.Join(cwd, ".crest", "permissions.json")
		cfg, err := loadConfigFile(shared)
		captureErr(err)
		if cfg != nil {
			rules = append(rules, parseConfig(cfg, RuleSource{Scope: ScopeSharedProject, Path: shared})...)
		}
		// localProject: gitignored override.
		local := filepath.Join(cwd, ".crest", "permissions.local.json")
		cfg, err = loadConfigFile(local)
		captureErr(err)
		if cfg != nil {
			rules = append(rules, parseConfig(cfg, RuleSource{Scope: ScopeLocalProject, Path: local})...)
		}
	}

	rules = append(rules, session...)
	rules = append(rules, builtin...)
	sortRulesForMatching(rules)
	return rules, firstErr
}

// Persist writes the given rule list to the given scope. Session
// scope writes are routed through AddSession (caller convenience —
// Persist is for file-backed scopes). Returns an error for
// scopes that aren't writable from this method (cliArg, builtin).
//
// For the project scopes the caller must supply Cwd via the rules'
// Source.Path; the store rewrites the file at that path. For user
// scope, the configured UserScopeBackend handles the write.
func (s *RuleStore) Persist(scope RuleScope, cwd string, rules []Rule) error {
	switch scope {
	case ScopeUser:
		s.mu.Lock()
		backend := s.userScope
		s.mu.Unlock()
		if backend == nil {
			return fmt.Errorf("permissions: no user-scope backend wired")
		}
		cfg := buildConfig(rules)
		return backend.Save(cfg)
	case ScopeSharedProject:
		if cwd == "" {
			return fmt.Errorf("permissions: cwd required to persist sharedProject rules")
		}
		path := filepath.Join(cwd, ".crest", "permissions.json")
		return saveConfigFile(path, buildConfig(rules))
	case ScopeLocalProject:
		if cwd == "" {
			return fmt.Errorf("permissions: cwd required to persist localProject rules")
		}
		path := filepath.Join(cwd, ".crest", "permissions.local.json")
		return saveConfigFile(path, buildConfig(rules))
	case ScopeSession:
		return fmt.Errorf("permissions: use AddSession for session rules, not Persist")
	default:
		return fmt.Errorf("permissions: scope %s is not writable via Persist", scope)
	}
}

// loadConfigFile reads and parses one JSON file. Returns (nil, nil)
// when the file does not exist — that's a valid state, not an error.
// A malformed file returns an error so the caller can report it
// without dropping the entire load.
func loadConfigFile(path string) (*AIPermissionsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := &AIPermissionsConfig{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// saveConfigFile writes the config as pretty-printed JSON, creating
// the parent directory if needed. mode 0o600 because the file may
// contain user-specific rules (e.g. shell prefixes that reveal a
// developer's project conventions).
func saveConfigFile(path string, cfg *AIPermissionsConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// parseConfig flattens an AIPermissionsConfig into a slice of Rules
// tagged with the given source. Malformed entries are skipped (with
// no surfacing — the engine is best-effort about ignoring rules it
// can't parse, similar to how compileGlob silently tolerates bad
// patterns).
func parseConfig(cfg *AIPermissionsConfig, source RuleSource) []Rule {
	if cfg == nil {
		return nil
	}
	var rules []Rule
	for _, s := range cfg.Allow {
		if r, err := ParseRule(s, RuleAllow, source); err == nil {
			rules = append(rules, r)
		}
	}
	for _, s := range cfg.Deny {
		if r, err := ParseRule(s, RuleDeny, source); err == nil {
			rules = append(rules, r)
		}
	}
	for _, s := range cfg.Ask {
		if r, err := ParseRule(s, RuleAsk, source); err == nil {
			rules = append(rules, r)
		}
	}
	return rules
}

// buildConfig is the inverse of parseConfig — flattens a Rules slice
// into the on-disk shape, grouping by behavior. Source is dropped on
// write because the file path itself encodes the scope.
func buildConfig(rules []Rule) *AIPermissionsConfig {
	cfg := &AIPermissionsConfig{}
	for _, r := range rules {
		switch r.Behavior {
		case RuleAllow:
			cfg.Allow = append(cfg.Allow, r.String())
		case RuleDeny:
			cfg.Deny = append(cfg.Deny, r.String())
		case RuleAsk:
			cfg.Ask = append(cfg.Ask, r.String())
		}
	}
	return cfg
}

// sortRulesForMatching reorders rules so the engine's match-walk hits
// the most-specific rule first. Ordering is:
//
//  1. Higher scope first (cliArg > user > sharedProject > localProject
//     > session > builtin).
//  2. Within a scope, higher Specificity() first.
//
// The engine still applies "deny in any scope wins" on top of this —
// the sort is for tie-breaking among non-deny matches.
func sortRulesForMatching(rules []Rule) {
	stableSort(rules, func(a, b Rule) bool {
		if a.Source.Scope != b.Source.Scope {
			return a.Source.Scope > b.Source.Scope
		}
		return a.Specificity() > b.Specificity()
	})
}

// stableSort wraps sort.SliceStable to keep the file's import set
// minimal in tests that mock parts of the package.
func stableSort(rules []Rule, less func(a, b Rule) bool) {
	for i := 1; i < len(rules); i++ {
		j := i
		for j > 0 && less(rules[j], rules[j-1]) {
			rules[j], rules[j-1] = rules[j-1], rules[j]
			j--
		}
	}
}

// LoadDefaultPosture reads the user scope and returns the configured
// default posture, falling back to `acceptEdits` (the bundled default
// per §5 of the design doc) when nothing is set or the backend isn't
// wired. Used by the agent runtime when starting a new chat.
func (s *RuleStore) LoadDefaultPosture() Posture {
	s.mu.Lock()
	backend := s.userScope
	s.mu.Unlock()
	if backend == nil {
		return PostureAcceptEdits
	}
	cfg, err := backend.Load()
	if err != nil || cfg == nil {
		return PostureAcceptEdits
	}
	switch Posture(cfg.DefaultPosture) {
	case PostureDefault, PostureAcceptEdits, PostureBypass:
		return Posture(cfg.DefaultPosture)
	default:
		return PostureAcceptEdits
	}
}
