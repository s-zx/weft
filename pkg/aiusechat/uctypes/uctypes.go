// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package uctypes

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

const DefaultAnthropicModel = "claude-sonnet-4-5"

const (
	APIType_AnthropicMessages = "anthropic-messages"
	APIType_OpenAIResponses   = "openai-responses"
	APIType_OpenAIChat        = "openai-chat"
	APIType_GoogleGemini      = "google-gemini"
)

const (
	AIProvider_Google      = "google"
	AIProvider_Groq        = "groq"
	AIProvider_OpenRouter  = "openrouter"
	AIProvider_NanoGPT     = "nanogpt"
	AIProvider_OpenAI      = "openai"
	AIProvider_Azure       = "azure"
	AIProvider_AzureLegacy = "azure-legacy"
	AIProvider_Custom      = "custom"
)

type UseChatRequest struct {
	Messages []UIMessage `json:"messages"`
}

type UIChat struct {
	ChatId     string      `json:"chatid"`
	APIType    string      `json:"apitype"`
	Model      string      `json:"model"`
	APIVersion string      `json:"apiversion"`
	Messages   []UIMessage `json:"messages"`
}

type UIMessage struct {
	ID       string          `json:"id"`
	Role     string          `json:"role"` // "system", "user", "assistant"
	Metadata any             `json:"metadata,omitempty"`
	Parts    []UIMessagePart `json:"parts,omitempty"`
}

type UIMessagePart struct {
	// text, reasoning, tool-[toolname], source-url, source-document, file, data-[dataname], step-start
	Type string `json:"type"`

	// TextUIPart & ReasoningUIPart
	Text string `json:"text,omitempty"`
	// State field:
	// - For "text"/"reasoning" types: optional, values are "streaming" or "done"
	// - For "tool-*" types: required, values are "input-streaming", "input-available", "output-available", or "output-error"
	State string `json:"state,omitempty"`

	// ToolUIPart
	ToolCallID       string `json:"toolCallId,omitempty"`
	Input            any    `json:"input,omitempty"`
	Output           any    `json:"output,omitempty"`
	ErrorText        string `json:"errorText,omitempty"`
	ProviderExecuted *bool  `json:"providerExecuted,omitempty"`

	// SourceUrlUIPart & SourceDocumentUIPart
	SourceID  string `json:"sourceId,omitempty"`
	URL       string `json:"url,omitempty"`
	Title     string `json:"title,omitempty"`
	Filename  string `json:"filename,omitempty"`
	MediaType string `json:"mediaType,omitempty"`

	// FileUIPart (uses URL and MediaType above)

	// DataUIPart
	ID   string `json:"id,omitempty"`
	Data any    `json:"data,omitempty"`

	// Provider metadata (ReasoningUIPart, SourceUrlUIPart, SourceDocumentUIPart)
	ProviderMetadata map[string]any `json:"providerMetadata,omitempty"`
}

// when updating this struct, also modify frontend/app/aipanel/aitypes.ts WaveUIDataTypes.userfile
type UIMessageDataUserFile struct {
	FileName   string `json:"filename,omitempty"`
	Size       int    `json:"size,omitempty"`
	MimeType   string `json:"mimetype,omitempty"`
	PreviewUrl string `json:"previewurl,omitempty"`
}

// DefaultMaxToolResultSizeChars is the inline-result cap applied when a
// tool's MaxResultSizeChars is unset. ~25K chars roughly equals 6K tokens
// — enough for most file reads and short shell output, but small enough
// that an unexpected mega-output (e.g. log dump, 1MB file) won't blow up
// the next request.
const DefaultMaxToolResultSizeChars = 25_000

// ToolDefinition represents a tool that can be used by the AI model
type ToolDefinition struct {
	Name                 string         `json:"name"`
	DisplayName          string         `json:"displayname,omitempty"` // internal field (cannot marshal to API, must be stripped)
	Description          string         `json:"description"`
	ShortDescription     string         `json:"shortdescription,omitempty"` // internal field (cannot marshal to API, must be stripped)
	ToolLogName          string         `json:"-"`                          // short name for telemetry (e.g., "term:getscrollback")
	InputSchema          map[string]any `json:"input_schema"`
	Strict               bool           `json:"strict,omitempty"`
	RequiredCapabilities []string       `json:"requiredcapabilities,omitempty"`

	Parallel bool `json:"-"`

	// MaxResultSizeChars caps the inline tool result size in chars. When the
	// tool callback returns text longer than this, the loop spills the full
	// text to disk and replaces the inline result with a preview + path so
	// the model can still reference it without bloating the context window.
	// Zero or negative means use DefaultMaxToolResultSizeChars.
	MaxResultSizeChars int `json:"-"`

	// Prompt is model-facing usage guidance appended to the system prompt
	// when this tool is included for the turn. Use it for the rules a model
	// would otherwise have to learn from failures: parallel-safety,
	// uniqueness requirements, "must read before edit", path conventions.
	// The schema's Description should remain a one-liner; long-form
	// instructions belong here.
	Prompt string `json:"-"`

	ToolTextCallback func(any) (string, error)                     `json:"-"`
	ToolAnyCallback  func(any, *UIMessageDataToolUse) (any, error) `json:"-"` // *UIMessageDataToolUse will NOT be nil
	ToolCallDesc     func(any, any, *UIMessageDataToolUse) string  `json:"-"` // passed input, output (may be nil), *UIMessageDataToolUse (may be nil)
	ToolApproval     func(any) string                              `json:"-"`
	ToolVerifyInput  func(any, *UIMessageDataToolUse) error        `json:"-"` // *UIMessageDataToolUse will NOT be nil
	ToolProgressDesc func(any) ([]string, error)                   `json:"-"`

	// Per-tool hooks. BeforeHooks run after approval but before the callback;
	// any non-nil return short-circuits execution with that result. AfterHooks
	// mutate the result in place after the callback. Per-tool hooks run before
	// the global hooks registered on WaveChatOpts.
	BeforeHooks []BeforeToolHook `json:"-"`
	AfterHooks  []AfterToolHook  `json:"-"`
}

type ToolCallOutcome struct {
	Result      AIToolResult
	Audit       ToolAuditEvent
	IsError     bool
	ToolLogName string
	FileChanged string
	FileBackup  string
	FileIsNew   bool
}

func (td *ToolDefinition) Clean() *ToolDefinition {
	if td == nil {
		return nil
	}
	rtn := *td
	rtn.DisplayName = ""
	rtn.ShortDescription = ""
	return &rtn
}

func (td *ToolDefinition) Desc() string {
	if td == nil {
		return ""
	}
	if td.ShortDescription != "" {
		return td.ShortDescription
	}
	return td.Description
}

func (td *ToolDefinition) HasRequiredCapabilities(capabilities []string) bool {
	if td == nil || len(td.RequiredCapabilities) == 0 {
		return true
	}
	for _, reqCap := range td.RequiredCapabilities {
		if !slices.Contains(capabilities, reqCap) {
			return false
		}
	}
	return true
}

//------------------
// Wave specific types, stop reasons, tool calls, config
// these are used internally to coordinate the calls/steps

const (
	ThinkingLevelLow    = "low"
	ThinkingLevelMedium = "medium"
	ThinkingLevelHigh   = "high"

	VerbosityLevelLow    = "low"
	VerbosityLevelMedium = "medium"
	VerbosityLevelHigh   = "high"
)

const (
	AIModeQuick          = "waveai@quick"
	AIModeBalanced       = "waveai@balanced"
	AIModeDeep           = "waveai@deep"
	AIModeBuilderDefault = "waveaibuilder@default"
	AIModeBuilderDeep    = "waveaibuilder@deep"
)

const (
	ToolUseStatusPending   = "pending"
	ToolUseStatusError     = "error"
	ToolUseStatusCompleted = "completed"
)

const (
	AICapabilityTools  = "tools"
	AICapabilityImages = "images"
	AICapabilityPdfs   = "pdfs"
)

const (
	ApprovalNeedsApproval = "needs-approval"
	ApprovalUserApproved  = "user-approved"
	ApprovalUserDenied    = "user-denied"
	ApprovalTimeout       = "timeout"
	ApprovalAutoApproved  = "auto-approved"
	ApprovalCanceled      = "canceled"
)

// when updating this struct, also modify frontend/app/aipanel/aitypes.ts WaveUIDataTypes.tooluse
type UIMessageDataToolUse struct {
	ToolCallId          string `json:"toolcallid"`
	ToolName            string `json:"toolname"`
	ToolDesc            string `json:"tooldesc"`
	Status              string `json:"status"`
	RunTs               int64  `json:"runts,omitempty"`
	ErrorMessage        string `json:"errormessage,omitempty"`
	Approval            string `json:"approval,omitempty"`
	BlockId             string `json:"blockid,omitempty"`
	WriteBackupFileName string `json:"writebackupfilename,omitempty"`
	InputFileName       string `json:"inputfilename,omitempty"`
	OriginalContent     string `json:"originalcontent,omitempty"`
	ModifiedContent     string `json:"modifiedcontent,omitempty"`
}

func (d *UIMessageDataToolUse) IsApproved() bool {
	return d.Approval == "" || d.Approval == ApprovalUserApproved || d.Approval == ApprovalAutoApproved
}

// when updating this struct, also modify frontend/app/aipanel/aitypes.ts WaveUIDataTypes.toolprogress
type UIMessageDataToolProgress struct {
	ToolCallId  string   `json:"toolcallid"`
	ToolName    string   `json:"toolname"`
	StatusLines []string `json:"statuslines"`
}

type StopReasonKind string

const (
	StopKindDone       StopReasonKind = "done"
	StopKindToolUse    StopReasonKind = "tool_use"
	StopKindMaxTokens  StopReasonKind = "max_tokens"
	StopKindContent    StopReasonKind = "content_filter"
	StopKindCanceled   StopReasonKind = "canceled"
	StopKindError      StopReasonKind = "error"
	StopKindPauseTurn  StopReasonKind = "pause_turn"
	StopKindRateLimit  StopReasonKind = "rate_limit"
	StopKindStepBudget StopReasonKind = "step_budget"
)

type WaveToolCall struct {
	ID          string                `json:"id"`                    // Anthropic tool_use.id
	Name        string                `json:"name,omitempty"`        // tool name (if provided)
	Input       any                   `json:"input,omitempty"`       // accumulated input JSON
	ToolUseData *UIMessageDataToolUse `json:"toolusedata,omitempty"` // UI tool use data
}

type WaveStopReason struct {
	Kind      StopReasonKind `json:"kind"`
	RawReason string         `json:"raw_reason,omitempty"`
	ToolCalls []WaveToolCall `json:"tool_calls,omitempty"`
	ErrorType string         `json:"error_type,omitempty"`
	ErrorText string         `json:"error_text,omitempty"`
}

// Wave Specific parameter used to signal to our step function that this is a continuation step, not an initial step
type WaveContinueResponse struct {
	Model            string         `json:"model,omitempty"`
	ContinueFromKind StopReasonKind `json:"continue_from_kind"`
}

// Wave Specific AI opts for configuration
type AIOptsType struct {
	Provider      string   `json:"provider,omitempty"`
	APIType       string   `json:"apitype,omitempty"`
	Model         string   `json:"model"`
	APIToken      string   `json:"apitoken"`
	APIVersion    string   `json:"apiversion,omitempty"`
	Endpoint      string   `json:"endpoint,omitempty"`
	ProxyURL      string   `json:"proxyurl,omitempty"`
	MaxTokens     int      `json:"maxtokens,omitempty"`
	TimeoutMs     int      `json:"timeoutms,omitempty"`
	ThinkingLevel string   `json:"thinkinglevel,omitempty"` // ThinkingLevelLow, ThinkingLevelMedium, or ThinkingLevelHigh
	Verbosity     string   `json:"verbosity,omitempty"`     // Text verbosity level (OpenAI Responses API only, ignored by other backends)
	AIMode        string   `json:"aimode,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

func (opts AIOptsType) HasCapability(cap string) bool {
	return slices.Contains(opts.Capabilities, cap)
}

type AIChat struct {
	ChatId         string         `json:"chatid"`
	APIType        string         `json:"apitype"`
	Model          string         `json:"model"`
	APIVersion     string         `json:"apiversion"`
	NativeMessages []GenAIMessage `json:"nativemessages"`
}

type AIUsage struct {
	APIType              string `json:"apitype"`
	Model                string `json:"model"`
	InputTokens          int    `json:"inputtokens,omitempty"`
	OutputTokens         int    `json:"outputtokens,omitempty"`
	NativeWebSearchCount int    `json:"nativewebsearchcount,omitempty"`
}

type ToolAuditEvent struct {
	Timestamp  int64  `json:"ts"`
	ChatId     string `json:"chatid"`
	ToolName   string `json:"tool"`
	ToolCallId string `json:"callid"`
	InputArgs  string `json:"input"`
	Approval   string `json:"approval"`
	DurationMs int64  `json:"durationms"`
	Outcome    string `json:"outcome"`
	ErrorText  string `json:"error,omitempty"`
	ErrorType  string `json:"errortype,omitempty"` // one of ErrorType_* constants
}

type AIMetrics struct {
	ChatId            string           `json:"chatid"`
	StepNum           int              `json:"stepnum"`
	Usage             AIUsage          `json:"usage"`
	RequestCount      int              `json:"requestcount"`
	ToolUseCount      int              `json:"toolusecount"`
	ToolUseErrorCount int              `json:"tooluseerrorcount"`
	ToolDetail        map[string]int   `json:"tooldetail,omitempty"`
	HadError          bool             `json:"haderror"`
	ImageCount        int              `json:"imagecount"`
	PDFCount          int              `json:"pdfcount"`
	TextDocCount      int              `json:"textdoccount"`
	TextLen           int              `json:"textlen"`
	FirstByteLatency  int              `json:"firstbytelatency"` // ms
	RequestDuration   int              `json:"requestduration"`  // ms
	WidgetAccess      bool             `json:"widgetaccess"`
	ThinkingLevel     string           `json:"thinkinglevel,omitempty"`
	AIMode            string           `json:"aimode,omitempty"`
	AIProvider        string           `json:"aiprovider,omitempty"`
	IsLocal           bool             `json:"islocal,omitempty"`
	AuditLog          []ToolAuditEvent `json:"auditlog,omitempty"`
}

type AIFunctionCallInput struct {
	CallId      string                `json:"call_id"`
	Name        string                `json:"name"`
	Arguments   string                `json:"arguments"`
	ToolUseData *UIMessageDataToolUse `json:"toolusedata,omitempty"`
}

// GenAIMessage interface for messages stored in conversations
// All messages must have a unique identifier for idempotency checks
type GenAIMessage interface {
	GetMessageId() string
	GetUsage() *AIUsage
	GetRole() string
}

// MessageDependsOnPrev is implemented by messages that cannot stand alone
// because their content references the immediately preceding message — e.g.
// a user-role message containing tool_result blocks whose tool_use IDs come
// from the prior assistant message, or an OpenAI Responses function_call_output
// that follows its function_call. Compaction uses this to avoid cutting in
// the middle of a tool-use/tool-result pair (which would 400 from the API).
type MessageDependsOnPrev interface {
	DependsOnPrev() bool
}

// ToolResultCollapsible is implemented by native chat messages that carry
// tool result content. Context-collapse (the lighter-touch sibling of full
// compaction) walks older messages, type-asserts to this interface, and
// replaces the long tool result text with `placeholder` while leaving the
// message structure intact. This preserves tool_use → tool_result pairing
// for the API and keeps the historical trail visible to the model — the
// model still sees "I called X and got [collapsed]" rather than nothing.
//
// Implementations must:
//   - leave message id, role, and tool-call ids untouched (so the message
//     still pairs with its assistant-side tool_use);
//   - replace ONLY the human-readable result content;
//   - return the number of tool-result blocks/parts they collapsed (0 if
//     there were none — a no-op is fine).
type ToolResultCollapsible interface {
	CollapseToolResults(placeholder string) (collapsed int)
}

// LLMVisibleProvider is implemented by transcript-only messages that should
// live in the chatstore (visible to /tree, audit, UI) but should NOT be
// included in the message list sent to the LLM. Messages that don't
// implement this interface are LLM-visible by default — the existing
// per-backend native message types (anthropic, openai-chat, gemini,
// openai-responses) carry actual provider content and stay visible.
//
// Implementations return false to mark themselves transcript-only. There
// is no method to "selectively visible" — a message is either a real
// provider message that goes to the LLM or a transcript artifact that
// stays local. Use cases:
//
//   - subagent transcript: parent agent records its child's full transcript
//     for UI rendering, but the LLM only sees the final tool result text.
//   - audit/notification rows: "user denied tool X" or "files changed
//     externally" surfaced inline so /tree shows context, but the LLM
//     gets the synthesized error result instead.
//   - branch markers: in a future branching session model, "← branched
//     here at step N" is transcript-only.
//
// For "compaction summary" message type, prefer LLMVisible() == true —
// the summary IS meant for the model to see. This interface is for content
// the model should never see.
type LLMVisibleProvider interface {
	LLMVisible() bool
}

// IsLLMVisibleMessage returns whether m should be included in the message
// list sent to an LLM. Messages that don't implement LLMVisibleProvider
// are treated as visible (the conservative default — historical Crest
// behavior is "every native message goes to the model").
func IsLLMVisibleMessage(m GenAIMessage) bool {
	if v, ok := m.(LLMVisibleProvider); ok {
		return v.LLMVisible()
	}
	return true
}

// FilterLLMVisible returns a new slice containing only the messages from
// `msgs` that are LLM-visible. The input slice is not modified. Useful at
// the LLM serialization boundary; the chatstore itself keeps the full
// transcript including transcript-only messages.
func FilterLLMVisible(msgs []GenAIMessage) []GenAIMessage {
	out := make([]GenAIMessage, 0, len(msgs))
	for _, m := range msgs {
		if IsLLMVisibleMessage(m) {
			out = append(out, m)
		}
	}
	return out
}

const (
	AIMessagePartTypeText = "text"
	AIMessagePartTypeFile = "file"
)

// wave specific for POSTing a new message to a convo
type AIMessage struct {
	MessageId string          `json:"messageid"` // only for idempotency
	Parts     []AIMessagePart `json:"parts"`
}

type AIMessagePart struct {
	Type string `json:"type"` // "text", "file"

	// for "text"
	Text string `json:"text,omitempty"`

	// for "file"
	// mimetype is required, filename is not
	// either data or url (not both) must be set
	// url must be either an "https" or "data" url
	FileName   string `json:"filename,omitempty"`
	MimeType   string `json:"mimetype,omitempty"` // required
	Data       []byte `json:"data,omitempty"`     // raw data (base64 on wire)
	URL        string `json:"url,omitempty"`
	Size       int    `json:"size,omitempty"`
	PreviewUrl string `json:"previewurl,omitempty"` // 128x128 webp data url for images
}

type AIToolResult struct {
	ToolName  string `json:"toolname"`
	ToolUseID string `json:"tooluseid"`
	ErrorText string `json:"errortext,omitempty"`
	// ErrorType is a coarse machine-readable category for telemetry and
	// loop-level decisions. Empty when the call succeeded. Allowed values
	// are the ErrorType_* constants below — keep this list flat (no
	// per-tool subcategories) so reports stay comparable across tools.
	ErrorType string `json:"errortype,omitempty"`
	Text      string `json:"text,omitempty"`
}

const (
	ErrorTypeValidation = "validation"  // bad input, schema mismatch, missing required field
	ErrorTypeNotFound   = "not_found"   // file/path/resource doesn't exist
	ErrorTypePermission = "permission"  // EACCES, EPERM, sandbox/policy denial
	ErrorTypeTimeout    = "timeout"     // tool exceeded its own time budget
	ErrorTypeCanceled   = "canceled"    // user/parent context canceled
	ErrorTypePanic      = "panic"       // recovered runtime panic
	ErrorTypeStaleFile  = "stale_file"  // file modified externally since last read
	ErrorTypeUnknown    = "unknown"     // fallback when classification fails
)

func (m *AIMessage) GetMessageId() string {
	return m.MessageId
}

func (m *AIMessage) Validate() error {
	if m.MessageId == "" {
		return fmt.Errorf("messageid must be set")
	}

	if len(m.Parts) == 0 {
		return fmt.Errorf("parts must not be empty")
	}

	for i, part := range m.Parts {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("part %d: %w", i, err)
		}
	}

	return nil
}

func (p *AIMessagePart) Validate() error {
	if p.Type == AIMessagePartTypeText {
		if p.Text == "" {
			return fmt.Errorf("text type requires non-empty text field")
		}
		// Check that no file fields are set
		if p.FileName != "" || p.MimeType != "" || len(p.Data) > 0 || p.URL != "" {
			return fmt.Errorf("text type cannot have file fields set")
		}
		return nil
	}

	if p.Type == AIMessagePartTypeFile {
		if p.Text != "" {
			return fmt.Errorf("file type cannot have text field set")
		}

		if p.MimeType == "" {
			return fmt.Errorf("file type requires mimetype")
		}

		// Either data or url (not both) must be set
		hasData := len(p.Data) > 0
		hasURL := p.URL != ""

		if !hasData && !hasURL {
			return fmt.Errorf("file type requires either data or url")
		}

		if hasData && hasURL {
			return fmt.Errorf("file type cannot have both data and url set")
		}

		// If URL is set, validate it's https or data URL
		if hasURL {
			parsedURL, err := url.Parse(p.URL)
			if err != nil {
				return fmt.Errorf("invalid url: %w", err)
			}

			if parsedURL.Scheme != "https" && parsedURL.Scheme != "data" {
				return fmt.Errorf("url must be https or data URL, got %q", parsedURL.Scheme)
			}
		}
		return nil
	}

	return fmt.Errorf("type must be %q or %q, got %q", AIMessagePartTypeText, AIMessagePartTypeFile, p.Type)
}

// ---------------------
// AI SDK Streaming Protocol

// Type can be one of these consts...
// text-start, text-delta, text-end,
// reasoning-start, reasoning-delta, reasoning-end,
// source-url, source-document,
// file,
// data-*,
// tool-input-start, tool-input-delta, tool-input-available, tool-output-available,
// error, start-step, finish-step, finish
type UseChatStreamPart struct {
	Type string `json:"type"`

	// Text
	Text string `json:"text,omitempty"`

	// Reasoning
	Delta string `json:"delta,omitempty"`

	// Source parts
	SourceID  string `json:"sourceId,omitempty"`
	URL       string `json:"url,omitempty"`       // also for file urls
	MediaType string `json:"mediaType,omitempty"` // also for file types
	Title     string `json:"title,omitempty"`

	// Data (custom data-\*)
	Data any `json:"data,omitempty"`

	// Tool use / tool result
	ToolCallID     string `json:"toolCallId,omitempty"`
	ToolName       string `json:"toolName,omitempty"`
	Input          any    `json:"input,omitempty"`
	Output         any    `json:"output,omitempty"`
	InputTextDelta string `json:"inputTextDelta,omitempty"`

	// Control parts (start/finish steps, errors, etc.)
	ErrorText string `json:"errorText,omitempty"`
}

// GetContent extracts the text content from the parts array
func (m *UIMessage) GetContent() string {
	if len(m.Parts) > 0 {
		var content strings.Builder
		for _, part := range m.Parts {
			if part.Type == "text" {
				content.WriteString(part.Text)
			}
		}
		return content.String()
	}
	return ""
}

// ApprovalDecision is what an ApprovalDecider returns. Three behaviors
// match the engine's RuleAllow/RuleAsk/RuleDeny but are kept as
// strings here so uctypes can stay unaware of the permissions package
// (which would otherwise create a uctypes ↔ permissions cycle once
// permissions imports uctypes for tool input shapes).
//
// `behavior` is "allow" | "ask" | "deny". `reason` is a short
// human-readable explanation that the dispatcher writes into the
// tool result on deny so the model can read why it was rejected.
type ApprovalDecision struct {
	Behavior string
	Reason   string
}

// ApprovalDecider is the closure CreateToolUseData calls to decide
// the per-call Approval state. Set by the agent runtime (in
// pkg/agent) to a function that wraps the permissions Engine plus the
// session's cwd/posture; uctypes itself stays free of policy logic.
//
// nil is fine — CreateToolUseData falls back to the tool's own
// ToolApproval callback (the v1 mode-baked path) when no decider is
// installed. This makes the field opt-in during the migration.
type ApprovalDecider func(toolCall WaveToolCall) ApprovalDecision

type WaveChatOpts struct {
	ChatId               string
	ClientId             string
	Config               AIOptsType
	Tools                []ToolDefinition
	SystemPrompt         []string
	TabStateGenerator    func() (string, []ToolDefinition, string, error)
	BuilderAppGenerator  func() (string, string, string, error)
	WidgetAccess         bool
	AllowNativeWebSearch bool
	BuilderId            string
	BuilderAppId         string
	Source               string
	MaxSteps             int
	ContextBudget        int
	MetricsCallback      func(*AIMetrics)
	FileChangeCallback   func(path, backupPath string, isNew bool)
	PendingTodosCheck    func() bool

	// Posture is the per-chat strictness signal threaded into
	// ApprovalDecider. Empty string is treated as "default". Set by
	// the agent runtime from the user's settings and the API
	// `mode: "bench"` alias.
	Posture string

	// ApprovalDecider, when non-nil, takes precedence over the
	// per-tool ToolApproval callback at CreateToolUseData time. The
	// agent runtime installs this from a Permissions Engine; tests
	// and legacy paths can leave it nil.
	ApprovalDecider ApprovalDecider

	// Global tool hooks. BeforeToolHooks run after per-tool BeforeHooks; any
	// non-nil return short-circuits execution with that result. AfterToolHooks
	// run after per-tool AfterHooks and mutate the result in place. Built-in
	// hooks (spill, error classification, reflection suffix) are installed by
	// RunAIChat at the top of the loop; callers can append additional hooks
	// before invoking the loop.
	BeforeToolHooks []BeforeToolHook
	AfterToolHooks  []AfterToolHook

	// EventSinks receive structured AgentEvent notifications at lifecycle
	// points (agent start/end, turn start/end, tool start/end). Additive to
	// existing SSE/audit; sinks are called synchronously, so expensive work
	// must hand off to goroutines.
	EventSinks []AgentEventSink

	// ephemeral to the step
	TabState       string
	TabTools       []ToolDefinition
	TabId          string
	AppGoFile      string
	AppStaticFiles string
	PlatformInfo   string
}

func (opts *WaveChatOpts) GetToolDefinition(toolName string) *ToolDefinition {
	for _, tool := range opts.Tools {
		if tool.Name == toolName {
			return &tool
		}
	}
	for _, tool := range opts.TabTools {
		if tool.Name == toolName {
			return &tool
		}
	}
	return nil
}

func (opts *WaveChatOpts) GetWaveRequestType() string {
	if opts.Source != "" {
		return opts.Source
	}
	if opts.BuilderId != "" {
		return "waveapps-builder"
	}
	return "waveai"
}

type ProxyErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func AreModelsCompatible(apiType, model1, model2 string) bool {
	if model1 == model2 {
		return true
	}

	if apiType == APIType_OpenAIResponses {
		gpt5Models := map[string]bool{
			"gpt-5.2":    true,
			"gpt-5.1":    true,
			"gpt-5":      true,
			"gpt-5-mini": true,
			"gpt-5-nano": true,
		}

		if gpt5Models[model1] && gpt5Models[model2] {
			return true
		}
	}

	return false
}
