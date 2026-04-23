# Crest Native Go Coding Agent — Progress Tracker

Branch: `feat/native-agent` | First commit: `070ea0a0`

## Phase 1 — MVP

### Week 1–2: Skeleton + Read-Only Tools ✅

- [x] `pkg/agent/` package skeleton
- [x] `modes.go` — `ask` / `plan` / `do` modes, `ApprovalPolicy`, `LookupMode`, `ResolveApproval`
- [x] `session.go` — `Session{ChatID, TabID, BlockID, Mode, Cwd, Connection, ...}`
- [x] `context.go` — `BuildTerminalContext(sess)` renders `<terminal_context>` for system prompt
- [x] `prompts/` — `shared_header.md`, `ask.md`, `plan.md`, `do.md`, `UPSTREAM.md` (Forge-attributed)
- [x] `prompts.go` — `//go:embed` loader, `SystemPromptForMode()`
- [x] `registry.go` — `ToolsForMode(sess)`, `buildTool()`, `approvalResolver()`
- [x] `agent.go` — `RunAgent()` → composes `WaveChatOpts`, calls `WaveAIPostMessageWrap`
- [x] `http.go` — `PostAgentMessageHandler` at `/api/post-agent-message`
- [x] `pkg/aiusechat/usechat.go` — exported `GetWaveAISettings()` helper
- [x] `pkg/web/web.go` — route registered
- [x] Read-only tool adapters: `read_text_file`, `read_dir`, `get_scrollback`, `cmd_history`
- [x] `tools/browser_stub.go` — reserves `browser.*` namespace for Phase 2
- [x] Frontend: `term-model.ts` — mode atom, prefix parsing (`:ask`/`:plan`/`:do`), pending mode+context fields
- [x] Frontend: `term-agent.tsx` — new transport endpoint, mode chip in overlay header
- [x] `NOTICE` (root) + `pkg/agent/NOTICE` — Apache 2.0 ForgeCode attribution
- [x] `go vet ./...` clean, `tsc --noEmit` clean on modified files

### Week 3–4: Mutation Tools + Do Mode ✅

- [x] `tools/write_file.go` — `WriteTextFile` + `EditTextFile` adapters
- [x] `tools/shell_exec.go` — creates visible cmd-block, polls `BlockControllerRuntimeStatus`, SIGINT timeout, ANSI-strip tail
- [x] `tools/write_plan.go` — writes `.crest-plans/<slug>.md`, optional auto-open preview block
- [x] `tools/create_block.go` — term/preview/web block creation with split positioning
- [x] `tools/focus_block.go` — `setblockfocus` RPC to tab route
- [x] `registry.go` — all new tools wired into `buildTool()` switch
- [x] `go vet ./...` clean (full workspace)
- [x] Committed: `070ea0a0`

### Week 4 remaining: Polish ⬜

- [ ] Telemetry wiring — reuse `recordChatEvent` for agent tool calls
- [ ] `chatstore.go` — namespaced wrapper (`"agent:"+chatId`) to isolate agent conversations
- [ ] Unit tests: `modes_test.go`, `http_test.go`, `cmd_history_test.go`, `shell_exec_test.go`
- [ ] Manual E2E smoke test (requires running Crest):
  - [ ] `:ask how is logging wired` → markdown, no writes, no approval
  - [ ] `:plan add retry to RunAIChat` → writes plan file, opens preview block
  - [ ] `:do run the unit tests` → approval chip → cmd-block → exit code in chat
  - [ ] Denied approval → structured rejection
  - [ ] Bare `:` defaults to `do` mode

## Phase 2 — Browser + MCP (~4 weeks) ⬜

- [ ] Browser tool implementation — CDP via `webContents.debugger`, slotted into reserved `browser.*` registry
- [ ] External MCP client (stdio + SSE), tool enumeration + dynamic registration
- [ ] Skills integration: `.kilocode/skills/` as agent-invokable library
- [ ] Refined prompts + approval policies from dogfood signal
- [ ] Eval harness: golden transcript replay + terminal-bench tasks

## Phase 3 — Stretch ⬜

- [ ] Git worktree sandboxing for `:do`
- [ ] Conversation export/import (`.crest-agent.json`)
- [ ] Local embedding-based repo search
- [ ] Multi-block coordinated tasks, plan → execution handoff
- [ ] Endpoint convergence (`/api/post-chat-message` ↔ `/api/post-agent-message`)

## File Inventory

```
pkg/agent/
├── NOTICE
├── agent.go
├── context.go
├── http.go
├── modes.go
├── prompts.go
├── registry.go
├── session.go
├── prompts/
│   ├── UPSTREAM.md
│   ├── ask.md
│   ├── do.md
│   ├── plan.md
│   └── shared_header.md
└── tools/
    ├── browser_stub.go
    ├── cmd_history.go
    ├── create_block.go
    ├── focus_block.go
    ├── get_scrollback.go
    ├── list_dir.go
    ├── read_file.go
    ├── shell_exec.go
    ├── write_file.go
    └── write_plan.go

Modified:
  frontend/app/view/term/term-agent.tsx
  frontend/app/view/term/term-model.ts
  pkg/aiusechat/usechat.go
  pkg/web/web.go
  NOTICE (root)
```

## Architecture Decisions

- **`pkg/agent` = policy, `pkg/aiusechat` = mechanism.** One-way dependency: agent imports aiusechat, never the reverse.
- **Tool adapters** wrap existing `aiusechat.GetXxxToolDefinition()` and inject mode-aware approval closures.
- **`shell_exec`** creates user-visible cmd-blocks (not hidden subprocesses) — the Crest differentiator.
- **Mode prefix** (`:ask`, `:plan`, `:do`) is parsed in `term-model.ts` via derived atom; stripped before sending to backend.
- **ForgeCode attribution**: Apache 2.0 preserved in `NOTICE` files + `UPSTREAM.md` with pinned commit SHA.

## Open Questions

- `chatstore.go` namespacing (`"agent:"+chatId`) — not yet implemented, conversations share the default store
- `write_plan` auto-opens preview block; revisit after dogfood if too intrusive
- `recent_cmds` cap at 20 entries (~4KB); watch token budget
- Phase 3 endpoint convergence behind feature flag
