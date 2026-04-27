// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package aiusechat

// AcceptedSuggestion is what the FE posts back when the user clicks
// "Approve and Remember" in the approval prompt. Lives in aiusechat
// (not in the permissions package) so wshserver can call into here
// without dragging the agent package's transitive deps along.
//
// The agent runtime (which DOES know about pkg/agent/permissions)
// registers a persister at startup via RegisterAcceptedSuggestionPersister.
// Until that registration happens (e.g. in tests), accepted
// suggestions are silently dropped — the user's approve still works,
// just without remembering.
type AcceptedSuggestion struct {
	ChatId      string
	ToolCallId  string
	ToolName    string
	Content     string
	Destination string // "session" | "localProject" | "sharedProject" | "user"
	Cwd         string // required for project-scope persistence
}

var acceptedSuggestionPersister func(s AcceptedSuggestion) error

// RegisterAcceptedSuggestionPersister installs the function that turns
// an accepted suggestion into a persisted permission rule. Called once
// at agent startup with a closure that wraps a permissions.Engine.
// Calling twice replaces the previous registration; nil disables
// persistence.
func RegisterAcceptedSuggestionPersister(fn func(AcceptedSuggestion) error) {
	acceptedSuggestionPersister = fn
}

// PersistAcceptedSuggestion is the wshserver-facing entry point. It
// no-ops when no persister is registered so the unit-test path
// doesn't have to set one up.
func PersistAcceptedSuggestion(s AcceptedSuggestion) error {
	if acceptedSuggestionPersister == nil {
		return nil
	}
	return acceptedSuggestionPersister(s)
}
