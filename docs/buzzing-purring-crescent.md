# Crest Native Go Coding Agent — Implementation Plan

## Context

Crest currently has two AI surfaces: the legacy `WaveAI` side panel (`frontend/app/aipanel/`) and the already-shipped `:` terminal overlay (`frontend/app/view/term/term-agent.tsx`). Both point at `/api/post-chat-message`, which runs the `pkg/aiusechat` step loop with a generic chat system prompt and a small built-in tool set.

We want a **native Go coding agent** — inspired by ForgeCode (Rust, Apache 2.0) but not integrated as a sidecar — that:

- Runs inside Crest as a first-class capability, reusing `pkg/aiusechat` as the mechanism layer (providers, SSE stream, tool protocol, approval registry).
- Ships three Forge-style modes from MVP: `:ask` (read-only research), `:plan` (produce a plan doc), `:do` (execute changes via Crest primitives, visible as blocks).
- Treats agent actions — especially shell execution — as **first-class Crest blocks** the user can observe and interact with, rather than hidden subprocesses.
- Reserves hooks for Phase 2 browser automation (CDP over webview blocks) and MCP/skills integration.
- Preserves Apache 2.0 attribution to ForgeCode for any extracted prompts.

The `:` overlay, AI-SDK v5 streaming, tool-call UI, and approval UI all exist and must be reused, not rewritten.

## Package Layout — `pkg/agent/`

```
pkg/agent/
├── agent.go          — RunAgent(ctx, sse, opts) → wraps aiusechat.RunAIChat
├── modes.go          — Mode struct, ApprovalPolicy, ModeAsk/ModePlan/ModeDo, LookupMode
├── session.go        — Session struct (chatId, tabId, blockId, mode, cwd, connection, recentCmds)
├── context.go        — BuildTerminalContext(sess) → <terminal_context> block for system prompt
├── http.go           — PostAgentMessageHandler + PostAgentMessageRequest
├── registry.go       — ToolRegistry, ToolsForMode, namespace reservation (browser.*)
├── chatstore.go      — namespaced wrapper around chatstore.DefaultChatStore ("agent:"+chatId)
├── prompts.go        — //go:embed prompts/*.md loader
├── prompts/
│   ├── shared_header.md
│   ├── ask.md
│   ├── plan.md
│   ├── do.md
│   └── UPSTREAM.md   — pinned ForgeCode commit SHA + re-sync notes
├── NOTICE            — Apache 2.0 attribution to tailcallhq/forgecode
├── tools/
│   ├── read_file.go        — adapter: pkg/aiusechat/tools_readfile.go
│   ├── list_dir.go         — adapter: pkg/aiusechat/tools_readdir.go
│   ├── write_file.go       — adapter: pkg/aiusechat/tools_writefile.go
│   ├── edit_file.go        — adapter: pkg/aiusechat/tools_writefile.go (edit_text_file)
│   ├── get_scrollback.go   — adapter: pkg/aiusechat/tools_term.go
│   ├── cmd_history.go      — NEW: cmdblock.GetByBlockID + filestore.WFS.ReadAt
│   ├── shell_exec.go       — NEW: creates cmd-block, streams output, waits completion
│   ├── write_plan.go       — NEW: writes markdown to <cwd>/.crest-plans/<slug>.md + opens preview block
│   ├── create_block.go     — NEW: thin wrapper on CreateBlockCommand
│   ├── focus_block.go      — NEW: thin wrapper on SetBlockFocusCommand
│   └── browser_stub.go     — NEW: reserves browser.{navigate,screenshot,click,read_text} names, returns "not yet available"
```

### Public entrypoint

```go
// pkg/agent/agent.go
type AgentOpts struct {
    Session *Session
    Mode    *Mode
    UserMsg *uctypes.AIMessage
    AIOpts  uctypes.AIOptsType
}

func RunAgent(ctx context.Context, sse *sse.SSEHandlerCh, opts AgentOpts) (*uctypes.AIMetrics, error)
```

`RunAgent` composes `uctypes.WaveChatOpts{ChatId, Config, Tools, SystemPrompt, TabId}`:
- `Tools = registry.ToolsForMode(opts.Mode, opts.Session)`
- `SystemPrompt = []string{shared_header, opts.Mode.SystemPrompt, context.BuildTerminalContext(opts.Session)}`
- `TabStateGenerator` is left nil (terminal context is static per request; per-step dynamic tools aren't needed in MVP)

Calls `aiusechat.RunAIChat(ctx, sse, backend, chatOpts)` with `backend := aiusechat.GetBackendByAPIType(opts.AIOpts.APIType)`.

**Division of concerns (enforced):**
- `pkg/agent` = policy (modes, approval rules, prompts, terminal-aware tools)
- `pkg/aiusechat` = mechanism (providers, stream protocol, step loop, approval registry)
- `pkg/aiusechat` MUST NOT import `pkg/agent`. Documented in `pkg/agent/README.md`.

## Mode Contract

```go
// pkg/agent/modes.go
type ApprovalPolicy struct {
    AutoApproveAll       bool
    AutoApproveTools     map[string]bool  // tool name → auto
    AutoApprovePathGlobs []string         // e.g. ".crest-plans/**"
    RequireApproval      map[string]bool  // explicit deny-auto
}

type Mode struct {
    Name          string   // "ask" | "plan" | "do"
    DisplayName   string
    SystemPrompt  string   // embedded from prompts/<name>.md
    ToolNames     []string // resolved against registry; supports globs (e.g. "browser.*")
    AllowMutation bool
    Approval      ApprovalPolicy
    StepBudget    int      // default 40
    FailureBudget int      // default 3 consecutive tool failures
}
```

### Seed modes

| Mode | Tools | Approval | Mutation |
|---|---|---|---|
| `ask` | `read_file`, `list_dir`, `get_scrollback`, `cmd_history` | `AutoApproveAll: true` | false |
| `plan` | `read_file`, `list_dir`, `get_scrollback`, `cmd_history`, `write_plan` | `AutoApprovePathGlobs: [".crest-plans/**"]` | false (plans only) |
| `do` | `read_file`, `list_dir`, `get_scrollback`, `cmd_history`, `write_file`, `edit_file`, `shell_exec`, `create_block`, `focus_block` | `RequireApproval: {write_file, edit_file, shell_exec, create_block}` = true; reads auto | true |

Each tool's `ToolDefinition.ToolApproval` callback consults `mode.Approval` and returns `ApprovalAutoApproved` or `ApprovalNeedsApproval`. The existing aiusechat step loop + `toolapproval.go` handle the rest — no changes to provider subpackages.

## The `shell_exec` Tool — Crest-Native Differentiator

This is the critical tool. Agent shell actions are **visible to the user as blocks**, not hidden subprocesses.

```go
type ShellExecInput struct {
    Cmd         string `json:"cmd"`
    Cwd         string `json:"cwd,omitempty"`
    TimeoutSec  int    `json:"timeout_sec,omitempty"` // default 120, max 600
    CloseOnExit bool   `json:"close_on_exit,omitempty"`
}

type ShellExecOutput struct {
    BlockId    string `json:"block_id"`    // user-visible, deep-linkable
    ExitCode   int    `json:"exit_code"`
    DurationMs int64  `json:"duration_ms"`
    StdoutTail string `json:"stdout_tail"` // last ~8 KiB, ANSI-stripped, UTF-8 repaired
    Truncated  bool   `json:"truncated"`
    TimedOut   bool   `json:"timed_out"`
}
```

**Execution flow** (inside `ToolAnyCallback`):

1. Gate on `do` mode approval policy.
2. Resolve cwd: input → `sess.Cwd` → parent block's `cmd:cwd` meta.
3. Build `waveobj.BlockDef{Meta: {view:"term", controller:"cmd", cmd:<Cmd>, "cmd:cwd":<cwd>, "cmd:runonstart":"true", "cmd:closeonexit":<CloseOnExit>}}` with connection from `sess.Connection`.
4. `wcore.CreateBlock(ctx, sess.TabId, blockDef, nil)` — returns new blockId.
5. Layout action: `LayoutActionData{ActionType: SplitVertical, TargetBlockId: sess.BlockId, Position:"after"}` — same pattern as `wshserver.CreateBlockCommand` (wshserver.go:267-280).
6. `blockcontroller.ResyncController(ctx, sess.TabId, newBlockId, nil, false)` to start.
7. Subscribe to `wps.Event_ControllerStatus` filtered by `newBlockId`; emit `ToolProgressDesc` with live status (`"running in block <id>, <s>s elapsed"`) so the overlay shows streaming progress via existing `data-toolprogress`.
8. Wait on completion: `cmdblock.LatestForBlock` polling with small backoff, or event subscription, until `state == StateDone` or ctx deadline.
9. Read tail: `filestore.WFS.ReadAt(newBlockId, "blockfile:term", cmdBlock.OutputStartOffset, size)` with 8 KiB window; strip ANSI; repair UTF-8; set `Truncated` if more bytes exist.
10. Populate `UIMessageDataToolUse.BlockId = newBlockId` so the overlay's tool-use chip can deep-link.

**Timeout**: on `ctx.Done()`, `blockcontroller.SendInput(newBlockId, &BlockInputUnion{InputData: "\x03"})` (SIGINT); if still running after 3 s, `blockcontroller.DestroyBlockController(newBlockId)`; mark `TimedOut`.

## Frontend Changes

All paths under `frontend/app/view/term/`.

### `term-model.ts`

- **New atom** (next to `termAgentChatId` at ~line 126):
  ```ts
  termAgentModeAtom: jotai.PrimitiveAtom<"ask" | "plan" | "do">
  ```
  Default: `"do"`. Initialized in `TermViewModel` constructor.

- **`handleTermAgentKeydown` (line 872)**: when composer opens and input begins with `:ask `, `:plan `, or `:do `, strip prefix and set `termAgentModeAtom`. Bare `:` opens composer with mode `do`.

- **`submitTermAgentPrompt` (~line 643)**: read `globalStore.get(this.termAgentModeAtom)` and include as `mode` in the request body, plus `cwd`, `connection`, `last_command` (already computed around lines 613-617 in `buildTermAgentPrompt`).

### `term-agent.tsx`

- **`DefaultChatTransport` (line 197)**: change `api` to `${getWebServerEndpoint()}/api/post-agent-message`.
- **`prepareSendMessagesRequest` (line 198)**: add `mode`, `blockid: model.blockId`, `context: {cwd, connection, last_command, recent_cmds}` to body.
- **Overlay header (~line 239)**: small mode chip ("ask" / "plan" / "do") reading from `termAgentModeAtom`.
- **No changes to message rendering** — AI-SDK v5 stream parts (`text`, `reasoning`, `data-tooluse`, `data-toolprogress`) are emitted by aiusechat's existing SSE writer. Approval UI (`TermAgentApprovalButtons` → `RpcApi.WaveAIToolApproveCommand`) works unchanged.

### No changes needed

- `frontend/app/aipanel/` — existing WaveAI panel continues to use `/api/post-chat-message` and is untouched.

## Request / Response

```go
// pkg/agent/http.go
type PostAgentMessageRequest struct {
    ChatID  string            `json:"chatid"`           // required UUID
    TabId   string            `json:"tabid"`
    BlockId string            `json:"blockid"`
    Mode    string            `json:"mode"`             // "ask" | "plan" | "do"
    Msg     uctypes.AIMessage `json:"msg"`
    AIMode  string            `json:"aimode"`           // provider selection (waveai@balanced etc.)
    Context AgentContext      `json:"context,omitempty"`
}

type AgentContext struct {
    Cwd         string   `json:"cwd,omitempty"`
    Connection  string   `json:"connection,omitempty"`
    LastCommand string   `json:"last_command,omitempty"`
    RecentCmds  []string `json:"recent_cmds,omitempty"` // up to 20 entries
}
```

**Handler flow** (`PostAgentMessageHandler`, modeled on `aiusechat.WaveAIPostMessageHandler` at usechat.go:635):
1. Decode + validate UUID chatid.
2. `modes.LookupMode(req.Mode)` — 400 on unknown.
3. Resolve `AIOptsType` (reuse aiusechat's settings resolution — factor a small helper if not already exported).
4. Build `Session{ChatID, TabId, BlockId, Mode, Cwd, Connection, LastCommand, RecentCmds}`.
5. `sse.MakeSSEHandlerCh(w, r.Context())`.
6. `agent.RunAgent(ctx, sse, AgentOpts{...})`.

**Response**: existing AI-SDK v5 SSE stream produced by `aiusechat` — no new format.

**Route registration** in `pkg/web/web.go` beside line 454:
```go
gr.HandleFunc("/api/post-agent-message", WebFnWrap(WebFnOpts{AllowCaching: false}, agent.PostAgentMessageHandler))
```

## Browser Tool Hook (MVP Placeholder)

- `pkg/agent/registry.go`: `const BrowserToolNamespace = "browser"`. Reserve `browser.navigate`, `browser.screenshot`, `browser.click`, `browser.read_text`.
- `pkg/agent/tools/browser_stub.go`: registers each reserved name with `ToolAnyCallback` returning `{"error": "browser tools require Phase 2 enablement"}` and `RequiredCapabilities: ["browser"]` so `HasRequiredCapabilities` filters them from the default tool list until enabled.
- `const ApprovalCategoryBrowser = "browser"` so Phase 2 can slot a category-grouped approval UI without schema migration.
- `Mode.ToolNames` supports globs (`"browser.*"`); no mode uses it in MVP.

## Phase Breakdown

### Phase 1 — MVP (~4 weeks)

- **Week 1**: `pkg/agent/` skeleton, `modes.go`, `prompts/` (extract + rewrite three Forge prompts, preserve attribution), HTTP handler registered, chatstore namespacing.
- **Week 2**: tool registry + read-only adapters (`read_file`, `list_dir`, `cmd_history`, `get_scrollback`, `write_plan`). End-to-end `:ask` and `:plan`.
- **Week 3**: `shell_exec`, `write_file`/`edit_file` adapters, `create_block`, `focus_block`. `:do` end-to-end with approvals. Browser stub.
- **Week 4**: frontend mode chip + prefix parsing, `NOTICE` attribution, telemetry wiring (reuse `recordChatEvent`), test coverage.

### Phase 2 (~4 weeks)

- Browser tool implementation — new RPC `WebInteractCommand` with CDP via `webContents.debugger` (or injected JS for click/fill MVP), slotted into reserved registry.
- External MCP client (stdio + SSE), tool enumeration + dynamic registration.
- Skills integration: expose `.kilocode/skills/` as agent-invokable library.
- Refined prompts + approval policies based on dogfood signal.
- Eval harness: replay golden transcripts + sample terminal-bench tasks.

### Phase 3 (stretch)

- Git worktree sandboxing for `:do`.
- Conversation export / import (`.crest-agent.json`).
- Local embedding-based repo search (replacing Forge's `api.forgecode.dev` dependency).
- Multi-block coordinated tasks, plan → execution handoff.

## Verification

### Unit tests

- `pkg/agent/modes_test.go` — mode lookup, tool filtering, approval policy resolution.
- `pkg/agent/tools/shell_exec_test.go` — spawn test tab + block via `wcore.CreateBlock`, run `echo hi`, assert `exit_code=0`, stdout tail non-empty; timeout path (`sleep 10` with 1 s timeout).
- `pkg/agent/tools/cmd_history_test.go` — seed cmdblock rows, assert query shape.
- `pkg/agent/http_test.go` — POST validation (missing chatid, invalid mode, malformed context).

### Existing suites must pass unchanged

- `pkg/aiusechat/...`, `pkg/blockcontroller/...`, `pkg/cmdblock/...`, `pkg/wcore/...`, `pkg/aiusechat/tools_readdir_test.go`, `pkg/aiusechat/usechat_mode_test.go`.

### Manual E2E

- `:ask how is logging wired here` → markdown response, no filesystem writes, no approval banners.
- `:plan add retry to RunAIChat` → writes `.crest-plans/add-retry-to-runaichat.md`, opens preview block next to terminal.
- `:do run the unit tests` → agent calls `shell_exec("go test ./pkg/agent/...")`, approval chip appears in overlay, user approves, new cmd-block appears adjacent, exit code + tail returned to chat.
- `:do` with denied approval → tool returns structured rejection, agent continues or asks.
- Background-tab approval: issue a `:do` in tab A, switch to tab B → approval surfaces via notification toast (existing `NotificationsModel` path).
- Regression: bare `:` in overlay still works with mode `do` default.

## Critical Files

**Modify:**
- `pkg/web/web.go:454` — register new route.
- `frontend/app/view/term/term-model.ts` — mode atom, prefix parsing, submit includes mode.
- `frontend/app/view/term/term-agent.tsx` — transport endpoint, request body, mode chip.

**Create:**
- Entire `pkg/agent/` package (layout above).
- `pkg/agent/NOTICE` + root `NOTICE` addition.

**Read / reference (do not modify):**
- `pkg/aiusechat/usechat.go` — step loop + handler pattern.
- `pkg/aiusechat/uctypes/uctypes.go` — `ToolDefinition`, `WaveChatOpts`, stream types.
- `pkg/aiusechat/toolapproval.go` — approval registry.
- `pkg/aiusechat/tools_readfile.go` / `tools_readdir.go` / `tools_writefile.go` / `tools_term.go` — adapter sources.
- `pkg/blockcontroller/blockcontroller.go` — `ResyncController`, `SendInput`, `GetBlockControllerRuntimeStatus`, `DestroyBlockController`.
- `pkg/cmdblock/store.go` — `GetByBlockID`, `LatestForBlock`.
- `pkg/wcore/block.go` — `CreateBlock`.
- `pkg/wshrpc/wshserver/wshserver.go:229` — `CreateBlockCommand` reference pattern.

## Risks & Open Questions

- **Forge prompt drift**: pin ForgeCode commit SHA in `pkg/agent/prompts/UPSTREAM.md`; quarterly re-sync cadence; `scripts/agent/diff-forge-prompts.sh` to help the diff review.
- **License attribution mechanics**: `pkg/agent/NOTICE` + top-of-file comment in each `prompts/*.md`; root `NOTICE` references it. Apache 2.0 preserved.
- **`pkg/agent` vs `pkg/aiusechat` overlap**: split documented in `pkg/agent/README.md` as "policy vs mechanism"; a CI lint forbids `pkg/aiusechat` importing `pkg/agent`.
- **Endpoint convergence**: `/api/post-chat-message` remains for WaveAI panel; `/api/post-agent-message` for terminal agent. Convergence = Phase 3 decision behind a feature flag.
- **chatstore isolation**: use key prefix `"agent:"` to avoid conversation cross-contamination when a user uses both surfaces simultaneously.
- **Open**: whether `write_plan` should create a Crest preview block automatically or only write the file (user opens manually). Default to auto-open for discoverability; revisit after dogfood.
- **Open**: max `recent_cmds` to inject — 20 entries × ~200 chars = ~4 KB, acceptable for all providers. Reconsider if token budget becomes tight.
