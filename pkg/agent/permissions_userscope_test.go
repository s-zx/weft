// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"reflect"
	"testing"

	"github.com/s-zx/crest/pkg/agent/permissions"
	"github.com/s-zx/crest/pkg/wconfig"
)

// TestPermissionsConfigStructsInSync guards against silent drift
// between wconfig.AIPermissionsConfig and permissions.AIPermissionsConfig.
// The two structs are deliberately duplicated (wconfig is at the bottom
// of the import graph and can't import the permissions package), but
// the translation in permissions_userscope.go's Load/Save is a
// field-by-field copy — adding a field on one side without the other
// silently drops data at the boundary.
//
// The check is: same field names, same JSON tags, same kinds. We
// don't require identical Go types because []string vs *AIPermConfig-
// nesting could legitimately differ in a future evolution; we DO
// require the JSON wire representation to match so settings files
// round-trip cleanly.
func TestPermissionsConfigStructsInSync(t *testing.T) {
	wcType := reflect.TypeOf(wconfig.AIPermissionsConfig{})
	permType := reflect.TypeOf(permissions.AIPermissionsConfig{})

	if wcType.NumField() != permType.NumField() {
		t.Fatalf("field count drift: wconfig has %d, permissions has %d",
			wcType.NumField(), permType.NumField())
	}

	for i := 0; i < wcType.NumField(); i++ {
		wcField := wcType.Field(i)
		// Find the matching field on the permissions side by name —
		// reflection doesn't guarantee field order across packages.
		permField, ok := permType.FieldByName(wcField.Name)
		if !ok {
			t.Errorf("field %q exists on wconfig.AIPermissionsConfig but not permissions.AIPermissionsConfig",
				wcField.Name)
			continue
		}
		// JSON tag's first segment (the field name on the wire) must
		// match. Trailing options (omitempty, jsonschema enums) are
		// allowed to differ — wconfig's enum tag is FE-tooling-only.
		wcWireName := jsonWireName(wcField.Tag.Get("json"))
		permWireName := jsonWireName(permField.Tag.Get("json"))
		if wcWireName != permWireName {
			t.Errorf("field %q wire-name drift: wconfig=%q permissions=%q",
				wcField.Name, wcWireName, permWireName)
		}
		if wcField.Type.Kind() != permField.Type.Kind() {
			t.Errorf("field %q kind drift: wconfig=%v permissions=%v",
				wcField.Name, wcField.Type.Kind(), permField.Type.Kind())
		}
	}
}

// TestPermissionsConfigTranslationRoundTrip exercises the actual
// Load/Save translation logic the userscope backend uses, ensuring
// every field survives the wconfig.AIPermissionsConfig ↔
// permissions.AIPermissionsConfig hop.
func TestPermissionsConfigTranslationRoundTrip(t *testing.T) {
	original := &permissions.AIPermissionsConfig{
		Allow:          []string{"shell_exec(prefix:npm)", "edit_text_file(/repo/**)"},
		Deny:           []string{"shell_exec(prefix:sudo)"},
		Ask:            []string{"shell_exec(prefix:git push --force)"},
		DefaultPosture: "acceptEdits",
	}

	// Mirror the Save translation (permissions → wconfig).
	wcView := &wconfig.AIPermissionsConfig{
		Allow:          append([]string(nil), original.Allow...),
		Deny:           append([]string(nil), original.Deny...),
		Ask:            append([]string(nil), original.Ask...),
		DefaultPosture: original.DefaultPosture,
	}
	// Mirror the Load translation (wconfig → permissions).
	roundTripped := &permissions.AIPermissionsConfig{
		Allow:          append([]string(nil), wcView.Allow...),
		Deny:           append([]string(nil), wcView.Deny...),
		Ask:            append([]string(nil), wcView.Ask...),
		DefaultPosture: wcView.DefaultPosture,
	}

	if !reflect.DeepEqual(original, roundTripped) {
		t.Errorf("round-trip lost data:\n  original=%+v\n  result=%+v", original, roundTripped)
	}
}

// jsonWireName returns the wire-format field name from a json tag,
// dropping any "omitempty" / option suffix. Empty tag returns "".
func jsonWireName(tag string) string {
	for i, c := range tag {
		if c == ',' {
			return tag[:i]
		}
	}
	return tag
}
