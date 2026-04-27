# Claude-Code Parity — Optimization Tracking

Living document for the Crest native agent → Claude Code parity sprint.
Companion to [`agent-optimization-roadmap.md`](./agent-optimization-roadmap.md)
(broader vision); this one tracks the **current optimization effort** with
phase-by-phase status, completed work, and what's queued next.

Reference: `/Users/user/Documents/Claude-Code/` source.

---

## Status Snapshot

| # | Workstream | Status | Notes |
|---|---|---|---|
| 1 | Reliability hardening (Phases 1-4) | ✅ shipped | commit `0ce9f60b` — see §1 |
| 2 | Context Governance v2 (remainder) | ✅ shipped | context collapse + richer summary — see §2 |
| 3 | Permissions v2 (design) | ✅ design approved | see [`permissions-v2-design.md`](./permissions-v2-design.md); ready to implement |
| 4 | Agent Task Runtime v2 | ⏳ queued | background tasks, lifecycle, UI surface — see §4 |
| 5 | Command Layer v1 | ⏳ queued | real slash commands, autocomplete — see §5 |
| 6 | Memory System (P1) | 📋 planned | hierarchical CLAUDE.md, auto-extract — see §6 |
| 7 | MCP v2 (P1) | 📋 planned | resources, auth, reconnect — see §7 |
| 8 | Tool补齐 (P1) | 📋 planned | LSP, web search — see §8 |

Legend: ✅ done · 🚧 in progress · 📐 designing · ⏳ queued · 📋 planned

---

## §1 — Reliability Hardening (shipped, commit `0ce9f60b`)

Eleven concrete gaps closed across four phases. Source pointers below trace
each item back to the Claude Code reference behavior we matched.

### Phase 1 — API Reliability

| Item | Crest file | Claude Code reference |
|---|---|---|
| 1.1 API retry w/ exp backoff (was already done) | `pkg/aiusechat/httpretry/httpretry.go` | `src/services/api/withRetry.ts` |
| 1.2 Max-tokens recovery (escalate → resume×3) | `pkg/aiusechat/usechat.go` (loop) | `src/query.ts` max-output-tokens recovery |
| 1.3 Reactive compact on context-length errors | `pkg/aiusechat/usechat.go` (loop) | `src/services/compact/reactiveCompact.ts` |

### Phase 2 — Context Management

| Item | Crest file | Claude Code reference |
|---|---|---|
| 2.1 Tool result spill to disk | `pkg/aiusechat/tool_spill.go` (new) | `src/services/toolResultBudget` |
| 2.2 Microcompact tier (60% threshold) | `pkg/aiusechat/usechat.go` (loop) | `src/services/compact/microCompact.ts` |

### Phase 3 — System Prompt & Tool Structure

| Item | Crest file | Claude Code reference |
|---|---|---|
| 3.1 Per-tool `Prompt` field + populated for 6 tools | `pkg/aiusechat/uctypes/uctypes.go`, `pkg/agent/tools/*.go` | each tool's `prompt.ts` |
| 3.2 Static/dynamic prompt boundary | `pkg/agent/agent.go` | `SYSTEM_PROMPT_DYNAMIC_BOUNDARY` |
| 3.3 "Executing actions with care" section | `pkg/agent/prompts/shared_header.md` | `getActionsSection` |

### Phase 4 — Subagent & Safety

| Item | Crest file | Claude Code reference |
|---|---|---|
| 4.1 spawn_task returns final assistant text | `pkg/agent/tools/spawn_task.go` | `src/tools/AgentTool/runAgent.ts` |
| 4.2 File mtime tracking (refuse stale edits) | `pkg/agent/tools/file_tracker.go` (new) | `FILE_UNEXPECTEDLY_MODIFIED_ERROR` |
| 4.3 Tool error classification (`ErrorType`) | `pkg/aiusechat/uctypes/uctypes.go`, `usechat.go` | `classifyToolError` |

---

## §2 — Context Governance v2 (shipped)

Three escalating tiers in the chat loop, picked by % of `ContextBudget`:

| Tier | Threshold | Action | Where |
|---|---|---|---|
| collapse | 50% | replace old tool result *content* with a placeholder, keep messages | `chatstore.CollapseOldToolResults` + per-backend `CollapseToolResults` |
| microcompact | 60% | drop whole older messages, no summary | `chatstore.CompactMessages` |
| heavy | 80% | drop + inject summary including files modified | `chatstore.CompactMessagesWithSummary` + `extractTouchedFilesFromAudit` |

### 2a. Context collapse

`ToolResultCollapsible` interface added to `uctypes`; implemented on
each backend's tool-result-bearing message type:
- Anthropic: rewrites `tool_result` block `Content` field
- OpenAI Chat: rewrites `role:"tool"` message `Content`
- Gemini: rewrites `functionResponse.Response` to `{"output": placeholder}`
- OpenAI Responses: rewrites `function_call_output.Output`

ToolUseID / call_id / function name preserved so tool_use ↔ tool_result
pairing stays valid. Tier runs at 50% of `ContextBudget`, keepLast=15.

### 2b. Richer compaction summary

`extractTouchedFilesFromAudit` walks `metrics.AuditLog` for write/edit/
multi_edit calls, parses `filename` from InputArgs (with truncation
recovery for cut-off JSON), and the heavy-tier summary now appends
`Files modified during this conversation: ...` so the model retains a
list of files it has worked on across compaction.

---

## §3 — Permissions v2

**Status:** design doc approved 2026-04-27 — see
[`permissions-v2-design.md`](./permissions-v2-design.md). All 4 open
questions resolved; ready to implement.

**Highlights of the design:**
- Rule grammar: tool-name + content matcher (`shell_exec(prefix:npm)`,
  `edit_text_file(/path/**)`); path globs + shell prefixes + exact
- Five scopes (highest precedence first): cliArg → user →
  sharedProject (`.crest/permissions.json`) → localProject
  (`.crest/permissions.local.json`) → session
- Modes become rule presets, not parallel rule namespaces — one rule
  pool, modes flip default posture
- **Mode and Permission Posture are split into two orthogonal axes**
  (this was the bigger structural lesson from Claude Code's design):
  - Mode (work axis): `ask` / `plan` / `do` — visible in the mode
    picker. `bench` is a fourth mode but hidden, API-only, used by
    eval harnesses.
  - Posture (strictness axis): `default` / `acceptEdits` /
    `bypassPermissions` — toggled via Shift+Tab cycle or
    `/permission` slash command. Status pill shows current posture in
    the agent overlay header. Bench mode implicitly forces a hidden
    `bench` posture that skips even bypass-immune safety checks.
  - Posture is per-chat state with `defaultPosture` setting for new
    chats. Posture only affects calls the rule engine would otherwise
    *ask* about — bypass-immune paths still prompt under any
    user-selectable posture.
- Bypass-immune safety checks: `.git/`, `.ssh/`, `.env`, `rm -rf /`,
  `curl|sh`, `sudo`, etc. force a prompt in `bypassPermissions`.
- Approval prompt default "save to" = `session` (least commitment).
- v1 ships **without** classifier (defer to v2); without
  PermissionRequest hooks (no hook framework yet); without policy tier

**Implementation order in design doc §5.3.** ETA: 2 sittings for core
(parser → matchers → store → engine → tool adapters → wire-in), 1
sitting for UI polish + defaults.

---

## §4 — Agent Task Runtime v2 (queued)

**Goal:** spawn_task today is one-shot synchronous. Claude has full
agent-task lifecycle: background, status, stop, continue, monitor.

**Backend work:**
- Task registry (`pkg/agent/taskregistry/`) keyed by task ID, with
  status transitions: pending → running → completed/canceled/error.
- Lifecycle: start, stop, query, list. Background tasks run in their
  own goroutine + context.
- Persistence: at least in-memory across the process; persist to disk
  out of scope for v1.
- New tools: `task_get`, `task_list`, `task_stop`, `task_output`.

**Frontend work (~50% of effort):**
- Tasks panel UI (sidebar or overlay tab) showing live tasks.
- Stop/continue buttons per task, output preview.
- Notification when a background task finishes.

**Claude reference:**
- `src/tasks/LocalAgentTask/LocalAgentTask.tsx`
- `src/tools/AgentTool/AgentTool.tsx` (background mode)

**Decision needed:** is this worth shipping without the UI? Backend
alone is 70% of code but 0% of user-visible value.

---

## §5 — Command Layer v1 (queued)

**Goal:** real command palette like Claude's slash commands. Today the
overlay does prefix detection (`ask:`, `plan:`, `do:`) — no
autocomplete, no help, no plugin commands.

**Scope for v1:**
- Command registry with `name`, `description`, `args`, `handler`.
- Autocomplete on `/<typing>` showing matching commands.
- `/help` lists all commands.
- Plugin command discovery from a known directory.
- Built-in commands: `/help`, `/clear`, `/model`, `/mode`, `/undo`,
  `/worktree` (some already exist as ad-hoc handlers — unify).

**Out of scope for v1:** plugin auth, hot-reload, command parameters
beyond positional strings.

**Claude reference:** `src/commands.ts:258`, `src/utils/plugins/loadPluginCommands.ts`

---

## §6-8 — P1 (planned, deferred)

- **Memory:** hierarchical CLAUDE.md discovery + auto-memory extraction
  via forked-agent. We have flat project guidelines today; Claude's
  layered system is large but high-leverage.
- **MCP v2:** resources read/write, OAuth flows, reconnect logic. Today
  it's stdio-only with manual config.
- **Tool补齐:** LSP integration (definition, references, diagnostics),
  first-class `web_search` (currently only `web_fetch`).

---

## Decisions Log

| Date | Decision | Why |
|---|---|---|
| 2026-04-27 | Skip token-budget continuation (Claude `+500k` syntax) | Niche; pending-todos nudge already covers the practical case |
| 2026-04-27 | Microcompact via message deletion, not content rewrite | Backend-agnostic; `MessageDependsOnPrev` already preserves tool pair integrity |
| 2026-04-27 | spawn_task returns final assistant text via `ConvertAIChatToUIChat` | Avoids needing a new method on the Backend interface |
| 2026-04-27 | Context collapse via opt-in `ToolResultCollapsible` interface, not a Backend method | Same pattern as `MessageDependsOnPrev`; only the message types that carry tool results need to opt in, no new method on the Backend interface |
| 2026-04-27 | Heavy-summary file list pulled from `metrics.AuditLog`, not chatstore-tracked | Audit log already exists; threading it into chatstore would need an interface change for marginal benefit |
| 2026-04-27 | Permissions v2 — `bench` and `bypassPermissions` are distinct modes | They look similar (auto-approve) but serve different audiences. Bench has *no* safety checks (eval harness can do whatever); bypass keeps bypass-immune ones (interactive "trust me"). Bench hidden from user picker so a casual user can't trip into a no-safety mode. |
| 2026-04-27 | Permissions v2 — Mode and Posture are orthogonal axes | Conflating them (single picker with `ask`/`plan`/`do`/`bypassPermissions`) mixes work mode with permission strictness. Split: Mode = tools/prompt/budget; Posture = strictness. Posture set: `default` / `acceptEdits` / `bypassPermissions` + hidden `bench`, toggled via Shift+Tab. |
| 2026-04-27 | Permissions v2 — rules in standalone `pkg/agent/permissions` package | Clean cycle story; engine reusable beyond the agent loop |
| 2026-04-27 | Permissions v2 — defer classifier to v2 | Rules-first ships value sooner; classifier needs prompt design + gating |

---

## Working Notes

- Each shipped item should match a pointer in this doc's status table —
  if it doesn't, either the doc is stale or the work was unscoped.
- Before adding any new "should match Claude" item, check whether it
  pays off for the *terminal* use case. Some Claude features (REPL
  mode, push notifications, cron) make sense for a CLI agent but not
  necessarily for Crest's terminal-block model.
- Keep this doc < 250 lines. Detail belongs in feature-specific design
  docs (e.g. `permissions-v2-design.md`) — this is an index.
