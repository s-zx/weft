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

### Remaining
- [ ] Eval harness: golden transcript replay + terminal-bench tasks

## Phase 3 — Stretch ⬜

- [ ] Git worktree sandboxing for `:do`
- [ ] Conversation export/import (`.crest-agent.json`)
- [ ] Local embedding-based repo search
- [ ] Multi-block coordinated tasks, plan → execution handoff
- [x] ~~Rename Go module path `wavetermdev/waveterm` → `s-zx/crest`~~ (done — 265 files, mechanical sed + regenerate)

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
