// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"fmt"

	"github.com/s-zx/crest/pkg/agent/permissions"
	"github.com/s-zx/crest/pkg/waveobj"
	"github.com/s-zx/crest/pkg/wconfig"
)

// wconfigUserScope is the production UserScopeBackend that reads/writes
// the user-scope permission rule set through wconfig's settings.json.
// Read goes through the watcher's cached FullConfig (no extra IO);
// write uses SetBaseConfigValue which round-trips the file with proper
// type validation.
//
// Lives in pkg/agent (not pkg/agent/permissions) so the permissions
// package stays free of wconfig as a transitive dependency — the
// engine can be reused in tests / harnesses without dragging the
// settings file watcher along.
type wconfigUserScope struct{}

// NewWconfigUserScope returns the singleton UserScopeBackend wired
// against wconfig. Stateless; safe to share across engines.
func NewWconfigUserScope() permissions.UserScopeBackend {
	return wconfigUserScope{}
}

// Load returns the user-scope permissions config from settings.json
// (cached by the wconfig watcher). Returns (nil, nil) when no
// `ai:permissions` block is configured — that's a valid empty state.
func (wconfigUserScope) Load() (*permissions.AIPermissionsConfig, error) {
	cfg := wconfig.GetWatcher().GetFullConfig()
	src := cfg.Settings.AiPermissions
	if src == nil {
		return nil, nil
	}
	return &permissions.AIPermissionsConfig{
		Allow:          append([]string(nil), src.Allow...),
		Deny:           append([]string(nil), src.Deny...),
		Ask:            append([]string(nil), src.Ask...),
		DefaultPosture: src.DefaultPosture,
	}, nil
}

// Save writes the user-scope permissions block back to settings.json.
// Uses wconfig.SetBaseConfigValue so the watcher picks up the change
// and the file's other settings stay intact. Passing a nil cfg clears
// the block.
func (wconfigUserScope) Save(cfg *permissions.AIPermissionsConfig) error {
	var val any
	if cfg != nil {
		// Translate to the wconfig-side struct so SetBaseConfigValue's
		// reflection check finds the right type. The two structs have
		// identical layouts; this is a copy not a cast.
		val = &wconfig.AIPermissionsConfig{
			Allow:          append([]string(nil), cfg.Allow...),
			Deny:           append([]string(nil), cfg.Deny...),
			Ask:            append([]string(nil), cfg.Ask...),
			DefaultPosture: cfg.DefaultPosture,
		}
	}
	toMerge := waveobj.MetaMapType{wconfig.ConfigKey_AiPermissions: val}
	if err := wconfig.SetBaseConfigValue(toMerge); err != nil {
		return fmt.Errorf("save ai:permissions: %w", err)
	}
	return nil
}
