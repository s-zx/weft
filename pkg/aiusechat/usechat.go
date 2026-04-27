// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package aiusechat

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/user"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/s-zx/crest/pkg/aiusechat/aiutil"
	"github.com/s-zx/crest/pkg/aiusechat/chatstore"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/secretstore"
	"github.com/s-zx/crest/pkg/telemetry"
	"github.com/s-zx/crest/pkg/telemetry/telemetrydata"
	"github.com/s-zx/crest/pkg/util/ds"
	"github.com/s-zx/crest/pkg/util/logutil"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"github.com/s-zx/crest/pkg/waveappstore"
	"github.com/s-zx/crest/pkg/wavebase"
	"github.com/s-zx/crest/pkg/waveobj"
	"github.com/s-zx/crest/pkg/web/sse"
	"github.com/s-zx/crest/pkg/wstore"
)

const DefaultAPI = uctypes.APIType_OpenAIResponses
const DefaultMaxTokens = 4 * 1024
const BuilderMaxTokens = 24 * 1024

var activeChats = ds.MakeSyncMap[bool]() // key is chatid

func getSystemPrompt(apiType string, model string, isBuilder bool, hasToolsCapability bool, widgetAccess bool) []string {
	if isBuilder {
		return []string{}
	}
	useNoToolsPrompt := !hasToolsCapability || !widgetAccess
	basePrompt := SystemPromptText_OpenAI
	if useNoToolsPrompt {
		basePrompt = SystemPromptText_NoTools
	}
	modelLower := strings.ToLower(model)
	needsStrictToolAddOn, _ := regexp.MatchString(`(?i)\b(mistral|o?llama|qwen|mixtral|yi|phi|deepseek)\b`, modelLower)
	if needsStrictToolAddOn && !useNoToolsPrompt {
		return []string{basePrompt, SystemPromptText_StrictToolAddOn}
	}
	return []string{basePrompt}
}

func isLocalEndpoint(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	endpointLower := strings.ToLower(endpoint)
	return strings.Contains(endpointLower, "localhost") || strings.Contains(endpointLower, "127.0.0.1")
}

func getWaveAISettings(premium bool, builderMode bool, rtInfo waveobj.ObjRTInfo, aiModeName string) (*uctypes.AIOptsType, error) {
	maxTokens := DefaultMaxTokens
	if builderMode {
		maxTokens = BuilderMaxTokens
	}
	if rtInfo.WaveAIMaxOutputTokens > 0 {
		maxTokens = rtInfo.WaveAIMaxOutputTokens
	}
	aiMode, config, err := resolveAIMode(aiModeName, premium)
	if err != nil {
		return nil, err
	}
	apiToken := config.APIToken
	if apiToken == "" && config.APITokenSecretName != "" {
		secret, exists, err := secretstore.GetSecret(config.APITokenSecretName)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve secret %s: %w", config.APITokenSecretName, err)
		}
		secret = strings.TrimSpace(secret)
		if !exists || secret == "" {
			return nil, fmt.Errorf("secret %s not found or empty", config.APITokenSecretName)
		}
		apiToken = secret
	}

	var baseUrl string
	if config.Endpoint != "" {
		baseUrl = config.Endpoint
	} else {
		return nil, fmt.Errorf("no ai:endpoint configured for AI mode %s", aiMode)
	}

	thinkingLevel := config.ThinkingLevel
	if thinkingLevel == "" {
		thinkingLevel = uctypes.ThinkingLevelMedium
	}
	verbosity := config.Verbosity
	if verbosity == "" {
		verbosity = uctypes.VerbosityLevelMedium // default to medium
	}
	opts := &uctypes.AIOptsType{
		Provider:      config.Provider,
		APIType:       config.APIType,
		Model:         config.Model,
		MaxTokens:     maxTokens,
		ThinkingLevel: thinkingLevel,
		Verbosity:     verbosity,
		AIMode:        aiMode,
		Endpoint:      baseUrl,
		ProxyURL:      config.ProxyURL,
		Capabilities:  config.Capabilities,
	}
	if apiToken != "" {
		opts.APIToken = apiToken
	}
	return opts, nil
}

func shouldUseChatCompletionsAPI(model string) bool {
	m := strings.ToLower(model)
	// Chat Completions API is required for older models: gpt-3.5-*, gpt-4, gpt-4-turbo, o1-*
	return strings.HasPrefix(m, "gpt-3.5") ||
		strings.HasPrefix(m, "gpt-4-") ||
		m == "gpt-4" ||
		strings.HasPrefix(m, "o1-")
}

// GetWaveAISettings is the exported entrypoint used by pkg/agent. It wraps
// the private getWaveAISettings helper with the standard premium-detection
// and non-builder defaults so external packages don't need to know about
// those knobs.
func GetWaveAISettings(rtInfo waveobj.ObjRTInfo, aiModeName string) (*uctypes.AIOptsType, error) {
	return getWaveAISettings(false, false, rtInfo, aiModeName)
}

func runAIChatStep(ctx context.Context, sseHandler *sse.SSEHandlerCh, backend UseChatBackend, chatOpts uctypes.WaveChatOpts, cont *uctypes.WaveContinueResponse) (*uctypes.WaveStopReason, []uctypes.GenAIMessage, error) {
	if chatOpts.Config.APIType == uctypes.APIType_OpenAIResponses && shouldUseChatCompletionsAPI(chatOpts.Config.Model) {
		return nil, nil, fmt.Errorf("Chat completions API not available (must use newer OpenAI models)")
	}
	stopReason, messages, err := backend.RunChatStep(ctx, sseHandler, chatOpts, cont)
	return stopReason, messages, err
}

// emitAgentEvent fires an AgentEvent to every sink registered on
// chatOpts.EventSinks. Synchronous; sinks are responsible for not blocking.
// No-op if there are no sinks (the common case today). Stamps Timestamp and
// ChatId so callers don't have to.
func emitAgentEvent(ctx context.Context, chatOpts *uctypes.WaveChatOpts, ev uctypes.AgentEvent) {
	if chatOpts == nil || len(chatOpts.EventSinks) == 0 {
		return
	}
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}
	if ev.ChatId == "" {
		ev.ChatId = chatOpts.ChatId
	}
	for _, sink := range chatOpts.EventSinks {
		sink(ctx, ev)
	}
}


// String returns the event-bus form of a LoopAction. AgentEvent.Action
// is a string field (uctypes can't import aiusechat for circular-dep
// reasons), so subscribers consume this serialization.
func (a LoopAction) String() string {
	switch a {
	case LoopActionFinish:
		return "finish"
	case LoopActionContinueWithTools:
		return "continue_with_tools"
	case LoopActionEscalateMaxTokens:
		return "escalate_max_tokens"
	case LoopActionResumeAfterMaxTokens:
		return "resume_after_max_tokens"
	case LoopActionReactiveCompact:
		return "reactive_compact"
	case LoopActionFailFirstStep:
		return "fail_first_step"
	case LoopActionFailFatal:
		return "fail_fatal"
	case LoopActionFailMaxTokens:
		return "fail_max_tokens"
	default:
		return "unknown"
	}
}

// LoopAction is the verdict returned by classifyTerminalState. Each action
// names exactly one branch of RunAIChat's outer loop. The classifier is
// pure: it inspects (stopReason, err, counters) and returns an Action;
// side effects (counter mutation, SSE emission, system-prompt nudges,
// chatstore writes) are the dispatcher's job. Pi calls this "errors are
// data, not control flow."
type LoopAction int

const (
	LoopActionUnknown LoopAction = iota
	// LoopActionFinish: step completed cleanly. Run post-step bookkeeping
	// (post messages, compaction tiers, pending-todos nudge), then break.
	LoopActionFinish
	// LoopActionContinueWithTools: stopReason carries tool calls. Run them,
	// set cont, and loop.
	LoopActionContinueWithTools
	// LoopActionEscalateMaxTokens: model hit max-output-tokens cap; this is
	// the first such hit and the cap is small enough to double. Bump
	// chatOpts.Config.MaxTokens, set cont, and loop.
	LoopActionEscalateMaxTokens
	// LoopActionResumeAfterMaxTokens: subsequent max-tokens hit (or cap was
	// already large). Append a "resume directly" system note, set cont, loop.
	// Capped at 3 attempts.
	LoopActionResumeAfterMaxTokens
	// LoopActionReactiveCompact: provider rejected the request because the
	// context exceeded its window. One-shot fallback: aggressively compact
	// and retry the same step. Skips post-step bookkeeping.
	LoopActionReactiveCompact
	// LoopActionFailFirstStep: step errored on the very first turn — surface
	// it as the function's error return so the caller knows the chat never
	// got off the ground. Skips post-step bookkeeping.
	LoopActionFailFirstStep
	// LoopActionFailFatal: non-recoverable error mid-conversation. Surface
	// via SSE and break. Skips post-step bookkeeping (rtnMessages may be nil
	// or partial; we don't risk posting them).
	LoopActionFailFatal
	// LoopActionFailMaxTokens: max-tokens loop gave up after escalation +
	// resume attempts. Post the partial output (different from FailFatal),
	// then surface a max-tokens-specific error and break.
	LoopActionFailMaxTokens
)

// loopCounters captures the per-turn state classifyTerminalState needs to
// pick the right Action. Kept as a value type so the classifier is pure.
type loopCounters struct {
	firstStep                bool
	maxTokensEscalated       bool
	maxTokensResumeAttempts  int
	reactiveCompactAttempted bool
	contextBudget            int
	currentMaxTokens         int
}

// classifyTerminalState maps the outcome of one runAIChatStep call onto a
// LoopAction. It is pure — no logging, no SSE, no counter mutation.
//
// Decision order:
//  1. err != nil takes precedence over stopReason. A context-length error
//     gets one shot at reactive compaction; everything else fails (first
//     step → returned err, otherwise fatal SSE).
//  2. nil stopReason without err is treated as a clean finish. (Backends
//     don't return this today, but it's the safest default.)
//  3. StopKindToolUse → ContinueWithTools.
//  4. StopKindMaxTokens → escalate cap, then resume up to 3x, then give up.
//  5. Everything else (StopKindError, Canceled, RateLimit, Content,
//     PauseTurn, StepBudget, Done) → Finish. Backends already emit
//     AiMsgError on the StopKindError path, so the loop just lets the
//     pending-todos nudge run and breaks.
func classifyTerminalState(stopReason *uctypes.WaveStopReason, err error, c loopCounters) LoopAction {
	if err != nil {
		if isContextLengthError(err) && !c.reactiveCompactAttempted && c.contextBudget > 0 {
			return LoopActionReactiveCompact
		}
		if c.firstStep {
			return LoopActionFailFirstStep
		}
		return LoopActionFailFatal
	}
	if stopReason == nil {
		return LoopActionFinish
	}
	switch stopReason.Kind {
	case uctypes.StopKindToolUse:
		return LoopActionContinueWithTools
	case uctypes.StopKindMaxTokens:
		if !c.maxTokensEscalated && c.currentMaxTokens < 32768 {
			return LoopActionEscalateMaxTokens
		}
		if c.maxTokensResumeAttempts < 3 {
			return LoopActionResumeAfterMaxTokens
		}
		return LoopActionFailMaxTokens
	default:
		return LoopActionFinish
	}
}

func getUsage(msgs []uctypes.GenAIMessage) uctypes.AIUsage {
	var rtn uctypes.AIUsage
	var found bool
	for _, msg := range msgs {
		if usage := msg.GetUsage(); usage != nil {
			if !found {
				rtn = *usage
				found = true
			} else {
				rtn.InputTokens += usage.InputTokens
				rtn.OutputTokens += usage.OutputTokens
				rtn.NativeWebSearchCount += usage.NativeWebSearchCount
			}
		}
	}
	return rtn
}

func GetChatUsage(chat *uctypes.AIChat) uctypes.AIUsage {
	usage := getUsage(chat.NativeMessages)
	usage.APIType = chat.APIType
	usage.Model = chat.Model
	return usage
}

func updateToolUseDataInChat(backend UseChatBackend, chatOpts uctypes.WaveChatOpts, toolCallID string, toolUseData uctypes.UIMessageDataToolUse) {
	if err := backend.UpdateToolUseData(chatOpts.ChatId, toolCallID, toolUseData); err != nil {
		log.Printf("failed to update tool use data in chat: %v\n", err)
	}
}

func processToolCallInternal(backend UseChatBackend, toolCall uctypes.WaveToolCall, chatOpts uctypes.WaveChatOpts, toolDef *uctypes.ToolDefinition, sseHandler *sse.SSEHandlerCh) uctypes.AIToolResult {
	hookCtx := uctypes.HookContext{
		ToolCall: toolCall,
		ToolDef:  toolDef,
		ChatOpts: &chatOpts,
	}
	bgCtx := sseHandler.Context()

	// finalize is the single tail every return path goes through. It runs
	// per-tool then global AfterHooks so classification + reflection-suffix
	// + spill apply uniformly to validation errors, approval denials, and
	// happy-path results alike. Before this refactor, only ResolveToolCall
	// errors got the suffix; the early-return paths bypassed it.
	finalize := func(result uctypes.AIToolResult) uctypes.AIToolResult {
		if toolDef != nil {
			for _, hook := range toolDef.AfterHooks {
				hook(bgCtx, hookCtx, &result)
			}
		}
		for _, hook := range chatOpts.AfterToolHooks {
			hook(bgCtx, hookCtx, &result)
		}
		return result
	}

	if toolCall.ToolUseData == nil {
		return finalize(uctypes.AIToolResult{
			ToolName:  toolCall.Name,
			ToolUseID: toolCall.ID,
			ErrorText: "Invalid Tool Call",
		})
	}

	if toolCall.ToolUseData.Status == uctypes.ToolUseStatusError {
		errorMsg := toolCall.ToolUseData.ErrorMessage
		if errorMsg == "" {
			errorMsg = "Unspecified Tool Error"
		}
		return finalize(uctypes.AIToolResult{
			ToolName:  toolCall.Name,
			ToolUseID: toolCall.ID,
			ErrorText: errorMsg,
		})
	}

	if toolDef != nil && toolDef.ToolVerifyInput != nil {
		if err := toolDef.ToolVerifyInput(toolCall.Input, toolCall.ToolUseData); err != nil {
			errorMsg := fmt.Sprintf("Input validation failed: %v", err)
			toolCall.ToolUseData.Status = uctypes.ToolUseStatusError
			toolCall.ToolUseData.ErrorMessage = errorMsg
			return finalize(uctypes.AIToolResult{
				ToolName:  toolCall.Name,
				ToolUseID: toolCall.ID,
				ErrorText: errorMsg,
			})
		}
		// ToolVerifyInput can modify the toolusedata.  re-send it here.
		_ = sseHandler.AiMsgData("data-tooluse", toolCall.ID, *toolCall.ToolUseData)
		updateToolUseDataInChat(backend, chatOpts, toolCall.ID, *toolCall.ToolUseData)
	}

	if toolCall.ToolUseData.Approval == uctypes.ApprovalNeedsApproval {
		log.Printf("  waiting for approval...\n")
		approval, err := WaitForToolApproval(sseHandler.Context(), toolCall.ID)
		if err != nil || approval == "" {
			approval = uctypes.ApprovalCanceled
		}
		log.Printf("  approval result: %q\n", approval)
		toolCall.ToolUseData.Approval = approval

		if !toolCall.ToolUseData.IsApproved() {
			errorMsg := "Tool use denied or timed out"
			if approval == uctypes.ApprovalUserDenied {
				errorMsg = "Tool use denied by user"
			} else if approval == uctypes.ApprovalTimeout {
				errorMsg = "Tool approval timed out"
			} else if approval == uctypes.ApprovalCanceled {
				errorMsg = "Tool approval canceled"
			}
			toolCall.ToolUseData.Status = uctypes.ToolUseStatusError
			toolCall.ToolUseData.ErrorMessage = errorMsg
			return finalize(uctypes.AIToolResult{
				ToolName:  toolCall.Name,
				ToolUseID: toolCall.ID,
				ErrorText: errorMsg,
			})
		}

		// this still happens here because we need to update the FE to say the tool call was approved
		_ = sseHandler.AiMsgData("data-tooluse", toolCall.ID, *toolCall.ToolUseData)
		updateToolUseDataInChat(backend, chatOpts, toolCall.ID, *toolCall.ToolUseData)
	}

	toolCall.ToolUseData.RunTs = time.Now().UnixMilli()

	// BeforeHooks: per-tool first (close to the tool's contract — e.g. mtime
	// check on file writers), then global (loop-level concerns — none today,
	// but Permissions v2 will land here). Any non-nil return short-circuits
	// execution with that result; finalize() still runs AfterHooks so
	// classification + reflection-suffix apply uniformly.
	var result uctypes.AIToolResult
	shortCircuited := false
	if toolDef != nil {
		for _, hook := range toolDef.BeforeHooks {
			if r := hook(bgCtx, hookCtx); r != nil {
				result = *r
				shortCircuited = true
				break
			}
		}
	}
	if !shortCircuited {
		for _, hook := range chatOpts.BeforeToolHooks {
			if r := hook(bgCtx, hookCtx); r != nil {
				result = *r
				shortCircuited = true
				break
			}
		}
	}
	if !shortCircuited {
		result = ResolveToolCall(toolDef, toolCall, chatOpts)
	}

	// Update toolUseData status from the (un-suffixed) result — AfterHooks
	// may mutate result.ErrorText (reflection suffix), but the UI should
	// show the original error message.
	if result.ErrorText != "" {
		toolCall.ToolUseData.Status = uctypes.ToolUseStatusError
		toolCall.ToolUseData.ErrorMessage = result.ErrorText
	} else {
		toolCall.ToolUseData.Status = uctypes.ToolUseStatusCompleted
	}

	return finalize(result)
}

func processToolCall(backend UseChatBackend, toolCall uctypes.WaveToolCall, chatOpts uctypes.WaveChatOpts, sseHandler *sse.SSEHandlerCh) uctypes.ToolCallOutcome {
	inputJSON, _ := json.Marshal(toolCall.Input)
	logutil.DevPrintf("TOOLUSE name=%s id=%s input=%s approval=%q\n", toolCall.Name, toolCall.ID, utilfn.TruncateString(string(inputJSON), 500), toolCall.ToolUseData.Approval)

	approval := ""
	if toolCall.ToolUseData != nil {
		approval = toolCall.ToolUseData.Approval
	}
	startTs := time.Now()

	emitAgentEvent(sseHandler.Context(), &chatOpts, uctypes.AgentEvent{
		Kind:       uctypes.AgentEventKindToolStart,
		ToolCallId: toolCall.ID,
		ToolName:   toolCall.Name,
		Args:       toolCall.Input,
	})

	toolDef := chatOpts.GetToolDefinition(toolCall.Name)
	result := processToolCallInternal(backend, toolCall, chatOpts, toolDef, sseHandler)
	// Spill / classify / reflection-suffix now run as built-in AfterToolHooks
	// installed by RunAIChat, so processToolCallInternal already returns a
	// fully-finalized result here.

	durationMs := time.Since(startTs).Milliseconds()

	isError := result.ErrorText != ""
	if isError {
		log.Printf("  error=%s\n", result.ErrorText)
	} else {
		log.Printf("  result=%s\n", utilfn.TruncateString(result.Text, 500))
	}

	toolLogName := ""
	if toolDef != nil && toolDef.ToolLogName != "" {
		toolLogName = toolDef.ToolLogName
	}

	outcomeStr := "success"
	if isError {
		outcomeStr = "error"
	}

	if toolCall.ToolUseData != nil {
		_ = sseHandler.AiMsgData("data-tooluse", toolCall.ID, *toolCall.ToolUseData)
		updateToolUseDataInChat(backend, chatOpts, toolCall.ID, *toolCall.ToolUseData)
	}

	var fileChanged, fileBackup string
	var fileIsNew bool
	if toolCall.ToolUseData != nil && !isError {
		fileChanged = toolCall.ToolUseData.InputFileName
		fileBackup = toolCall.ToolUseData.WriteBackupFileName
		if fileChanged != "" && fileBackup == "" {
			fileIsNew = true
		}
	}

	emitAgentEvent(sseHandler.Context(), &chatOpts, uctypes.AgentEvent{
		Kind:       uctypes.AgentEventKindToolEnd,
		ToolCallId: toolCall.ID,
		ToolName:   toolCall.Name,
		Result:     &result,
		IsError:    isError,
		ErrorType:  result.ErrorType,
	})

	return uctypes.ToolCallOutcome{
		Result: result,
		Audit: uctypes.ToolAuditEvent{
			Timestamp:  startTs.UnixMilli(),
			ChatId:     chatOpts.ChatId,
			ToolName:   toolCall.Name,
			ToolCallId: toolCall.ID,
			InputArgs:  utilfn.TruncateString(string(inputJSON), 200),
			Approval:   approval,
			DurationMs: durationMs,
			Outcome:    outcomeStr,
			ErrorText:  result.ErrorText,
			ErrorType:  result.ErrorType,
		},
		IsError:     isError,
		ToolLogName: toolLogName,
		FileChanged: fileChanged,
		FileBackup:  fileBackup,
		FileIsNew:   fileIsNew,
	}
}

func applyOutcome(metrics *uctypes.AIMetrics, outcome uctypes.ToolCallOutcome, chatOpts uctypes.WaveChatOpts) {
	if outcome.IsError {
		metrics.ToolUseErrorCount++
	}
	if outcome.ToolLogName != "" {
		metrics.ToolDetail[outcome.ToolLogName]++
	}
	metrics.AuditLog = append(metrics.AuditLog, outcome.Audit)
	if outcome.FileChanged != "" && chatOpts.FileChangeCallback != nil {
		chatOpts.FileChangeCallback(outcome.FileChanged, outcome.FileBackup, outcome.FileIsNew)
	}
}

func processAllToolCalls(backend UseChatBackend, stopReason *uctypes.WaveStopReason, chatOpts uctypes.WaveChatOpts, sseHandler *sse.SSEHandlerCh, metrics *uctypes.AIMetrics) []uctypes.AIToolResult {
	// Create and send all data-tooluse packets at the beginning
	for i := range stopReason.ToolCalls {
		toolCall := &stopReason.ToolCalls[i]
		// Create toolUseData from the tool call input
		var argsJSON string
		if toolCall.Input != nil {
			argsBytes, err := json.Marshal(toolCall.Input)
			if err == nil {
				argsJSON = string(argsBytes)
			}
		}
		toolUseData := aiutil.CreateToolUseData(toolCall.ID, toolCall.Name, argsJSON, chatOpts)
		stopReason.ToolCalls[i].ToolUseData = &toolUseData
		log.Printf("AI data-tooluse %s\n", toolCall.ID)
		_ = sseHandler.AiMsgData("data-tooluse", toolCall.ID, toolUseData)
		updateToolUseDataInChat(backend, chatOpts, toolCall.ID, toolUseData)
		if toolUseData.Approval == uctypes.ApprovalNeedsApproval {
			RegisterToolApproval(toolCall.ID, sseHandler)
		}
	}
	allParallel := len(stopReason.ToolCalls) > 1
	if allParallel {
		for _, tc := range stopReason.ToolCalls {
			toolDef := chatOpts.GetToolDefinition(tc.Name)
			if toolDef == nil || !toolDef.Parallel {
				allParallel = false
				break
			}
			if tc.ToolUseData != nil && tc.ToolUseData.Approval == uctypes.ApprovalNeedsApproval {
				allParallel = false
				break
			}
		}
	}

	var toolResults []uctypes.AIToolResult
	if allParallel {
		outcomes := make([]uctypes.ToolCallOutcome, len(stopReason.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range stopReason.ToolCalls {
			wg.Add(1)
			go func(idx int, toolCall uctypes.WaveToolCall) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						log.Printf("panic in parallel tool goroutine for %s: %v\n", toolCall.Name, r)
						outcomes[idx] = uctypes.ToolCallOutcome{
							Result: uctypes.AIToolResult{
								ToolName:  toolCall.Name,
								ToolUseID: toolCall.ID,
								ErrorText: fmt.Sprintf("panic in parallel tool execution: %v", r),
								ErrorType: uctypes.ErrorTypePanic,
							},
							IsError: true,
							Audit: uctypes.ToolAuditEvent{
								Timestamp:  time.Now().UnixMilli(),
								ChatId:     chatOpts.ChatId,
								ToolName:   toolCall.Name,
								ToolCallId: toolCall.ID,
								Outcome:    "error",
								ErrorText:  fmt.Sprintf("panic: %v", r),
								ErrorType:  uctypes.ErrorTypePanic,
							},
						}
					}
				}()
				if ctxErr := sseHandler.Err(); ctxErr != nil {
					outcomes[idx] = uctypes.ToolCallOutcome{
						Result: uctypes.AIToolResult{
							ToolName:  toolCall.Name,
							ToolUseID: toolCall.ID,
							ErrorText: fmt.Sprintf("canceled before tool execution: %v", ctxErr),
							ErrorType: uctypes.ErrorTypeCanceled,
						},
						IsError: true,
						Audit: uctypes.ToolAuditEvent{
							Timestamp:  time.Now().UnixMilli(),
							ChatId:     chatOpts.ChatId,
							ToolName:   toolCall.Name,
							ToolCallId: toolCall.ID,
							Outcome:    "canceled",
							ErrorText:  ctxErr.Error(),
							ErrorType:  uctypes.ErrorTypeCanceled,
						},
					}
					return
				}
				outcomes[idx] = processToolCall(backend, toolCall, chatOpts, sseHandler)
			}(i, tc)
		}
		wg.Wait()
		for _, outcome := range outcomes {
			toolResults = append(toolResults, outcome.Result)
			applyOutcome(metrics, outcome, chatOpts)
		}
	} else {
		for _, toolCall := range stopReason.ToolCalls {
			if sseHandler.Err() != nil {
				log.Printf("AI tool processing stopped: %v\n", sseHandler.Err())
				break
			}
			outcome := processToolCall(backend, toolCall, chatOpts, sseHandler)
			toolResults = append(toolResults, outcome.Result)
			applyOutcome(metrics, outcome, chatOpts)
		}
	}

	// Cleanup: unregister approvals, remove incomplete/canceled tool calls, and filter results
	var filteredResults []uctypes.AIToolResult
	for i, toolCall := range stopReason.ToolCalls {
		UnregisterToolApproval(toolCall.ID)
		hasResult := i < len(toolResults)
		shouldRemove := !hasResult || (toolCall.ToolUseData != nil && toolCall.ToolUseData.Approval == uctypes.ApprovalCanceled)
		if shouldRemove {
			backend.RemoveToolUseCall(chatOpts.ChatId, toolCall.ID)
		} else if hasResult {
			filteredResults = append(filteredResults, toolResults[i])
		}
	}

	if len(filteredResults) > 0 {
		toolResultMsgs, err := backend.ConvertToolResultsToNativeChatMessage(filteredResults)
		if err != nil {
			log.Printf("Failed to convert tool results to native chat messages: %v", err)
		} else {
			for _, msg := range toolResultMsgs {
				if err := chatstore.DefaultChatStore.PostMessage(chatOpts.ChatId, &chatOpts.Config, msg); err != nil {
					log.Printf("Failed to post tool result message: %v", err)
				}
			}
		}
	}
	return filteredResults
}

func extractCmdName(input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	cmd, ok := m["cmd"].(string)
	if !ok || cmd == "" {
		return ""
	}
	cmd = strings.TrimSpace(cmd)
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	name := fields[0]
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

// extractTouchedFilesFromAudit walks the audit log for write_text_file,
// edit_text_file, and multi_edit calls, pulling the "filename" field out
// of each call's InputArgs (which is a truncated JSON string). Used to
// enrich the heavy-tier compaction summary so the model retains a list
// of files it has worked on across compaction. Best-effort: malformed
// JSON, missing fields, or non-file tools are silently skipped.
//
// Returns deduped paths in first-seen order, capped at maxFiles to keep
// the summary readable.
func extractTouchedFilesFromAudit(audit []uctypes.ToolAuditEvent, maxFiles int) []string {
	if len(audit) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var ordered []string
	for _, ev := range audit {
		switch ev.ToolName {
		case "write_text_file", "edit_text_file", "multi_edit":
		default:
			continue
		}
		// InputArgs is JSON, possibly truncated to 200 chars. Try to parse
		// just the "filename" field. If truncation cut us off mid-JSON,
		// fall back to a regex-style scan for "filename":"..." up to the
		// next quote.
		var probe struct {
			Filename string `json:"filename"`
		}
		if json.Unmarshal([]byte(ev.InputArgs), &probe) == nil && probe.Filename != "" {
			if !seen[probe.Filename] {
				seen[probe.Filename] = true
				ordered = append(ordered, probe.Filename)
			}
			continue
		}
		if fn := scanFilenameFromTruncated(ev.InputArgs); fn != "" && !seen[fn] {
			seen[fn] = true
			ordered = append(ordered, fn)
		}
	}
	if maxFiles > 0 && len(ordered) > maxFiles {
		ordered = ordered[:maxFiles]
	}
	return ordered
}

// scanFilenameFromTruncated handles the case where audit InputArgs got
// truncated mid-JSON. Looks for the literal `"filename":"` and returns
// the substring up to the next unescaped `"`. Cheap recovery for the
// common case — gives up if the value itself was cut off.
func scanFilenameFromTruncated(s string) string {
	const key = `"filename":"`
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	rest := s[i+len(key):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// classifyToolError maps a tool's error text to a coarse ErrorType for
// telemetry and loop-level decisions. The match is keyword-based because
// our tools surface plain `error` values rather than typed errors —
// switching to typed errors across every tool callback would be a much
// bigger refactor for a telemetry-only payoff. Order matters: more
// specific patterns first (panic, canceled, stale-file) before broad
// substring tests (permission, timeout, not-found).
func classifyToolError(errText string) string {
	if errText == "" {
		return ""
	}
	low := strings.ToLower(errText)
	switch {
	case strings.Contains(low, "panic in tool"):
		return uctypes.ErrorTypePanic
	case strings.Contains(low, "canceled before tool execution"),
		strings.Contains(low, "context canceled"),
		strings.Contains(low, "context deadline exceeded"):
		return uctypes.ErrorTypeCanceled
	case strings.Contains(low, "modified externally since you last read"):
		return uctypes.ErrorTypeStaleFile
	case strings.HasPrefix(low, "denied:"),
		strings.Contains(low, "permission denied"),
		strings.Contains(low, "access denied"),
		strings.Contains(low, "operation not permitted"),
		strings.Contains(low, "not authorized"):
		return uctypes.ErrorTypePermission
	case strings.Contains(low, "timeout"),
		strings.Contains(low, "timed out"),
		strings.Contains(low, "deadline exceeded"):
		return uctypes.ErrorTypeTimeout
	case strings.Contains(low, "no such file"),
		strings.Contains(low, "not found"),
		strings.Contains(low, "does not exist"),
		strings.Contains(low, "file not found"):
		return uctypes.ErrorTypeNotFound
	case strings.Contains(low, "invalid"),
		strings.Contains(low, "must be"),
		strings.Contains(low, "is required"),
		strings.Contains(low, "schema"),
		strings.Contains(low, "missing"):
		return uctypes.ErrorTypeValidation
	default:
		return uctypes.ErrorTypeUnknown
	}
}

// isContextLengthError matches error strings from providers that mean
// "your prompt exceeded the model's context window". We don't have a
// structured error type for this — it comes through as a plain error
// from the streaming POST or mid-stream. The match is intentionally
// broad; a false positive only triggers one extra summarize+retry.
func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context length") ||
		strings.Contains(msg, "context_length") ||
		strings.Contains(msg, "context window") ||
		strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "prompt too long") ||
		strings.Contains(msg, "maximum context") ||
		strings.Contains(msg, "max_tokens_to_keep") ||
		strings.Contains(msg, "request too large") ||
		strings.Contains(msg, "too many tokens") ||
		strings.Contains(msg, "413")
}

func detectDoomLoop(sigs []string, threshold int) bool {
	counts := make(map[string]int)
	for _, sig := range sigs {
		counts[sig]++
		if counts[sig] >= threshold {
			return true
		}
	}
	return false
}

func RunAIChat(ctx context.Context, sseHandler *sse.SSEHandlerCh, backend UseChatBackend, chatOpts uctypes.WaveChatOpts) (*uctypes.AIMetrics, error) {
	if !activeChats.SetUnless(chatOpts.ChatId, true) {
		return nil, fmt.Errorf("chat %s is already running", chatOpts.ChatId)
	}
	defer activeChats.Delete(chatOpts.ChatId)

	installBuiltinHooks(&chatOpts)

	emitAgentEvent(ctx, &chatOpts, uctypes.AgentEvent{Kind: uctypes.AgentEventKindAgentStart})

	stepNum := chatstore.DefaultChatStore.CountUserMessages(chatOpts.ChatId)
	aiProvider := chatOpts.Config.Provider
	if aiProvider == "" {
		aiProvider = uctypes.AIProvider_Custom
	}
	isLocal := isLocalEndpoint(chatOpts.Config.Endpoint)
	metrics := &uctypes.AIMetrics{
		ChatId:  chatOpts.ChatId,
		StepNum: stepNum,
		Usage: uctypes.AIUsage{
			APIType: chatOpts.Config.APIType,
			Model:   chatOpts.Config.Model,
		},
		WidgetAccess:  chatOpts.WidgetAccess,
		ToolDetail:    make(map[string]int),
		ThinkingLevel: chatOpts.Config.ThinkingLevel,
		AIMode:        chatOpts.Config.AIMode,
		AIProvider:    aiProvider,
		IsLocal:       isLocal,
	}
	defer func() {
		// AgentEnd fires on every exit (normal completion, FailFirstStep
		// early return, panic propagation). Closure captures the metrics
		// pointer so subscribers see the final accumulated state.
		emitAgentEvent(ctx, &chatOpts, uctypes.AgentEvent{
			Kind:    uctypes.AgentEventKindAgentEnd,
			Metrics: metrics,
		})
	}()
	firstStep := true
	stepBudgetWarned := false
	doomLoopWarned := false
	pendingTodosNudged := false
	unavailableCmdsLastCount := 0
	unavailableCmds := make(map[string]bool)
	hasProducedOutput := false
	outputNudged := false
	maxTokensEscalated := false
	maxTokensResumeAttempts := 0
	reactiveCompactAttempted := false
	var lastInputTokens int
	var cont *uctypes.WaveContinueResponse
	var recentToolSigs []string
stepLoop:
	for {
		emitAgentEvent(ctx, &chatOpts, uctypes.AgentEvent{
			Kind:    uctypes.AgentEventKindTurnStart,
			StepNum: metrics.RequestCount,
		})
		if chatOpts.TabStateGenerator != nil {
			tabState, tabTools, tabId, tabErr := chatOpts.TabStateGenerator()
			if tabErr == nil {
				chatOpts.TabState = tabState
				chatOpts.TabTools = tabTools
				chatOpts.TabId = tabId
			}
		}
		if chatOpts.BuilderAppGenerator != nil {
			appGoFile, appStaticFiles, platformInfo, appErr := chatOpts.BuilderAppGenerator()
			if appErr == nil {
				chatOpts.AppGoFile = appGoFile
				chatOpts.AppStaticFiles = appStaticFiles
				chatOpts.PlatformInfo = platformInfo
			}
		}
		if chatOpts.MaxSteps > 0 && metrics.RequestCount >= chatOpts.MaxSteps {
			_ = sseHandler.AiMsgError(fmt.Sprintf("Step budget exhausted (%d/%d steps)", metrics.RequestCount, chatOpts.MaxSteps))
			_ = sseHandler.AiMsgFinish("step_budget", nil)
			metrics.HadError = true
			break
		}
		if chatOpts.MaxSteps > 0 && !stepBudgetWarned {
			remaining := chatOpts.MaxSteps - metrics.RequestCount
			warningAt := max(chatOpts.MaxSteps/5, 1)
			if remaining <= warningAt {
				chatOpts.SystemPrompt = append(chatOpts.SystemPrompt,
					fmt.Sprintf("IMPORTANT: You have %d steps remaining out of %d total. Begin wrapping up your current task.", remaining, chatOpts.MaxSteps))
				stepBudgetWarned = true
			}
		}
		if chatOpts.MaxSteps > 0 && !hasProducedOutput && !outputNudged && metrics.RequestCount > chatOpts.MaxSteps*2/5 {
			outputNudged = true
			chatOpts.SystemPrompt = append(chatOpts.SystemPrompt,
				fmt.Sprintf("URGENT: You have used %d of %d steps without writing any output files. Stop researching and start building your solution NOW. Use write_text_file to create your initial implementation immediately.", metrics.RequestCount, chatOpts.MaxSteps))
			log.Printf("output nudge: %d steps without file writes\n", metrics.RequestCount)
		}
		stopReason, rtnMessages, err := runAIChatStep(ctx, sseHandler, backend, chatOpts, cont)
		metrics.RequestCount++
		if stopReason != nil {
			logutil.DevPrintf("stopreason: %s (%s) (%s) (%s)\n", stopReason.Kind, stopReason.ErrorText, stopReason.ErrorType, stopReason.RawReason)
		}
		if len(rtnMessages) > 0 {
			usage := getUsage(rtnMessages)
			lastInputTokens = usage.InputTokens
			log.Printf("usage: input=%d output=%d websearch=%d\n", usage.InputTokens, usage.OutputTokens, usage.NativeWebSearchCount)
			metrics.Usage.InputTokens += usage.InputTokens
			metrics.Usage.OutputTokens += usage.OutputTokens
			_ = sseHandler.AiMsgData("data-usage", "usage", map[string]int{
				"inputtokens":  metrics.Usage.InputTokens,
				"outputtokens": metrics.Usage.OutputTokens,
				"steps":        metrics.RequestCount,
			})
			metrics.Usage.NativeWebSearchCount += usage.NativeWebSearchCount
			if usage.Model != "" && metrics.Usage.Model != usage.Model {
				metrics.Usage.Model = "mixed"
			}
		}
		action := classifyTerminalState(stopReason, err, loopCounters{
			firstStep:                firstStep,
			maxTokensEscalated:       maxTokensEscalated,
			maxTokensResumeAttempts:  maxTokensResumeAttempts,
			reactiveCompactAttempted: reactiveCompactAttempted,
			contextBudget:            chatOpts.ContextBudget,
			currentMaxTokens:         chatOpts.Config.MaxTokens,
		})

		emitAgentEvent(ctx, &chatOpts, uctypes.AgentEvent{
			Kind:       uctypes.AgentEventKindTurnEnd,
			StepNum:    metrics.RequestCount,
			StopReason: stopReason,
			Action:     action.String(),
		})

		// Early-out dispatch: actions that bypass post-step bookkeeping
		// (post messages, compaction tiers, firstStep flag flip).
		switch action {
		case LoopActionReactiveCompact:
			// Last-ditch recovery: provider rejected the request because the
			// context exceeded its window. Aggressively summarize and retry once.
			// Single-shot — if the retry also blows up the next classify() call
			// will return FailFatal because reactiveCompactAttempted is set.
			reactiveCompactAttempted = true
			summary, removed := chatstore.DefaultChatStore.CompactMessagesWithSummary(chatOpts.ChatId, 1, 5)
			log.Printf("reactive compact on context-length error: removed=%d, error=%v\n", removed, err)
			if removed > 0 {
				if summary != "" {
					summaryMsg := &uctypes.AIMessage{
						MessageId: uuid.New().String(),
						Parts:     []uctypes.AIMessagePart{{Type: uctypes.AIMessagePartTypeText, Text: summary}},
					}
					nativeMsg, convErr := backend.ConvertAIMessageToNativeChatMessage(*summaryMsg)
					if convErr == nil {
						_ = chatstore.DefaultChatStore.PostMessage(chatOpts.ChatId, &chatOpts.Config, nativeMsg)
					}
				}
				continue stepLoop
			}
			// removed==0 means we had nothing left to compact. Fall through
			// to the same fail path the classifier would have picked if
			// reactive compact wasn't an option (FailFirstStep on the first
			// turn so the caller sees a wrapped error, otherwise FailFatal).
			if firstStep {
				metrics.HadError = true
				return metrics, fmt.Errorf("failed to stream %s chat: %w", chatOpts.Config.APIType, err)
			}
			metrics.HadError = true
			_ = sseHandler.AiMsgError(err.Error())
			_ = sseHandler.AiMsgFinish("", nil)
			break stepLoop
		case LoopActionFailFirstStep:
			metrics.HadError = true
			return metrics, fmt.Errorf("failed to stream %s chat: %w", chatOpts.Config.APIType, err)
		case LoopActionFailFatal:
			metrics.HadError = true
			_ = sseHandler.AiMsgError(err.Error())
			_ = sseHandler.AiMsgFinish("", nil)
			break stepLoop
		}
		for _, msg := range rtnMessages {
			if msg != nil {
				if err := chatstore.DefaultChatStore.PostMessage(chatOpts.ChatId, &chatOpts.Config, msg); err != nil {
					log.Printf("Failed to post message: %v", err)
				}
			}
		}
		if chatOpts.ContextBudget > 0 {
			// Tiered context governance — three escalating tiers, picked by
			// how close we are to the hard context limit:
			//   50% → collapse: keep messages, shrink old tool result *content*
			//                   to a placeholder (cheapest, preserves shape)
			//   60% → microcompact: drop whole older messages (still no summary)
			//   80% → heavy: summarize dropped range and inject a summary msg
			// Each tier subsumes the next less-aggressive one's effect, so we
			// only run the highest applicable tier per step (`switch`).
			budget := chatOpts.ContextBudget
			switch {
			case lastInputTokens > budget*4/5:
				summary, removed := chatstore.DefaultChatStore.CompactMessagesWithSummary(chatOpts.ChatId, 1, 10)
				if removed > 0 {
					log.Printf("context compaction (heavy): removed %d messages (input_tokens=%d, budget=%d)\n", removed, lastInputTokens, budget)
					// Enrich the bare role-count summary with files the agent
					// has touched across the whole turn — without this, the
					// model can lose track of work it already did and start
					// re-creating files it had written.
					if files := extractTouchedFilesFromAudit(metrics.AuditLog, 30); len(files) > 0 {
						summary = summary + "\nFiles modified during this conversation: " + strings.Join(files, ", ")
					}
					if summary != "" {
						summaryMsg := &uctypes.AIMessage{
							MessageId: uuid.New().String(),
							Parts:     []uctypes.AIMessagePart{{Type: uctypes.AIMessagePartTypeText, Text: summary}},
						}
						nativeMsg, err := backend.ConvertAIMessageToNativeChatMessage(*summaryMsg)
						if err == nil {
							_ = chatstore.DefaultChatStore.PostMessage(chatOpts.ChatId, &chatOpts.Config, nativeMsg)
						}
					}
				}
			case lastInputTokens > budget*3/5:
				removed := chatstore.DefaultChatStore.CompactMessages(chatOpts.ChatId, 1, 20)
				if removed > 0 {
					log.Printf("context compaction (microcompact): removed %d messages (input_tokens=%d, budget=%d)\n", removed, lastInputTokens, budget)
				}
			case lastInputTokens > budget/2:
				const collapsePlaceholder = "[earlier tool result — collapsed for context]"
				collapsed := chatstore.DefaultChatStore.CollapseOldToolResults(chatOpts.ChatId, 15, collapsePlaceholder)
				if collapsed > 0 {
					log.Printf("context governance (collapse): collapsed %d tool results (input_tokens=%d, budget=%d)\n", collapsed, lastInputTokens, budget)
				}
			}
		}
		firstStep = false

		// Continue-path dispatch: actions that follow a successful step.
		// Each case either continues the loop (with cont set) or breaks
		// stepLoop. The Finish case falls through to the pending-todos
		// nudge before breaking.
		switch action {
		case LoopActionContinueWithTools:
			metrics.ToolUseCount += len(stopReason.ToolCalls)
			toolResults := processAllToolCalls(backend, stopReason, chatOpts, sseHandler, metrics)
			for i, tc := range stopReason.ToolCalls {
				if tc.Name == "shell_exec" && i < len(toolResults) {
					if strings.Contains(toolResults[i].Text, `"exit_code":127`) {
						if cmdName := extractCmdName(tc.Input); cmdName != "" {
							unavailableCmds[cmdName] = true
						}
					}
				}
			}
			if len(unavailableCmds) > unavailableCmdsLastCount {
				unavailableCmdsLastCount = len(unavailableCmds)
				cmds := make([]string, 0, len(unavailableCmds))
				for c := range unavailableCmds {
					cmds = append(cmds, c)
				}
				chatOpts.SystemPrompt = append(chatOpts.SystemPrompt,
					fmt.Sprintf("ENVIRONMENT NOTE: The following commands are NOT available: %s. Do not retry them — use alternative approaches.", strings.Join(cmds, ", ")))
				log.Printf("unavailable commands detected: %v\n", cmds)
			}
			for _, tc := range stopReason.ToolCalls {
				if tc.Name == "write_text_file" || tc.Name == "edit_text_file" || tc.Name == "multi_edit" {
					hasProducedOutput = true
					break
				}
			}
			for _, tc := range stopReason.ToolCalls {
				inputJSON, _ := json.Marshal(tc.Input)
				sig := tc.Name + ":" + utilfn.TruncateString(string(inputJSON), 200)
				recentToolSigs = append(recentToolSigs, sig)
			}
			const doomLoopWindow = 6
			const doomLoopThreshold = 3
			if len(recentToolSigs) > doomLoopWindow {
				recentToolSigs = recentToolSigs[len(recentToolSigs)-doomLoopWindow:]
			}
			if !doomLoopWarned && detectDoomLoop(recentToolSigs, doomLoopThreshold) {
				chatOpts.SystemPrompt = append(chatOpts.SystemPrompt,
					"WARNING: You appear to be stuck in a repetitive loop making similar tool calls. "+
						"Stop and reconsider your approach. Try a fundamentally different strategy, "+
						"different tool, or different arguments. If you are stuck, explain what you are "+
						"trying to accomplish.")
				doomLoopWarned = true
				log.Printf("doom-loop detected in chat %s after %d tool calls\n", chatOpts.ChatId, len(recentToolSigs))
			}
			cont = &uctypes.WaveContinueResponse{
				Model:            chatOpts.Config.Model,
				ContinueFromKind: uctypes.StopKindToolUse,
			}
			continue stepLoop

		case LoopActionEscalateMaxTokens:
			// Reasoning models (Gemini 3.x, GPT-5, Claude w/ thinking) routinely
			// blow through small max_tokens budgets on extended thinking. First
			// hit: silently double the cap (capped at 64K).
			newMax := chatOpts.Config.MaxTokens * 2
			if newMax > 65536 {
				newMax = 65536
			}
			if newMax <= chatOpts.Config.MaxTokens {
				newMax = 32768
			}
			log.Printf("max_tokens hit: escalating %d -> %d\n", chatOpts.Config.MaxTokens, newMax)
			chatOpts.Config.MaxTokens = newMax
			maxTokensEscalated = true
			cont = &uctypes.WaveContinueResponse{
				Model:            chatOpts.Config.Model,
				ContinueFromKind: uctypes.StopKindMaxTokens,
			}
			continue stepLoop

		case LoopActionResumeAfterMaxTokens:
			// Subsequent max-tokens hits — tell the model to resume directly,
			// up to 3 attempts. classifyTerminalState enforces the cap.
			maxTokensResumeAttempts++
			log.Printf("max_tokens hit: resume attempt %d/3\n", maxTokensResumeAttempts)
			chatOpts.SystemPrompt = append(chatOpts.SystemPrompt,
				"Output token limit hit. Resume directly from where you stopped — no apology, no recap, just continue.")
			cont = &uctypes.WaveContinueResponse{
				Model:            chatOpts.Config.Model,
				ContinueFromKind: uctypes.StopKindMaxTokens,
			}
			continue stepLoop

		case LoopActionFailMaxTokens:
			log.Printf("max_tokens hit: giving up after %d resume attempts\n", maxTokensResumeAttempts)
			_ = sseHandler.AiMsgError("Output token limit hit repeatedly — model could not finish. Try a smaller request or raise ai:maxtokens.")
			_ = sseHandler.AiMsgFinish("max_tokens", nil)
			metrics.HadError = true
			break stepLoop

		case LoopActionFinish:
			if chatOpts.PendingTodosCheck != nil && chatOpts.PendingTodosCheck() && !pendingTodosNudged {
				pendingTodosNudged = true
				chatOpts.SystemPrompt = append(chatOpts.SystemPrompt,
					"You have pending todo items that are not yet completed. Do not stop — continue working on the remaining items. Use `todo_read` to review your progress.")
				cont = &uctypes.WaveContinueResponse{
					Model:            chatOpts.Config.Model,
					ContinueFromKind: uctypes.StopKindToolUse,
				}
				continue stepLoop
			}
			break stepLoop
		}
	}
	return metrics, nil
}

func ResolveToolCall(toolDef *uctypes.ToolDefinition, toolCall uctypes.WaveToolCall, chatOpts uctypes.WaveChatOpts) (result uctypes.AIToolResult) {
	result = uctypes.AIToolResult{
		ToolName:  toolCall.Name,
		ToolUseID: toolCall.ID,
	}

	defer func() {
		if r := recover(); r != nil {
			result.ErrorText = fmt.Sprintf("panic in tool execution: %v", r)
			result.Text = ""
		}
	}()

	if toolDef == nil {
		result.ErrorText = fmt.Sprintf("tool '%s' not found", toolCall.Name)
		return
	}

	// Try ToolTextCallback first, then ToolAnyCallback
	if toolDef.ToolTextCallback != nil {
		text, err := toolDef.ToolTextCallback(toolCall.Input)
		if err != nil {
			result.ErrorText = err.Error()
		} else {
			result.Text = text
			// Recompute tool description with the result
			if toolDef.ToolCallDesc != nil && toolCall.ToolUseData != nil {
				toolCall.ToolUseData.ToolDesc = toolDef.ToolCallDesc(toolCall.Input, text, toolCall.ToolUseData)
			}
		}
	} else if toolDef.ToolAnyCallback != nil {
		output, err := toolDef.ToolAnyCallback(toolCall.Input, toolCall.ToolUseData)
		if err != nil {
			result.ErrorText = err.Error()
		} else {
			// Marshal the result to JSON
			jsonBytes, marshalErr := json.Marshal(output)
			if marshalErr != nil {
				result.ErrorText = fmt.Sprintf("failed to marshal tool output: %v", marshalErr)
			} else {
				result.Text = string(jsonBytes)
				// Recompute tool description with the result
				if toolDef.ToolCallDesc != nil && toolCall.ToolUseData != nil {
					toolCall.ToolUseData.ToolDesc = toolDef.ToolCallDesc(toolCall.Input, output, toolCall.ToolUseData)
				}
			}
		}
	} else {
		result.ErrorText = fmt.Sprintf("tool '%s' has no callback functions", toolCall.Name)
	}

	return
}

func WaveAIPostMessageWrap(ctx context.Context, sseHandler *sse.SSEHandlerCh, message *uctypes.AIMessage, chatOpts uctypes.WaveChatOpts) error {
	startTime := time.Now()

	// Convert AIMessage to native chat message using backend
	backend, err := GetBackendByAPIType(chatOpts.Config.APIType)
	if err != nil {
		return err
	}
	convertedMessage, err := backend.ConvertAIMessageToNativeChatMessage(*message)
	if err != nil {
		return fmt.Errorf("message conversion failed: %w", err)
	}

	// Post message to chat store
	if err := chatstore.DefaultChatStore.PostMessage(chatOpts.ChatId, &chatOpts.Config, convertedMessage); err != nil {
		return fmt.Errorf("failed to store message: %w", err)
	}

	metrics, err := RunAIChat(ctx, sseHandler, backend, chatOpts)
	if metrics != nil {
		metrics.RequestDuration = int(time.Since(startTime).Milliseconds())
		for _, part := range message.Parts {
			if part.Type == uctypes.AIMessagePartTypeText {
				metrics.TextLen += len(part.Text)
			} else if part.Type == uctypes.AIMessagePartTypeFile {
				mimeType := strings.ToLower(part.MimeType)
				if strings.HasPrefix(mimeType, "image/") {
					metrics.ImageCount++
				} else if mimeType == "application/pdf" {
					metrics.PDFCount++
				} else {
					metrics.TextDocCount++
				}
			}
		}
		log.Printf("AI call metrics: requests=%d tools=%d images=%d pdfs=%d textdocs=%d textlen=%d duration=%dms error=%v\n",
			metrics.RequestCount, metrics.ToolUseCount,
			metrics.ImageCount, metrics.PDFCount, metrics.TextDocCount, metrics.TextLen, metrics.RequestDuration, metrics.HadError)

		sendAIMetricsTelemetry(ctx, metrics)
		if chatOpts.MetricsCallback != nil {
			chatOpts.MetricsCallback(metrics)
		}
	}
	return err
}

func sendAIMetricsTelemetry(ctx context.Context, metrics *uctypes.AIMetrics) {
	event := telemetrydata.MakeTEvent("waveai:post", telemetrydata.TEventProps{
		WaveAIAPIType:              metrics.Usage.APIType,
		WaveAIModel:                metrics.Usage.Model,
		WaveAIChatId:               metrics.ChatId,
		WaveAIStepNum:              metrics.StepNum,
		WaveAIInputTokens:          metrics.Usage.InputTokens,
		WaveAIOutputTokens:         metrics.Usage.OutputTokens,
		WaveAINativeWebSearchCount: metrics.Usage.NativeWebSearchCount,
		WaveAIRequestCount:         metrics.RequestCount,
		WaveAIToolUseCount:         metrics.ToolUseCount,
		WaveAIToolUseErrorCount:    metrics.ToolUseErrorCount,
		WaveAIToolDetail:           metrics.ToolDetail,
		WaveAIHadError:             metrics.HadError,
		WaveAIImageCount:           metrics.ImageCount,
		WaveAIPDFCount:             metrics.PDFCount,
		WaveAITextDocCount:         metrics.TextDocCount,
		WaveAITextLen:              metrics.TextLen,
		WaveAIFirstByteMs:          metrics.FirstByteLatency,
		WaveAIRequestDurMs:         metrics.RequestDuration,
		WaveAIWidgetAccess:         metrics.WidgetAccess,
		WaveAIThinkingLevel:        metrics.ThinkingLevel,
		WaveAIMode:                 metrics.AIMode,
		WaveAIProvider:             metrics.AIProvider,
		WaveAIIsLocal:              metrics.IsLocal,
	})
	_ = telemetry.RecordTEvent(ctx, event)
}

// PostMessageRequest represents the request body for posting a message
type PostMessageRequest struct {
	TabId        string            `json:"tabid,omitempty"`
	BuilderId    string            `json:"builderid,omitempty"`
	BuilderAppId string            `json:"builderappid,omitempty"`
	ChatID       string            `json:"chatid"`
	Msg          uctypes.AIMessage `json:"msg"`
	WidgetAccess bool              `json:"widgetaccess,omitempty"`
	AIMode       string            `json:"aimode"`
}

func WaveAIPostMessageHandler(w http.ResponseWriter, r *http.Request) {
	// Only allow POST method
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req PostMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate chatid is present and is a UUID
	if req.ChatID == "" {
		http.Error(w, "chatid is required in request body", http.StatusBadRequest)
		return
	}
	if _, err := uuid.Parse(req.ChatID); err != nil {
		http.Error(w, "chatid must be a valid UUID", http.StatusBadRequest)
		return
	}

	// Get RTInfo from TabId or BuilderId
	var rtInfo *waveobj.ObjRTInfo
	if req.TabId != "" {
		oref := waveobj.MakeORef(waveobj.OType_Tab, req.TabId)
		rtInfo = wstore.GetRTInfo(oref)
	} else if req.BuilderId != "" {
		oref := waveobj.MakeORef(waveobj.OType_Builder, req.BuilderId)
		rtInfo = wstore.GetRTInfo(oref)
	}
	if rtInfo == nil {
		rtInfo = &waveobj.ObjRTInfo{}
	}

	builderMode := req.BuilderId != ""
	if req.AIMode == "" {
		http.Error(w, "aimode is required in request body", http.StatusBadRequest)
		return
	}
	aiOpts, err := getWaveAISettings(false, builderMode, *rtInfo, req.AIMode)
	if err != nil {
		http.Error(w, fmt.Sprintf("WaveAI configuration error: %v", err), http.StatusInternalServerError)
		return
	}

	// Call the core WaveAIPostMessage function
	chatOpts := uctypes.WaveChatOpts{
		ChatId:               req.ChatID,
		ClientId:             wstore.GetClientId(),
		Config:               *aiOpts,
		WidgetAccess:         req.WidgetAccess,
		AllowNativeWebSearch: true,
		BuilderId:            req.BuilderId,
		BuilderAppId:         req.BuilderAppId,
	}
	chatOpts.SystemPrompt = getSystemPrompt(chatOpts.Config.APIType, chatOpts.Config.Model, chatOpts.BuilderId != "", chatOpts.Config.HasCapability(uctypes.AICapabilityTools), chatOpts.WidgetAccess)

	if req.TabId != "" {
		chatOpts.TabStateGenerator = func() (string, []uctypes.ToolDefinition, string, error) {
			tabState, tabTools, err := GenerateTabStateAndTools(r.Context(), req.TabId, req.WidgetAccess, &chatOpts)
			return tabState, tabTools, req.TabId, err
		}
	}

	if req.BuilderAppId != "" {
		chatOpts.BuilderAppGenerator = func() (string, string, string, error) {
			return generateBuilderAppData(req.BuilderAppId)
		}
	}

	if req.BuilderAppId != "" {
		chatOpts.Tools = append(chatOpts.Tools,
			GetBuilderWriteAppFileToolDefinition(req.BuilderAppId, req.BuilderId),
			GetBuilderEditAppFileToolDefinition(req.BuilderAppId, req.BuilderId),
			GetBuilderListFilesToolDefinition(req.BuilderAppId),
		)
	}

	// Validate the message
	if err := req.Msg.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("Message validation failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Create SSE handler and set up streaming
	sseHandler := sse.MakeSSEHandlerCh(w, r.Context())
	defer sseHandler.Close()

	if err := WaveAIPostMessageWrap(r.Context(), sseHandler, &req.Msg, chatOpts); err != nil {
		http.Error(w, fmt.Sprintf("Failed to post message: %v", err), http.StatusInternalServerError)
		return
	}
}

func WaveAIGetChatHandler(w http.ResponseWriter, r *http.Request) {
	// Only allow GET method
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get chatid from URL parameters
	chatID := r.URL.Query().Get("chatid")
	if chatID == "" {
		http.Error(w, "chatid parameter is required", http.StatusBadRequest)
		return
	}

	// Validate chatid is a UUID
	if _, err := uuid.Parse(chatID); err != nil {
		http.Error(w, "chatid must be a valid UUID", http.StatusBadRequest)
		return
	}

	// Get chat from store
	chat := chatstore.DefaultChatStore.Get(chatID)
	if chat == nil {
		http.Error(w, "chat not found", http.StatusNotFound)
		return
	}

	// Set response headers for JSON
	w.Header().Set("Content-Type", "application/json")

	// Encode and return the chat
	if err := json.NewEncoder(w).Encode(chat); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// CreateWriteTextFileDiff generates a diff for write_text_file or edit_text_file tool calls.
// Returns the original content, modified content, and any error.
// For Anthropic, this returns an unimplemented error.
func CreateWriteTextFileDiff(ctx context.Context, chatId string, toolCallId string) ([]byte, []byte, error) {
	aiChat := chatstore.DefaultChatStore.Get(chatId)
	if aiChat == nil {
		return nil, nil, fmt.Errorf("chat not found: %s", chatId)
	}

	backend, err := GetBackendByAPIType(aiChat.APIType)
	if err != nil {
		return nil, nil, err
	}

	funcCallInput := backend.GetFunctionCallInputByToolCallId(*aiChat, toolCallId)
	if funcCallInput == nil {
		return nil, nil, fmt.Errorf("tool call not found: %s", toolCallId)
	}

	toolName := funcCallInput.Name
	if toolName != "write_text_file" && toolName != "edit_text_file" {
		return nil, nil, fmt.Errorf("tool call %s is not a write_text_file or edit_text_file (got: %s)", toolCallId, toolName)
	}

	var backupFileName string
	if funcCallInput.ToolUseData != nil {
		backupFileName = funcCallInput.ToolUseData.WriteBackupFileName
	}

	var parsedArguments any
	if err := json.Unmarshal([]byte(funcCallInput.Arguments), &parsedArguments); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
	}

	if toolName == "edit_text_file" {
		originalContent, modifiedContent, err := EditTextFileDryRun(parsedArguments, backupFileName)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to generate diff: %w", err)
		}
		return originalContent, modifiedContent, nil
	}

	params, err := parseWriteTextFileInput(parsedArguments)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse write_text_file input: %w", err)
	}

	var originalContent []byte
	if backupFileName != "" {
		originalContent, err = os.ReadFile(backupFileName)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read backup file: %w", err)
		}
	} else {
		expandedPath, err := wavebase.ExpandHomeDir(params.Filename)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to expand path: %w", err)
		}
		originalContent, err = os.ReadFile(expandedPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("failed to read original file: %w", err)
		}
	}

	modifiedContent := []byte(params.Contents)
	return originalContent, modifiedContent, nil
}

type StaticFileInfo struct {
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Modified     string `json:"modified"`
	ModifiedTime string `json:"modified_time"`
}

func generateBuilderAppData(appId string) (string, string, string, error) {
	appGoFile := ""
	fileData, err := waveappstore.ReadAppFile(appId, "app.go")
	if err == nil {
		appGoFile = string(fileData.Contents)
	}

	staticFilesJSON := ""
	allFiles, err := waveappstore.ListAllAppFiles(appId)
	if err == nil {
		var staticFiles []StaticFileInfo
		for _, entry := range allFiles.Entries {
			if strings.HasPrefix(entry.Name, "static/") {
				staticFiles = append(staticFiles, StaticFileInfo{
					Name:         entry.Name,
					Size:         entry.Size,
					Modified:     entry.Modified,
					ModifiedTime: entry.ModifiedTime,
				})
			}
		}

		if len(staticFiles) > 0 {
			staticFilesBytes, marshalErr := json.Marshal(staticFiles)
			if marshalErr == nil {
				staticFilesJSON = string(staticFilesBytes)
			}
		}
	}

	platformInfo := wavebase.GetSystemSummary()
	if currentUser, userErr := user.Current(); userErr == nil && currentUser.Username != "" {
		platformInfo = fmt.Sprintf("Local Machine: %s, User: %s", platformInfo, currentUser.Username)
	} else {
		platformInfo = fmt.Sprintf("Local Machine: %s", platformInfo)
	}

	return appGoFile, staticFilesJSON, platformInfo, nil
}
