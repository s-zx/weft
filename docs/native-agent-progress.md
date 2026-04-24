# Crest Native Go Coding Agent — Progress Tracker

Branch: `feat/native-agent`

## Phase 1 — MVP ✅

### Core Implementation
- [x] `pkg/agent/` — 3 modes (ask/plan/do), 10 tools, HTTP handler, prompts, registry, session, context
- [x] `tools/shell_exec.go` — visible cmd-block, poll completion, ANSI-strip tail, SIGINT timeout
- [x] `tools/create_block.go` — term/preview/web block with split positioning
- [x] `tools/write_plan.go` — `.crest-plans/<slug>.md` + auto-open preview
- [x] Chatstore isolation (`"agent:"` prefix), telemetry source tagging
- [x] 38 unit tests across `pkg/agent/` and `pkg/agent/tools/`

### Frontend Integration
- [x] Agent overlay integrated into `termblocks/` (the active terminal view)
- [x] `:` key opens composer on empty input, Esc closes overlay
- [x] Real `<input>` element with auto-focus, Enter/Esc handling
- [x] Mode chip (ask/plan/do) derived from input prefix
- [x] AI Provider settings UI in Settings sidebar

### AI Provider Configuration
- [x] Visual form: provider dropdown, API key (OS keychain), model, advanced (base URL, max tokens)
- [x] `ai:apitokensecretname` added to SettingsType
- [x] Settings fallback: `resolveAgentAIOpts()` tries waveai mode system, falls back to `settings.json`
- [x] Full endpoint URLs for all providers

### E2E Verified
- [x] `:ask hello` → AI response in overlay (OpenRouter)

## Wave Legacy Cleanup ✅

| Step | Removed | Lines |
|------|---------|-------|
| `pkg/wcloud/` | Cloud telemetry upload | -396 |
| Preset system | `AiSettingsType`, preset files, schema | -241 |
| WaveAI panel | `aipanel/` 18 files + 30 downstream refs | -4542 |
| Cloud provider | `AIProvider_Wave`, X-Wave headers, rate limit, premium fallback | -490 |
| Cloud modes | `waveai@quick/balanced/deep`, mode broadcaster | -85 |
| Remaining artifacts | wsh view type, meta constants, telemetry fields | -41 |
| **Total** | | **~5800 lines removed** |

## Phase 2 — MCP + Browser

### MCP Client ✅
- [x] `mcp-go` v0.49 dependency (stdio, SSE, streamable HTTP transports)
- [x] `MCPServerConfig` type + `ai:mcpservers` settings key
- [x] `pkg/agent/mcp/bridge.go` — MCP Tool → ToolDefinition conversion, `mcp__<server>__<tool>` namespacing
- [x] `pkg/agent/mcp/manager.go` — singleton MCPManager, lazy init, config watcher, server lifecycle
- [x] Wired into `ToolsForMode()` — MCP tools appended in mutation modes
- [x] App shutdown integration — `MCPManager.Shutdown()` in server doShutdown
- [x] System prompt updated with MCP tool guidance
- [x] 7 unit tests for bridge (name parsing, tool conversion, error handling, text extraction)

### Skills Integration ✅
- [x] `pkg/agent/skills.go` — discovers `.kilocode/skills/*/SKILL.md`, parses YAML frontmatter
- [x] `BuildSkillsContext()` injects `<available_skills>` block into system prompt
- [x] Agent auto-discovers 8 skills (add-config, add-rpc, add-wshcmd, context-menu, create-view, electron-api, waveenv, wps-events)
- [x] 7 unit tests (discovery, frontmatter parsing, context building, edge cases)

### Browser Tools ✅
- [x] `browser.navigate` — updates `meta.url` on web block, frontend re-renders `<webview>`
- [x] `browser.read_text` — reads DOM via `WebSelectorCommand` → `executeJavaScript()`
- [x] `browser.click` — clicks element via new `WebClickCommand` → Electron handler
- [x] `browser.screenshot` — captures webview viewport via new `WebScreenshotCommand` → `capturePage()`
- [x] New RPC types: `CommandWebClickData`, `CommandWebScreenshotData`
- [x] New Electron handlers: `handle_webclick`, `handle_webscreenshot` in `emain-wsh.ts`
- [x] `webClickElement()`, `webScreenshot()` helpers in `emain-web.ts`
- [x] All 4 tools wired into `ModeDo`, all require user approval
- [x] 10 unit tests (input parsing, tool definitions, capabilities)

### Eval Harness ✅
- [x] Golden transcript replay framework (`pkg/agent/eval/`)
  - MockBackend implementing `UseChatBackend` — queued responses, real tool execution
  - `RunGoldenTest()` engine — loads JSON transcripts, sets up temp workspace, runs with auto-approved tools
  - `{{CWD}}` substitution for absolute paths in tool inputs
  - `RunAllGoldenTests()` auto-discovers `*.golden.json` files
  - 3 golden transcripts: `ask-read-file`, `ask-list-dir`, `do-write-file`
  - 5 unit tests total
- [x] Terminal-bench 2.0 Harbor adapter (`eval/harbor/`)
  - `CrestAgent` installed agent — builds `wavesrv` in container, POSTs to agent HTTP API
  - Prompt template, ATIF trajectory output, README with usage docs
  - Runnable via `harbor run --agent-import-path eval.harbor.crest_agent:CrestAgent`

## Phase 3 — Stretch ⬜

- [ ] Git worktree sandboxing for `:do`
- [ ] Conversation export/import (`.crest-agent.json`)
- [ ] Local embedding-based repo search
- [ ] Multi-block coordinated tasks, plan → execution handoff
- [x] ~~Rename Go module path `wavetermdev/waveterm` → `s-zx/crest`~~ (done — 265 files, mechanical sed + regenerate)

## Optimization (Tier 1 — production hardening)

Tracking the prioritized roadmap in [`agent-optimization-roadmap.md`](./agent-optimization-roadmap.md).

- [x] LLM retry with exponential backoff — `pkg/aiusechat/httpretry/` wraps `httpClient.Do` for all 4 backends (anthropic, openai responses, gemini, openaichat). Retries 429/5xx and transport errors with equal-jitter backoff, honors `Retry-After`, capped at MaxBackoff. 18 unit tests.
- [x] Step budget enforcement — `MaxSteps` on WaveChatOpts, default 50 for agent. Hard stop at limit, soft system-prompt warning at 80%. 3 tests.
- [x] Context compaction — `ContextBudget` on WaveChatOpts, default 100k tokens. When last step's `input_tokens` exceeds 80% of budget, drops middle messages from chatstore (keeps first + last 10). `CompactMessages()` on ChatStore. 4 tests.
- [x] Dangerous command detection — 12 regex patterns in `IsDangerousCommand()` covering rm -rf, force push, hard reset, pipe-to-shell, dd, mkfs, chmod 777, etc. Wired into shell_exec via `ToolVerifyInput` — forces approval even when mode auto-approves. 43 test cases.
- [x] Structured audit log — `ToolAuditEvent` type on `AIMetrics.AuditLog`. Each tool call emits timestamp, chatId, tool name, callId, truncated input, approval, duration, outcome, error. 1 test.

## Architecture

- **`pkg/agent` = policy, `pkg/aiusechat` = mechanism.** One-way dependency.
- **Tool adapters** wrap `aiusechat.GetXxxToolDefinition()` + inject mode-aware approval closures.
- **`shell_exec`** creates user-visible cmd-blocks — the Crest differentiator.
- **`TermAgentModel` interface** — decouples overlay from any specific ViewModel.
- **Settings fallback** — agent tries waveai mode system first, then reads `settings.json` directly.
- **API keys via secretstore** — stored in OS keychain, referenced by `ai:apitokensecretname`.
- **ForgeCode attribution**: Apache 2.0 preserved in `NOTICE` files + `UPSTREAM.md`.
- **MCP client** — `pkg/agent/mcp/` manages external MCP server connections. Config via `ai:mcpservers` in `settings.json`. Tools namespaced as `mcp__<server>__<tool>`, always require approval.
- **Skills** — `pkg/agent/skills.go` scans `.kilocode/skills/` at runtime, injects skill names + descriptions into the system prompt so the agent knows which guides are available.
- **Module path** — `github.com/s-zx/crest` (renamed from `wavetermdev/waveterm`).
- **Browser tools** — 4 tools (`browser.navigate/read_text/click/screenshot`) use Electron's webContents via RPC. Navigate updates block meta; read/click/screenshot route through `emain-wsh.ts` Electron handlers to the `<webview>` webContents.
- **Eval harness** — `pkg/agent/eval/` provides golden transcript replay (mock LLM, real tools, JSON test cases). `eval/harbor/` provides a terminal-bench 2.0 adapter for running Crest on the Harbor benchmark framework.
- **HTTP retry** — `pkg/aiusechat/httpretry/` is the shared retry wrapper used by every backend before the SSE stream starts. Retry happens at the HTTP layer only; once headers come back with a non-retryable status (typically 200 + `text/event-stream`), the caller owns the stream and any mid-stream error is surfaced to the user without retry (re-emitting partial deltas would corrupt the UI).
