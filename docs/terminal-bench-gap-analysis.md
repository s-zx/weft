# Terminal-Bench 2 Gap Analysis — Crest vs ForgeCode

**Status:** Draft for review. Implementation has not started. **2026-04-26**.

## TL;DR

Crest cannot run on Terminal-Bench 2 today — there's no harness adapter, no Docker-container execution model, and `shell_exec` is single-shot rather than session-resident. Even after fixing those plumbing gaps, the agent's loop is missing several scaffold features that empirically separate ~60% scoring agents from ~75% scoring agents on TB2: doom-loop detection, tool-error reflection, todo-state enforcement, AI-summarized compaction, ripgrep/glob tools, and a verification discipline prompt.

**Realistic target:** ~70–72% on TB2 with `Claude Opus 4.7` + a clean Crest-Harbor scaffold. ForgeCode reports 81.8% but a third-party audit ([debugml.github.io/cheating-agents](https://debugml.github.io/cheating-agents/)) found their submitted traces relied on `AGENTS.md` answer keys; clean rerun was ~71.7%. Treat ~72% as the credible ceiling, not 82%.

**Reference benchmarks** (cleaner comparisons):
- Codex CLI + GPT-5.2 = 62.9% (paper baseline)
- Claude Opus 4.5 + Terminus-2 = 57.8% (paper baseline, the canonical "simple loop")
- Top scaffolds with the same model = +7–17pp over Terminus-2

The score-relevant work is *all in the agent loop and tool surface*, not in UI/UX.

---

## What Terminal-Bench 2 actually tests

89 tasks, each shipping as `(Dockerfile + instruction.md + tests/test.sh + oracle solution)`. Per-task:
- Wall-clock budget ~30 min (`task.toml` `[agent].timeout_sec=1800`)
- Resource cap `cpus=1 memory=2G storage=10G`
- Binary scoring — `tests/test.sh` exits zero or it doesn't.

Failure-mode breakdown (Snorkel + paper):
1. **Execution errors (~34%)** — command-not-found / missing executables / runtime errors
2. **Coherence errors (~25%)** — agent forgets prior file edits, repeats subgoals, drifts off plan
3. **Verification errors (~25%)** — agent declares "done" without running the tests

What top scaffolds (Terminus-KIRA, Forge, Droid) all add over a vanilla loop:
- **Persistent tmux session** for long-running processes (servers, builds)
- **Pre-execution plan** the model writes and commits to
- **Pending-todos guard** preventing premature "done"
- **Tool-error reflection** before retry
- **Doom-loop detection** when same call repeats N times

Things that don't move the score: diff UIs, syntax highlighting, browser tools, file previews. The benchmark is shell-throughput-limited.

---

## Crest baseline (current state)

Tooling and loop, with file references:
- 17 core tools, 3 modes (`ask`/`plan`/`do`) — `pkg/agent/registry.go`, `pkg/agent/modes.go:41-125`
- 50-step hard budget — `pkg/agent/agent.go:28` (`DefaultMaxAgentSteps=50`)
- 100k token compaction trigger at 80% — `pkg/aiusechat/usechat.go:576-581`
- Compaction = drop middle, keep first 1 + last 10 messages (no AI summary) — `pkg/aiusechat/chatstore/chatstore.go`
- Parallel tool calls (read-only only) — `pkg/aiusechat/usechat.go:586`
- Anthropic prompt caching (system prompt + rolling) — `pkg/aiusechat/anthropic/anthropic-convertmessage.go`
- Git worktree sandbox — `pkg/agent/sandbox.go`
- Dangerous-cmd guard (15 regex patterns) — `pkg/agent/tools/dangerous.go:25-59`
- SSRF-protected web_fetch — `pkg/agent/tools/web_fetch.go`
- Sub-agent (`spawn_task`, 15-step / 120s budget) — `pkg/agent/tools/spawn_task.go`
- File checkpoint + rewind — `pkg/agent/checkpoint.go`
- Four backends (Anthropic / OpenAI Responses / OpenAI Completions / Gemini) — `pkg/aiusechat/usechat-backend.go`
- System prompt = `prompts/shared_header.md` + `prompts/{ask,plan,do}.md` — `pkg/agent/prompts.go:30-49`

Tools available in `do` mode: `read_text_file`, `read_dir`, `get_scrollback`, `cmd_history`, `web_fetch`, `write_text_file`, `edit_text_file`, `shell_exec`, `write_plan`, `create_block`, `focus_block`, 4 browser tools, `spawn_task`, plus dynamic MCP tools.

---

## Gap matrix — Crest vs ForgeCode

Organized by impact on TB2 score. Each row: what's missing, why it matters, where to add it.

### Critical — blocks scoring entirely

| # | Gap | Crest current | ForgeCode | Where this lives |
|---|---|---|---|---|
| C1 | **No Harbor harness adapter** | None — Crest is a desktop UI agent only | Custom agents register a Python `BaseAgent` class (`harbor/src/harbor/agents/base.py`); Forge ships its own benchmark harness in `benchmarks/` | New: `benchmarks/harbor-adapter/` driving Crest's Go core via stdin/stdout JSON |
| C2 | **shell_exec is single-shot** — each call spawns a fresh subprocess | `pkg/agent/tools/shell_exec.go:57-130`. No way to keep a server running across calls; no shared shell state | Forge's `shell` tool also single-shot, but the **benchmark scaffolds (Terminus-2, Terminus-KIRA) use tmux** — `harbor/src/harbor/agents/terminus_2/tmux_session.py` is the reference | New tool: `pty_session` (open / send / read / close) backed by a long-lived `bash` or tmux pane. Termblocks already manage PTYs; reuse the plumbing |
| C3 | **Host-only execution** — tools touch the host filesystem | `shell_exec`, `read_text_file`, `write_text_file` all hit local fs | TB2 runs each task in a Docker container; agent must operate **inside** the container | Adapter (C1) must shell into the container; tool calls become `docker exec` style |
| C4 | **No "headless / non-interactive" mode** — tool approvals require UI | `pkg/agent/toolapproval.go` blocks until SSE approval message; sub-agent dangerous commands time out at 120s with no UI | Forge has `restricted` mode + `permissions.default.yaml` policy; runs benchmarks unattended | New: bench mode that auto-approves all non-dangerous tools and *runs* dangerous ones in a hardened sandbox (since the Docker container is itself the sandbox). `pkg/agent/modes.go` needs a `bench` mode |

### Important — moves score significantly

| # | Gap | Crest current | ForgeCode | Where this lives |
|---|---|---|---|---|
| I1 | **No ripgrep/grep tool** — model uses `shell_exec` for search, which is approval-gated and noisy | No `pkg/agent/tools/search.go` | `fs_search` with regex + glob + multiline + 3 output modes; description forbids `rg`/`grep` via shell — `crates/forge_domain/src/tools/descriptions/fs_search.md` | New: `pkg/agent/tools/search.go` wrapping `rg --json`. Auto-approved (read-only). Mark `Parallel: true` |
| I2 | **No glob tool** | Folded into shell_exec | Folded into `fs_search` via `glob` parameter | Add `glob` parameter to the new `search` tool |
| I3 | **No multi-edit** — model has to call `edit_text_file` N times for N hunks | `pkg/agent/tools/edit_text_file.go` does one range replace | `multi_patch` is atomic batch (all-or-nothing) | New: `pkg/agent/tools/multi_edit.go` taking `[]EditOp{old_string, new_string, replace_all?}`, applies sequentially in memory, single backup, single approval |
| I4 | **No doom-loop detection** | None — model can repeat the same failed call indefinitely until the 50-step cap | `templates/forge-doom-loop-reminder.md` injected when `consecutive_calls` exceeds threshold | Add to step loop in `pkg/aiusechat/usechat.go:506-596`: hash recent (tool_name, args) tuples; if N identical in window, append a system message warning |
| I5 | **No tool-error reflection** | Tool errors surface as plain text; model often blindly retries | `templates/forge-partial-tool-error-reflection.md` forces *"deeply reflect: pinpoint exactly what was wrong… explain why that mistake happened"* | After a failed tool call, prepend a reflection nudge to the next tool result. Implement in `pkg/aiusechat/usechat.go` tool-result conversion |
| I6 | **No `todo_write` / `todo_read` tools with pending-todos guard** | Model self-tracks in text only | Stateful diff-update todos; **pending-todos guard prevents declaring done** with open items — `templates/forge-pending-todos-reminder.md` | New: `pkg/agent/tools/todo.go`. State stored in chat session. Guard: when model emits a "stop" signal with open items, inject a reminder and continue the loop |
| I7 | **Compaction loses state** — drops middle, no summary | `keepFirst=1, keepLast=10` (`pkg/aiusechat/chatstore/chatstore.go`) | Compactor renders structured summary stubs per tool kind (`**Update:** path`, `**Read:** path`, `**Search:** pattern`); deduces "last operation per file path"; preserves Anthropic reasoning_details across compaction — `crates/forge_app/src/compact.rs` | Rewrite compaction in `pkg/aiusechat/chatstore/chatstore.go`. Build a summary message from compacted range using a template like Forge's `forge-partial-summary-frame.md`. For Anthropic: copy last `reasoning_details` into first surviving assistant message |
| I8 | **No verification step in prompt** | `do.md` says *"verify them: run tests"* but not enforced | Forge: `forge.md` has *"Implementation Methodology: 4. Quality Assurance — Validate changes through compilation and testing"* + pending-todos guard forces it | Tighten `prompts/do.md` to require a verify step. Combine with I6 — auto-add a "verify with tests" todo after first mutation |
| I9 | **No AGENTS.md auto-prepend** | Not loaded; agent reads CLAUDE.md/CONVENTIONS.md only if explicitly read | Forge auto-injects `<project_guidelines>` from `AGENTS.md` at repo root — `templates/forge-custom-agent-template.md` | Add to `pkg/agent/context.go` `BuildTerminalContext`: scan cwd for AGENTS.md (and CLAUDE.md as fallback) and inject as `<project_guidelines>` |
| I10 | **Compaction trigger only on input tokens, not output** | Triggers when `lastInputTokens > ContextBudget * 0.8` (`usechat.go:576-581`) | Forge tracks both, plus per-tool output spillover to temp files | Add output-token tracking. Long tool outputs (>20KB) should be spilled to a temp file in chat scratchspace and the model gets a stub + path |
| I11 | **Sub-agent step budget too low for complex tasks** | `SpawnTaskMaxSteps=15`, `SpawnTaskTimeout=120s` — too short for anything non-trivial | Forge `task` tool runs sub-tasks in **parallel** via `join_all`, no fixed step cap (config-driven) | Bump to ~30 steps + 600s default in `pkg/agent/tools/spawn_task.go`. Make sub-agents return their final assistant message text, not just metrics summary |
| I12 | **Sub-agent only returns summary** | Returns step/tool counts; caller has to parse | Forge's `task` returns the actual response from the sub-agent | Extract last assistant message from sub-chatstore before deletion in `pkg/agent/tools/spawn_task.go:113` |
| I13 | **Output truncation drops content** — model can't recover dropped content | `shell_exec` truncates at 8192 bytes (`shellExecTailBytes`); `web_fetch` truncates at 100KB; rest is gone | Forge spills overflow to temp files; model can re-read or `fs_search` them | When truncating, write full output to `<chatdir>/scratch/<tool>-<id>.out` and return path in response |
| I14 | **No "research" sub-mode** — `ask` is the only read-only mode but lacks the prompt discipline of Forge's Sage | Single ask prompt | Forge has `sage.md` — research-only with mandatory `Research Summary / Key Findings / Technical Details / Insights / Follow-ups` schema | Optional: add a fourth mode `research` for read-deep + cite. Lower priority — partial overlap with `ask` |

### Nice-to-have — smaller wins, do later

| # | Gap | Notes |
|---|---|---|
| N1 | Tool aliases like `Read`/`Write` | Forge ships `#[serde(alias = "Read")]` to match training priors; possibly +0.5pp |
| N2 | Reasoning effort levels per session | `:reasoning-effort none/minimal/low/medium/high/xhigh/max` in Forge; Crest has `ThinkingLevel` but no slash command |
| N3 | Skill lazy-loading | Forge keeps skill bodies out of the system prompt; only descriptions visible until `skill_fetch`. Crest has `pkg/agent/skills.go` — check if it's already lazy |
| N4 | Snapshot tests of rendered prompts | `crates/forge_app/src/orch_spec/snapshots/`. Add Go snapshot tests for assembled system prompt — guards against accidental prompt regressions |
| N5 | `followup` tool | Forge surfaces clarifying questions as a structured tool. Crest just lets the model write text. Useful for non-bench UX |
| N6 | Permission policy YAML | Forge `permissions.default.yaml`. Lets advanced users customize without recompiling |

---

## Recommended implementation phases

Each phase = one PR / commit batch. Stop after Phase 1 to validate baseline TB2 score; that number tells us how much the loop work matters vs the harness plumbing.

### Phase 0 — Reproducibility setup (1-2 days)

Goal: be able to run *some* TB2 score, even a bad one.
- Read Terminus-2 source (`harbor/src/harbor/agents/terminus_2/`) end-to-end. It's the canonical simple loop and the reference scaffold from the paper.
- Set up a private Harbor worktree with `uv` + Docker.
- Run `harbor run -d terminal-bench/terminal-bench-2 -a oracle` to confirm the harness works.
- Run `harbor run -d ... -a terminus_2 -m anthropic/claude-opus-4-7 -n 1 --task-name <small_task>` to confirm the model API works.

**Deliverable:** a one-page run-recipe doc and a baseline score for `terminus_2 + opus-4-7` on a 5-task subset.

### Phase 1 — Harness adapter + headless mode (Critical: C1, C4) (3-5 days)

`pkg/agent/` change. Requires app restart but does NOT touch the desktop UI.

- New `pkg/agent/harbor/` package containing:
  - A stdin/stdout JSON-line driver (Crest spawns Go binary; Harbor adapter pipes prompt in, reads tool calls + final answer out)
  - Headless tool-approval policy: auto-approve everything except destructive shell commands (and even then, since we're inside a TB Docker container, allow them)
  - Mode `ModeBench` in `pkg/agent/modes.go` with `AllowMutation=true`, full tool palette, no approval gates
- Python adapter under `benchmarks/harbor-adapter/crest_agent.py`:
  - Subclass `BaseInstalledAgent`
  - In `setup()`: `docker exec` to install the Crest binary in the task container
  - In `run()`: stream the instruction to Crest, capture the trace
  - Implement `populate_context_post_run()` from chatstore output
- Drive the **existing** shell_exec / read / write tools (no new tools yet).

**Verification:** `harbor run -d terminal-bench/terminal-bench-2 -a crest -m anthropic/claude-opus-4-7 -n 5` produces a score. Baseline expected ~50-60% (matching simple-scaffold + Opus published numbers).

### Phase 2 — Persistent shell session (Critical: C2) (2-3 days)

Single most consequential gap after the adapter. Long-running services (servers, daemons, watch-mode tests) currently break Crest's per-call shell.

- New tool: `pty_session` (`pkg/agent/tools/pty_session.go`)
  - `open(cmd?, cwd?)` → returns `session_id`
  - `send(session_id, data, expect_output_for_ms?)` → returns new output + `is_running`
  - `read(session_id, since_seq?)` → returns output and `is_running`
  - `close(session_id)`
- Backed by a Go `*os.exec.Cmd` with a pty (use `creack/pty`)
- Outputs accumulate per session; per-call returns delta only
- Cap N=4 sessions per chat
- For benchmark mode, allow the model to use `pty_session` instead of `shell_exec` for anything that needs persistence

**Verification:** add an eval task that runs `python -m http.server` in a session, then does `curl localhost:8000` in another, and confirms the model can do both.

### Phase 3 — Search tools and multi-edit (Important: I1, I2, I3, I13) (2 days)

Pure tool additions — no loop changes. All in `pkg/agent/tools/`:

- `search` — wraps `rg --json`. Params: `pattern`, `glob?`, `multiline?`, `output_mode`. Read-only, auto-approve, parallel-safe.
- `glob` — simple glob match (or fold into `search` like Forge).
- `multi_edit` — atomic sequence of `(old_string, new_string, replace_all?)` operations on one file. Single backup, single approval.
- Output spillover for `shell_exec` and `web_fetch` — when truncating, write full output to `<chatdir>/scratch/<tool>-<id>.out` and return path.

**Verification:** unit tests + one TB2 task that exercises grep + multi-file refactor.

### Phase 4 — Loop discipline (Important: I4, I5, I6, I7, I8, I9) (4-6 days)

Highest-leverage loop work. All in `pkg/aiusechat/usechat.go` and `pkg/agent/prompts/`.

- **Doom-loop detection.** In the step loop, hash `(tool_name, args_json)` for last 6 calls. If 3+ identical, inject a system-message reminder.
- **Tool-error reflection.** When a tool returns an error, prepend the reflection template to the next tool result so the model sees it.
- **`todo_write` / `todo_read` tools** with pending-todos guard. Block "stop" responses if open items exist; inject reminder.
- **Compaction rewrite.** Replace `keepFirst=1, keepLast=10` with rendered summary. Implement template-based summary (one line per tool call: `**Update:** path`, `**Read:** path`, `**Search:** pattern`, `**Execute:** cmd`). Dedupe per-file-path to last operation. For Anthropic, copy last `reasoning_details` into the surviving assistant message.
- **Verification prompt.** Tighten `prompts/do.md` to require running the task's verifier before declaring done. Wire to todos: auto-add a "run verifier" todo after first mutation.
- **AGENTS.md auto-prepend.** In `pkg/agent/context.go` `BuildTerminalContext`: scan cwd for `AGENTS.md`, inject as `<project_guidelines>`.

**Verification:** rerun the TB2 5-task subset; expect material improvement (10–15pp gain plausible).

### Phase 5 — Sub-agent improvements (Important: I11, I12) (1 day)

- Bump `SpawnTaskMaxSteps` to 30 and `SpawnTaskTimeout` to 600s.
- Return final assistant text from sub-agent, not just metrics.
- Update `spawn_task` description to recommend parallel use for independent sub-tasks.

### Phase 6 — Full TB2 run + leaderboard (1 day)

- Run the full 89-task suite with `-n 3` (3 attempts each, average pass@1).
- File a PR to `laude-institute/terminal-bench-leaderboard`.
- Iterate on individual task failures using Harbor's trace viewer.

---

## Open questions / decisions to make

1. **Headless mode trust model.** In benchmark mode, do we run *all* shell commands (including `rm -rf`) without approval, or do we keep dangerous-cmd guard active? I'd argue full pass-through inside Docker — the container is the sandbox.

2. **Where the harness adapter lives.** Same repo, or sibling repo `crest-bench/`? Sibling avoids polluting Crest with Python + Docker dependencies.

3. **Does `pty_session` replace `shell_exec`, or coexist?** Both probably needed — `shell_exec` for one-shot commands the user actually wants to see, `pty_session` for the agent's internal long-running processes. In the desktop UI, `shell_exec` is the only one with visible-block semantics.

4. **Multi-agent split (Forge's muse/sage/forge)?** Likely overkill for Crest's UI use case; the existing `ask`/`plan`/`do` modes already cover this. Hold off.

5. **Semantic search.** Forge has hosted `sem_search`. We don't, and shouldn't add a hosted dep. Could use a local embeddings index, but that's a multi-week project — out of scope for TB2 work.

6. **Model choice for the leaderboard run.** `Claude Opus 4.7` is the obvious default. Worth comparing to `Sonnet 4.6` (faster, cheaper) and `GPT-5.4`.

---

## Risks

- **Anthropic rate limits.** A full 89-task × 3-attempt run with 30-min budgets per task can burn millions of tokens. Budget needed before kicking off the leaderboard run.
- **Harness instability.** Harbor is recently rewritten. Expect setup pain. Get the oracle agent working first as a smoke test.
- **Benchmarks lie.** Forge's 81.8% was inflated; Crest's clean number is likely the more interesting datapoint to publish. Don't ship a `crest-bench` `AGENTS.md` with answer keys — that's the cheating trap.
- **Loop changes can regress UI behavior.** The compaction rewrite (Phase 4) and todos guard touch hot paths used by the desktop UI. Keep snapshot tests + golden transcripts.

---

## Sources

- ForgeCode source survey: github.com/antinomyhq/forgecode (`crates/forge_domain/src/tools/`, `crates/forge_app/src/orch.rs`, `crates/forge_app/src/compact.rs`, `crates/forge_repo/src/agents/{forge,muse,sage}.md`, `templates/`)
- Terminal-Bench 2 mechanics: github.com/harbor-framework/terminal-bench-2, github.com/harbor-framework/harbor, harborframework.com/docs, arxiv.org/abs/2601.11868
- Failure-mode breakdown: snorkel.ai/blog/terminal-bench-2-0-raising-the-bar-for-ai-agent-evaluation/, morphllm.com/terminal-bench-2
- Cheating audit: debugml.github.io/cheating-agents/
- Crest current state: this repo, branch `feat/native-agent`. Architecture at `docs/agent-architecture.md`. Tools at `pkg/agent/tools/`. Loop at `pkg/aiusechat/usechat.go:506-596`. Modes at `pkg/agent/modes.go`. Prompts at `pkg/agent/prompts/`.
