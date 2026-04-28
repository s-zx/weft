# Crest Native Agent — Optimization Roadmap

> Reference document for evolving Crest's native agent from MVP (`feat/native-agent`) to production quality.
> Companion to [`native-agent-progress.md`](./native-agent-progress.md) (what's built) — this doc covers **what's missing and why**.

---

## 1. Gap Analysis (MVP → Production)

Seven dimensions, each with concrete gaps observed in the current implementation vs. what a production-grade terminal coding agent needs.

### 1.1 Core Capabilities

| Gap | Current state | Target |
|-----|---------------|--------|
| Sub-agent delegation | Single monolithic loop | Spawn sub-agents for scoped tasks (explore, plan, verify) with isolated context |
| Background task execution | All tools block the step | Long-running jobs (builds, servers) run in background, agent polls / subscribes |
| Interactive clarification | Agent cannot ask the user mid-task | `ask_user` tool that pauses the loop and waits for a human reply |
| Rich file editing | `write_text_file` overwrites whole files | Search/replace, line-range, multi-hunk patch, apply-diff |
| Multi-file coordinated edits | One file at a time | Transactional batch edits with preview + rollback |
| Terminal session persistence | Each `shell_exec` is stateless | Long-lived shell sessions (env vars, cwd, activated venvs) |
| Web search | Not built in | First-class `web_search` tool, integrated with provider or external API |
| Task resumption | Conversation lost on crash | Persist step state; resume mid-task after restart |

### 1.2 Architecture

| Gap | Current state | Target |
|-----|---------------|--------|
| Tool plugin system | Hard-coded registry in `pkg/agent/registry.go` | Runtime-discovered plugins (beyond MCP) with manifest + schema |
| Observability | `log.Printf` scattered throughout | Structured events (JSON), trace IDs, OpenTelemetry export |
| Pluggable memory / context store | In-memory only | Abstraction over chat history backend (SQLite, file, remote) |
| Model router / fallback | One provider at a time | Route per-mode, fallback on outage, cost-aware selection |
| Tool sandboxing | `shell_exec` has direct host access | Opt-in containers / chroot / VM-per-task for `:do` |
| Streaming abstractions | Tight coupling between SSE and step loop | Clear split: model stream → event bus → frontend |

### 1.3 Reliability

| Gap | Current state | Target |
|-----|---------------|--------|
| Retry on LLM failure | Request fails → loop dies | Exponential backoff on 429/5xx/network with jitter, max-retry |
| Step budget | Unbounded | Configurable `max_steps` (default 50), soft warning, hard stop |
| Context compaction | Passes full history every turn | Auto-compact at 80% of model's window, summarize older turns |
| Tool timeouts | Hardcoded per-tool | User-configurable defaults + per-call override |
| Partial failure recovery | One bad tool → whole task dies | Classify errors: recoverable (retry), actionable (surface to model), fatal (abort) |
| Deterministic error messages | Raw error strings bubble up | Normalized error taxonomy the model has been trained on |
| Provider outage handling | Crashes | Graceful degrade: switch model, queue, or surface "AI down" banner |

### 1.4 Safety

| Gap | Current state | Target |
|-----|---------------|--------|
| Permission model | Per-tool approval only | Per-tool + per-path + per-session policies, remembered choices |
| Dangerous command detection | None | Pattern list (`rm -rf /`, `git push --force`, `curl \| sh`) → force approval |
| Path allowlists | None | Optional allow/deny lists for read/write tools |
| Credential scrubbing | Logs may contain API keys / tokens | Redact known patterns in telemetry and chat export |
| Audit trail | Partial (chatstore) | Structured log of every tool call: who, what, when, approved-by, result |
| Git safety | Model can do anything | Block force-push to main/master, warn on destructive ops, never `--no-verify` without explicit user consent |
| Network egress controls | None | Optional deny-list / allow-list for tool-initiated outbound traffic |

### 1.5 Performance

| Gap | Current state | Target |
|-----|---------------|--------|
| Prompt caching | Not used | Enable Anthropic prompt cache on system prompt + stable history prefix |
| Parallel tool execution | Sequential | Run independent tool calls concurrently (same step) |
| Streaming tool output | Tool completes → result sent | Incremental stream to frontend (and optionally to model on long runs) |
| Token / cost accounting | Not tracked per session | Live counter surfaced in UI, persisted per conversation |
| Model selection by task | Always same model | Cheap model for routing/classification, strong model for synthesis |
| Context pruning | None | Truncate huge tool results (with "see full output" expansion) |

### 1.6 Developer Experience

| Gap | Current state | Target |
|-----|---------------|--------|
| Token / cost counter | No UI | Live in the overlay — current turn + session total |
| Plan preview | `:plan` writes markdown only | Show generated plan + "approve → switch to :do" flow |
| Diff preview | Edits apply immediately | Show unified diff before write, one-key approve/reject |
| Tool call UI | Flat list | Collapse/expand, filter by tool, search |
| Rewind / fork | None | Rewind to step N, fork a new conversation from any point |
| Runtime model switcher | Via settings only | Inline in overlay: `:model gpt-5-turbo` |
| Debug mode | None | Toggle to show raw LLM I/O, system prompt, tool schemas |

### 1.7 Evaluation

| Gap | Current state | Target |
|-----|---------------|--------|
| Golden transcripts | 3 | 20+ covering each tool + each mode, edge cases |
| terminal-bench 2.0 runs | Smoke-tested on 1 task | Full suite (200+ tasks), automated nightly, tracked over time |
| Tool-level micro-benchmarks | None | Per-tool pass/fail suite (mock LLM, real tool execution) |
| CI regression gate | None | Block merge on golden-suite regression |
| Trajectory visualization | Raw JSON | Viewer that replays a trajectory step-by-step with diffs |
| Latency / token benchmarks | None | Track p50/p95 per tool, per model, per task type |

---

## 2. Prioritized Roadmap

Three tiers. Tier 1 is "blocks calling this production." Tier 2 is "production-grade UX." Tier 3 is polish and stretch.

### Tier 1 — Critical ✅

1. ~~**LLM retry with exponential backoff**~~ ✅
2. ~~**Step budget enforcement**~~ ✅
3. ~~**Context compaction at 80% threshold**~~ ✅
4. ~~**Dangerous command detection**~~ ✅
5. ~~**Structured audit log**~~ ✅

### Tier 2 — Important ✅

6. ~~**Prompt caching (Anthropic)**~~ ✅
7. ~~**Parallel tool execution**~~ ✅
8. ~~**Live token / cost counter**~~ ✅
9. ~~**Diff preview (write + edit)**~~ ✅ — jsdiff + structuredPatch
10. ~~**Plan → Do handoff**~~ ✅
11. ~~**Runtime model switcher**~~ ✅
12. ~~**Expanded golden transcripts (21)**~~ ✅

### Bonus — Warp-Style Inline Blocks ✅

- ~~**Replace overlay with inline timeline**~~ ✅ — agent messages render as blocks in the terminal stream

### Tier 3 — Polish / Stretch (open-ended)

Goal: capabilities that separate a good agent from a great one.

13. ~~**Sub-agent delegation**~~ ✅ — `spawn_task` tool with isolated chat context, 15-step budget, same model/tools.
14. ~~**Background task execution**~~ ✅ — `shell_exec` `background: true` returns immediately with block_id.
15. ~~**Web search / fetch tool**~~ ✅ — `web_fetch` fetches URLs, strips HTML, returns text. Available in all modes.
16. ~~**Tool sandboxing**~~ ✅ — Git worktree isolation via `:worktree` command (Claude Code model). Opt-in, persistent per session.
17. ~~**Conversation rewind**~~ ✅ — `:undo` removes last turn from both frontend and backend chatstore.
18. **Full Harbor nightly run** — all tasks, scored, trended; regression alerts.
19. ~~**CI regression gate**~~ ✅ — GitHub Actions workflow for agent tests
20. **Trajectory viewer** — replay a session step-by-step, show diffs, timing, token usage.

---

## 3. Reference Coding Agents

When working on any item above, pick one or two reference implementations to benchmark against. List below; user chooses which to study per feature.

| Agent | Language | Source | Strengths | When to look |
|-------|----------|--------|-----------|--------------|
| **ForgeCode** | Rust | [antinomyhq/forge](https://github.com/antinomyhq/forge) | Multi-agent architecture, mode system, already our structural reference | Architecture, modes, tool registry |
| **Claude Code** | Proprietary | Anthropic (observable behavior only) | Most feature-complete terminal agent; strong UX polish | DevEx, plan/diff previews, overall feel |
| **OpenAI Codex CLI** | TypeScript | [openai/codex](https://github.com/openai/codex) | Official reference, approval model, sandboxing | Safety model, approval flows |
| **Aider** | Python | [Aider-AI/aider](https://github.com/Aider-AI/aider) | Mature, git-native, strong diff/edit semantics | File editing, diff UI, git integration |
| **OpenHands** | Python / TS | [All-Hands-AI/OpenHands](https://github.com/All-Hands-AI/OpenHands) | Full dev-agent framework, sandboxed runtime | Sub-agents, sandboxing, evaluation |
| **Goose** | Rust | [block/goose](https://github.com/block/goose) | MCP-first design, extensible providers | MCP, provider abstraction |
| **Cline** | TypeScript | [cline/cline](https://github.com/cline/cline) | Popular VS Code agent, approval UX, plan-then-act | Approval UI, plan/act mode split |

**Suggested pairing:**
- Reliability (Tier 1) → ForgeCode + Codex CLI
- UX (Tier 2) → Claude Code + Cline
- Sandboxing / sub-agents (Tier 3) → OpenHands + Goose
- Editing / diffs → Aider

---

## 4. Working Notes

- Don't treat this doc as a contract — re-prioritize freely as we learn from real use.
- Each Tier 1 / Tier 2 item should land as its own PR with at least one golden transcript covering the new behavior.
- When adding Tier 3 items, check back here first and reconsider priority in light of whatever real pain has surfaced.
