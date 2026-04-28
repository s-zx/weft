// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package aiusechat

import (
	"context"
	"strings"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

// reflectionSuffix is appended to every tool error so the model is nudged
// to actually think about what went wrong rather than blindly retry the
// same call. Kept as a constant so it can be detected (idempotency) and
// also so the wording stays consistent across the codebase.
const reflectionSuffix = "\n\n[Reflection required] Before retrying, identify exactly what went wrong and why. Try a different approach or different arguments rather than repeating the same call."

// builtinClassifyErrorHook fills in result.ErrorType for any error result
// that doesn't already have one. It runs ahead of the reflection suffix
// hook so classification looks at the raw error text, not the suffixed
// version.
func builtinClassifyErrorHook(_ context.Context, _ uctypes.HookContext, result *uctypes.AIToolResult) {
	if result == nil || result.ErrorText == "" {
		return
	}
	if result.ErrorType != "" {
		return
	}
	result.ErrorType = classifyToolError(result.ErrorText)
}

// builtinReflectionSuffixHook appends the reflection suffix to every error
// result. Idempotent: a result that already carries the suffix (e.g.
// produced by a custom hook that sets it explicitly) is left alone.
func builtinReflectionSuffixHook(_ context.Context, _ uctypes.HookContext, result *uctypes.AIToolResult) {
	if result == nil || result.ErrorText == "" {
		return
	}
	if strings.Contains(result.ErrorText, "[Reflection required]") {
		return
	}
	result.ErrorText = result.ErrorText + reflectionSuffix
}

// builtinSpillHook spills oversized tool result text to disk and replaces
// the inline result with a head+tail preview. Skipped on errors — error
// text is rarely large and the spill machinery's preview format would just
// muddy short failures.
func builtinSpillHook(_ context.Context, h uctypes.HookContext, result *uctypes.AIToolResult) {
	if result == nil || result.ErrorText != "" {
		return
	}
	chatId := ""
	if h.ChatOpts != nil {
		chatId = h.ChatOpts.ChatId
	}
	spillToolResultIfOversized(result, h.ToolDef, chatId)
}

// installBuiltinHooks prepends the built-in global hooks to the caller's
// AfterToolHooks slice. Order matters: classify (sees raw error) →
// reflection suffix (mutates error text) → spill (no-ops on errors, runs
// only on success).
//
// Prepending — not appending — means caller hooks run AFTER the built-ins
// and can observe the classified+suffixed error text. If a future use case
// needs the raw text, register a per-tool AfterHook (those run before
// global hooks).
func installBuiltinHooks(opts *uctypes.WaveChatOpts) {
	if opts == nil {
		return
	}
	builtins := []uctypes.AfterToolHook{
		builtinClassifyErrorHook,
		builtinReflectionSuffixHook,
		builtinSpillHook,
	}
	opts.AfterToolHooks = append(builtins, opts.AfterToolHooks...)
}
