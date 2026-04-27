// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package uctypes

import "context"

// AgentEventKind discriminates the variants of AgentEvent. Subscribers
// switch on this to dispatch.
type AgentEventKind string

const (
	// AgentEventKindAgentStart fires once at the top of RunAIChat after
	// built-in hooks are installed and before any LLM call. ChatId set.
	AgentEventKindAgentStart AgentEventKind = "agent_start"
	// AgentEventKindAgentEnd fires once at RunAIChat's exit, regardless of
	// success or failure. Metrics carry the final accumulated state. Wired
	// via defer so it fires on early returns too.
	AgentEventKindAgentEnd AgentEventKind = "agent_end"
	// AgentEventKindTurnStart fires at the top of each step iteration —
	// before runAIChatStep. StepNum reflects the upcoming request count.
	AgentEventKindTurnStart AgentEventKind = "turn_start"
	// AgentEventKindTurnEnd fires after the step has been classified, just
	// before the action dispatch decides whether to continue, escalate,
	// retry, or break. StopReason and Action are populated.
	AgentEventKindTurnEnd AgentEventKind = "turn_end"
	// AgentEventKindToolStart fires at the top of processToolCall, before
	// any hooks or callback have run. Args carry the raw input.
	AgentEventKindToolStart AgentEventKind = "tool_start"
	// AgentEventKindToolEnd fires at the bottom of processToolCall, after
	// hooks have shaped the final result. Result, IsError, ErrorType set.
	AgentEventKindToolEnd AgentEventKind = "tool_end"
)

// AgentEvent is the single bus event type. Different kinds populate
// different fields; consumers should only read fields documented for their
// kind. Kept as a flat struct rather than a tagged union so subscribers
// can be terse — Go's lack of union types makes the tagged-struct
// idiomatic.
//
// This bus is additive infrastructure: SSE (`sseHandler.AiMsg*`), audit
// (`metrics.AuditLog`), and telemetry (`metrics`) continue to fire as
// before. Sinks registered on WaveChatOpts.EventSinks see the same events
// in a structured form. Today no built-in subscriber exists; the bus is
// foundation for future audit/telemetry/UI consolidation.
type AgentEvent struct {
	Kind      AgentEventKind
	ChatId    string
	Timestamp int64 // Unix milliseconds

	// Turn-scoped fields. StepNum populated for TurnStart and TurnEnd.
	// StopReason populated for TurnEnd. Action is the LoopAction's string
	// form ("continue_with_tools", "escalate_max_tokens", etc.) — kept as
	// string to avoid uctypes ↔ aiusechat circular import.
	StepNum    int
	StopReason *WaveStopReason
	Action     string

	// Tool-scoped fields. ToolCallId / ToolName populated for ToolStart
	// and ToolEnd. Args populated for ToolStart (the raw input map).
	// Result / IsError / ErrorType populated for ToolEnd.
	ToolCallId string
	ToolName   string
	Args       any
	Result     *AIToolResult
	IsError    bool
	ErrorType  string

	// Agent-end field. Populated only for AgentEventKindAgentEnd.
	Metrics *AIMetrics
}

// AgentEventSink is the subscriber callback shape. Sinks must not block
// for long; emissions happen synchronously on the loop's hot path. For
// expensive work (network calls, file I/O), the sink should hand off to a
// goroutine. Sinks must not panic; the emitter does not recover.
//
// The context is the loop's request context — sinks should honor
// cancelation when doing async work.
type AgentEventSink func(ctx context.Context, event AgentEvent)
