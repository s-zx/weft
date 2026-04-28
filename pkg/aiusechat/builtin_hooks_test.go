// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package aiusechat

import (
	"context"
	"strings"
	"testing"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

// runBuiltinAfterChain runs the three global built-in AfterToolHooks
// in the same order RunAIChat installs them (classify → reflection →
// spill), so tests can verify the composed behavior without spinning
// up a full processToolCall.
func runBuiltinAfterChain(t *testing.T, h uctypes.HookContext, result *uctypes.AIToolResult) {
	t.Helper()
	ctx := context.Background()
	builtinClassifyErrorHook(ctx, h, result)
	builtinReflectionSuffixHook(ctx, h, result)
	builtinSpillHook(ctx, h, result)
}

// TestAfterHookChain_ValidationErrorGetsReflectionSuffix is the
// regression for the D-phase behavior change: BEFORE the hooks
// refactor, only ResolveToolCall errors got the reflection suffix.
// Validation errors (ToolVerifyInput failing, "Invalid Tool Call",
// approval denials) bypassed the suffix entirely. After the refactor
// they all flow through the same AfterHook chain — this test pins
// that contract.
func TestAfterHookChain_ValidationErrorGetsReflectionSuffix(t *testing.T) {
	hookCtx := uctypes.HookContext{
		ToolCall: uctypes.WaveToolCall{
			ID:   "call-1",
			Name: "edit_text_file",
		},
		ChatOpts: &uctypes.WaveChatOpts{ChatId: "chat-1"},
	}
	result := &uctypes.AIToolResult{
		ToolName:  "edit_text_file",
		ToolUseID: "call-1",
		// Simulates the kind of error message produced by
		// ToolVerifyInput / "Invalid Tool Call" / approval denial —
		// pre-refactor these never saw a reflection suffix.
		ErrorText: "Input validation failed: filename is required",
	}

	runBuiltinAfterChain(t, hookCtx, result)

	if !strings.Contains(result.ErrorText, "[Reflection required]") {
		t.Errorf("validation error should be suffixed with reflection prompt, got %q", result.ErrorText)
	}
	// Classify hook should also have populated ErrorType. "is required"
	// matches the validation arm of classifyToolError.
	if result.ErrorType != uctypes.ErrorTypeValidation {
		t.Errorf("expected ErrorType=validation, got %q", result.ErrorType)
	}
}

// TestAfterHookChain_DenialErrorClassifiedAsPermission proves the
// I2 fix: a "denied: ..." error message (produced when the
// permissions engine returns RuleDeny in CreateToolUseData) now
// classifies as ErrorTypePermission instead of ErrorTypeUnknown.
func TestAfterHookChain_DenialErrorClassifiedAsPermission(t *testing.T) {
	hookCtx := uctypes.HookContext{
		ToolCall: uctypes.WaveToolCall{ID: "call-1", Name: "shell_exec"},
		ChatOpts: &uctypes.WaveChatOpts{ChatId: "chat-1"},
	}
	result := &uctypes.AIToolResult{
		ToolName:  "shell_exec",
		ToolUseID: "call-1",
		ErrorText: "denied: shell_exec(prefix:sudo)",
	}

	runBuiltinAfterChain(t, hookCtx, result)

	if result.ErrorType != uctypes.ErrorTypePermission {
		t.Errorf("policy denial should classify as permission, got %q", result.ErrorType)
	}
	if !strings.Contains(result.ErrorText, "[Reflection required]") {
		t.Errorf("denial should also get reflection suffix, got %q", result.ErrorText)
	}
}

// TestAfterHookChain_SuccessNoReflectionSuffix: hooks must NOT touch
// successful results. Spill might mutate result.Text on oversized
// success but reflection/classify must leave it alone.
func TestAfterHookChain_SuccessNoReflectionSuffix(t *testing.T) {
	hookCtx := uctypes.HookContext{
		ToolCall: uctypes.WaveToolCall{ID: "call-1", Name: "read_text_file"},
		ChatOpts: &uctypes.WaveChatOpts{ChatId: "chat-1"},
	}
	result := &uctypes.AIToolResult{
		ToolName:  "read_text_file",
		ToolUseID: "call-1",
		Text:      "file contents",
	}

	runBuiltinAfterChain(t, hookCtx, result)

	if strings.Contains(result.Text, "[Reflection required]") {
		t.Errorf("success result should not be suffixed, got %q", result.Text)
	}
	if result.ErrorType != "" {
		t.Errorf("success result should have empty ErrorType, got %q", result.ErrorType)
	}
}

// TestAfterHookChain_ReflectionSuffixIdempotent: a result that
// already carries the suffix (e.g. set by a custom hook) should not
// be double-suffixed.
func TestAfterHookChain_ReflectionSuffixIdempotent(t *testing.T) {
	hookCtx := uctypes.HookContext{
		ToolCall: uctypes.WaveToolCall{ID: "call-1", Name: "shell_exec"},
		ChatOpts: &uctypes.WaveChatOpts{ChatId: "chat-1"},
	}
	original := "boom\n\n[Reflection required] already here"
	result := &uctypes.AIToolResult{
		ToolName:  "shell_exec",
		ToolUseID: "call-1",
		ErrorText: original,
	}

	runBuiltinAfterChain(t, hookCtx, result)

	if result.ErrorText != original {
		t.Errorf("idempotent suffix should leave error text unchanged, got %q", result.ErrorText)
	}
}
