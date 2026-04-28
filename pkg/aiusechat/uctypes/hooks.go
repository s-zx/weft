// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package uctypes

import "context"

// HookContext is the immutable input passed to a tool hook. Hooks should
// treat all fields as read-only; the tool result is mutated separately
// (BeforeToolHook returns a replacement, AfterToolHook mutates in place).
type HookContext struct {
	ToolCall WaveToolCall
	ToolDef  *ToolDefinition
	ChatOpts *WaveChatOpts
}

// BeforeToolHook runs after argument validation and approval but before the
// tool's callback. Return a non-nil *AIToolResult to short-circuit execution
// with that result (typically an error). Return nil to allow execution to
// proceed and the next hook in the chain to run.
//
// Hooks must not block on user interaction or external I/O for long; the
// tool dispatcher already serializes per-tool execution. Hooks must NOT
// panic; the dispatcher does not recover, so a panic propagates up through
// processToolCall and aborts the step. If a hook needs to fail, return an
// AIToolResult with ErrorText set instead.
type BeforeToolHook func(ctx context.Context, h HookContext) *AIToolResult

// AfterToolHook runs after the tool callback completes (whether successful
// or errored). It mutates the result pointer in place. Hooks run in
// registration order; later hooks see earlier hooks' mutations.
//
// AfterToolHooks must not assume success — check result.ErrorText. They are
// the right place for size capping, error classification, and post-write
// bookkeeping. Same panic contract as BeforeToolHook: don't panic; if the
// hook can't proceed, leave the result alone and let the dispatcher's
// later stages handle it.
type AfterToolHook func(ctx context.Context, h HookContext, result *AIToolResult)
