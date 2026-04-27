# Permissions v2 — Design Doc

**Status:** revised draft · **Owner:** native-agent worktree · **Date:** 2026-04-27 (revised)
**Tracker:** [`claude-code-parity.md`](./claude-code-parity.md) §3
**Reference designs:**
- Claude Code (`/Users/user/Documents/Claude-Code/`) — rule grammar, decision pipeline, bypass-immune safety
- pi-mono (`badlogic/pi-mono`) — minimalism: drop Mode, drop prompt templates, derive everything from rules + posture

> **What changed from earlier drafts:** the **Mode** axis (`ask` / `plan` / `do`) is gone. After deciding the prior design's Mode×Posture×Rules×Safety was 4-dimensional and confusing for a simplification effort, we collapsed Mode into rules + system-prompt customization. There is now exactly **one work axis (rules) and one strictness axis (posture)**. `bench` survives as an API-only hidden escape for eval harnesses. No prompt-template / `/plan` / `/ask` slash commands — see §10 for what replaces them.

---

## 1. Why v1 isn't enough

```go
// pkg/agent/modes.go (existing)
type ApprovalPolicy struct {
    AutoApproveAll   bool
    AutoApproveTools map[string]bool
    RequireApproval  map[string]bool
}
```

Every tool call → `mode.ResolveApproval(toolName)` → `auto-approved` or `needs-approval`. One-shot per call. Breaks down for:

- **Real coding work** — every `edit_text_file` needs a click. Users burn out and switch to `bench` (`AutoApproveAll`), losing all safety.
- **Repeated commands** — running `npm install` 8 times in a debugging session = 8 prompts.
- **Path-aware safety** — currently can't say "auto-allow writes inside cwd, prompt for everything else."
- **No memory** — chat ends, every preference learned during it is gone.

Plus the v1 system mixes 5 concepts that should be separate (mode → tool list → approval policy → state machine → UI). Adding granularity on top of that mix doesn't help; the mix itself has to go.

---

## 2. Architecture: three layers, two user-facing axes

```
┌──────────────────────────────────────────────────────────────┐
│  Tool allowlist (process startup, immutable for the run)     │
│  --tools / --no-builtin-tools / --no-tools                   │
│  Bound at agent launch, defines the universe of available    │
│  tools. The permission engine never sees calls to tools      │
│  excluded here.                                              │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│  System prompt (single source, project + global override)    │
│  pkg/agent/prompts/*.md → ~/.crest/SYSTEM.md                 │
│  → .crest/SYSTEM.md (project) → APPEND_SYSTEM.md             │
│  Tone, project conventions, custom guidance live here.       │
│  Permissions don't gate prompt text — that's the user's job. │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│  Permission Engine — every tool call passes through          │
│                                                              │
│   ┌────────────────────────────────────────────────────┐    │
│   │  Layer 1 — Bypass-immune Safety                    │    │
│   │  Hard list (`.git/`, `.ssh/`, `.env*`, `rm -rf /`, │    │
│   │  `curl|sh`, `prefix:sudo`, force-push to main…).   │    │
│   │  Always prompts; no posture can disable it.        │    │
│   └────────────────────────────────────────────────────┘    │
│                       │                                      │
│                       ▼                                      │
│   ┌────────────────────────────────────────────────────┐    │
│   │  Layer 2 — Rules (allow / deny / ask)              │    │
│   │  User-defined. Tool-name + content matcher.        │    │
│   │  Loaded from 5 scopes; deny-anywhere wins.         │    │
│   └────────────────────────────────────────────────────┘    │
│                       │                                      │
│                       ▼                                      │
│   ┌────────────────────────────────────────────────────┐    │
│   │  Layer 3 — Posture default                         │    │
│   │  default → ask · acceptEdits → auto-allow edits    │    │
│   │  · bypass → auto-allow all (still constrained by   │    │
│   │  Layer 1) · bench → auto-allow ignoring Layer 1    │    │
│   └────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────┘
```

**User-facing axes:**

1. **Rules** — `/permissions` opens the editor; approval prompts can persist new rules.
2. **Posture** — `Shift+Tab` cycles `default → acceptEdits → bypass`; status pill shows current posture.

**Hidden:** `bench` posture — API-only, activated by Harbor/eval harnesses posting `mode: "bench"`. Users never select it.

---

## 3. Rules

### 3.1 Grammar

```go
// pkg/agent/permissions/rule.go
type Rule struct {
    Behavior  RuleBehavior // "allow" | "deny" | "ask"
    ToolName  string       // "shell_exec", "edit_text_file", "*"
    Content   string       // optional; tool-specific matcher syntax (see below)
    Source    RuleSource   // who set it (precedence)
    AddedAt   int64        // unix ms; for UI only
}

type RuleBehavior string
const (
    RuleAllow RuleBehavior = "allow"
    RuleDeny  RuleBehavior = "deny"
    RuleAsk   RuleBehavior = "ask"
)
```

**Tool-name format** — exact tool name or `*`. MCP tools use the existing `mcp__server__tool` convention; `mcp__server__*` matches all tools from one server.

**Content matcher** — interpreted by each tool's `MatchContent` method:

| Tool | Matcher syntax | Examples |
|---|---|---|
| `shell_exec` | exact, or `prefix:<cmd>` | `npm install`, `prefix:git`, `prefix:cargo build` |
| `edit_text_file`, `write_text_file`, `multi_edit` | gitignore-style glob over absolute path | `/Users/me/repo/**`, `**/*.go`, `!**/secrets/**` |
| `read_text_file`, `read_dir` | gitignore-style glob | same |
| `web_fetch` | URL host/path glob | `https://api.github.com/**`, `https://*.internal/**` |
| `browser.navigate` | URL host glob | `https://github.com/*` |
| `spawn_task` | (no content matcher today) | n/a |
| any other | exact-match content | (rarely needed) |

Empty `Content` matches *any* call to that tool.

### 3.2 Wire format

Compact, human-friendly. Lives in settings.json under `ai:permissions`:

```json
{
  "ai:permissions": {
    "allow": [
      "shell_exec(prefix:npm)",
      "shell_exec(prefix:git status)",
      "edit_text_file(/Users/me/work/**)",
      "read_text_file(*)",
      "mcp__filesystem__*"
    ],
    "deny": [
      "shell_exec(rm -rf *)",
      "shell_exec(prefix:sudo)",
      "edit_text_file(**/.env)",
      "edit_text_file(**/credentials*)"
    ],
    "ask": [
      "shell_exec(prefix:npm publish)",
      "shell_exec(prefix:git push --force)"
    ],
    "defaultPosture": "acceptEdits"
  }
}
```

`ParseRule(s string) (Rule, error)` splits on the first `(`, escapes `\)` and `\\`. Rejects malformed strings at load time so typos don't silently neuter a rule.

---

## 4. Scopes

Five scopes, strict precedence (higher beats lower):

| # | Scope | Where it lives | Who writes |
|---|---|---|---|
| 5 | `cliArg` | runtime, never persisted | `--allow-tool`, `--deny-tool` flags (future) |
| 4 | `user` | `~/.config/waveterm/settings.json` `ai:permissions` | Settings UI / hand-edit |
| 3 | `sharedProject` | `<cwd>/.crest/permissions.json` | committed to repo |
| 2 | `localProject` | `<cwd>/.crest/permissions.local.json` | gitignored (per-machine personal) |
| 1 | `session` | in-memory, lifetime of the agent chat | Approval prompt |

**Resolution rule:** when multiple scopes have rules for the same tool:
1. **Deny in any scope wins.** No way to opt out of a hard deny.
2. Then highest-precedence `ask`.
3. Then highest-precedence `allow`.

Within a scope, more-specific content patterns beat broader ones (`shell_exec(prefix:git push)` ask beats `shell_exec(prefix:git)` allow).

**Why this layout:**
- `user` = personal preferences across all projects (`prefix:git status`, etc.)
- `sharedProject` = team conventions, committed (e.g. "always allow `npm test`, never allow `npm publish`")
- `localProject` = my-machine personal overrides per repo
- `session` = "remember for this chat only"
- No `policySettings` enterprise tier — Crest isn't enterprise-managed today.

---

## 5. Posture (the strictness axis)

**Three user-facing values + one hidden:**

| Posture | Behavior on calls the rules don't match | Bypass-immune safety | New-chat default? | Audience |
|---|---|---|---|---|
| `default` | Falls back to per-tool `DefaultBehavior()` (mutations → ask, reads → allow) | fires | no | cautious users / unfamiliar repo |
| `acceptEdits` | Auto-allow file-edit tools (`edit_text_file`, `write_text_file`, `multi_edit`) when target path is inside `cwd`. Other tools fall through to `default`. | **fires** (can't auto-allow `.env`/`.git/`/`.ssh/` etc.) | **yes** | iterative coding (the bundled default) |
| `bypass` | Auto-allow everything | **fires** | no | "trust me" / inside a sandbox |
| `bench` | Auto-allow everything | **off** | no — eval-only, API-activated | non-interactive eval (Harbor/TB2) |

**Why `acceptEdits` is the bundled default** — diverging from Claude Code's `default` default:

- Clicking through every `edit_text_file` during interactive coding is the #1 friction in current usage.
- Risk is bounded: file edits get a `filebackup.MakeFileBackup` snapshot before write; the mtime tracker (commit `0ce9f60b`) refuses edits to files that changed externally; bypass-immune paths still prompt; `shell_exec` still prompts; deny rules still fire.
- `default` posture is one `Shift+Tab` away when the user wants strict control.
- Claude Code's audience includes high-stakes shared environments. Crest is a personal terminal for local coding — `acceptEdits` matches that.

**Posture state lives per-chat** in the session. New chats start in the user's `defaultPosture` setting (defaults to `acceptEdits`). User flips per-chat with `Shift+Tab` (cycles `default → acceptEdits → bypass → default`) or `/permissions` to open the chooser.

**`bench` posture** is privileged — only set when the API receives `mode: "bench"` (Harbor's existing convention). Never user-selectable. Skips bypass-immune safety entirely so eval harnesses get clean signal. This is the only place `mode` still appears in the system, and only as an API alias for "set posture to bench."

---

## 6. Bypass-immune safety list

The `acceptEdits` and `bypass` postures auto-approve calls the rules don't match — but a fixed safety list overrides that and forces a prompt regardless. `bench` posture skips this list entirely.

- **`shell_exec`:**
  - `rm -rf /`, `rm -rf ~`, `rm -rf $HOME`
  - `git push --force` to `main`/`master`
  - anything containing `curl | sh` / `wget | sh`
  - fork-bomb patterns (`:(){:|:&};:` and obvious siblings)
  - `prefix:sudo`
- **File tools** (`edit_text_file`, `write_text_file`, `multi_edit`):
  - writes to `.git/`, `.crest/`, `.ssh/`, `.aws/`, `.gnupg/`
  - OS shell configs (`.bashrc`, `.zshrc`, `.profile`, `.bash_profile`)
  - `.env*`
  - files containing `credentials` or `secret` in the name
- **`web_fetch` / `browser.navigate`:** deferred — `localhost` / `127.0.0.1` on common dev ports is a footgun for legitimate local-server debugging. Add when we see an incident.

Safety checks emit an `ask` decision with reason `"safetyCheck"`. The prompt UI explains why the looser posture was overridden so the user isn't surprised by a sudden approval prompt mid-stream.

---

## 7. Decision pipeline

```
Decide(req {ToolName, Input, ChatId, Cwd, Posture}):

  1. Posture == bench?
     → Decision{Allow, reason=posture-bench, bypassImmune=false}
     (eval-only escape; skips safety entirely)

  2. Load rules from all scopes for (chat=ChatId, cwd=Cwd)
     → flat []Rule sorted by (scope precedence desc, content specificity desc)

  3. Tool-level deny rule (Content == "")?
     → Decision{Deny, reason=rule}

  4. Content-specific Deny or Ask rule match?
     - run tool.MatchContent(input, rule) for each rule with non-empty
       Content where Behavior is Deny or Ask
     - first match (highest precedence) wins
     - Allow rules are intentionally NOT matched here — they wait
       until step 6 so safety can run first
     → Decision{rule.Behavior, reason=rule}

  5. Per-tool safety check (bypass-immune)?
     - file tools: target path matches bypass-immune list → ask{safetyCheck, bypassImmune=true}
     - shell_exec: command matches bypass-immune list → same
     - default: passthrough
     → if non-passthrough, return that decision (ignores posture in step 7)

  6. Allow rule match?
     a. Tool-level allow (Content == "") → Decision{Allow, reason=rule}
     b. Content-specific allow → Decision{Allow, reason=rule}

  7. Posture-driven default for unmatched calls:
     a. posture == bypass → Decision{Allow, reason=posture-bypass}
     b. posture == acceptEdits AND tool is a file-edit AND target inside cwd
        → Decision{Allow, reason=posture-acceptEdits}
     c. posture == default → fall through

  8. Per-tool default behavior:
     - file-edit tools default to ask
     - shell_exec defaults to ask
     - read tools default to allow
     - web_fetch / browser defaults to ask
     → Decision{tool.DefaultBehavior(), reason=default}

  9. Behavior == Ask?
     - tool.SuggestRules(input) populates Decision.Suggestions
     - return for UI to prompt user

 10. Behavior == Allow/Deny? return immediately, no prompt.
```

**Step 1** is the only Mode-derived special case left, and it's renamed to `posture == bench` to match the new vocabulary.

**Step 4 splits Allow out for safety reasons.** Letting a content-specific Allow short-circuit before step 5 would let a rule like `shell_exec(prefix:echo)` auto-approve `echo \`rm -rf /\`` — the safety substring matcher never gets a chance to evaluate the inner command. Splitting Deny/Ask (which short-circuit before safety, the conservative direction) from Allow (which sits below safety) preserves the intent without ordering surprises.

**Step 5** runs *before* every Allow path (step 6 tool-level + content-specific, step 7 posture auto-allow) so safety wins over `acceptEdits` and `bypass` postures and over user-supplied Allow rules alike.

**Step 6/7 ordering:** explicit Allow rules win over posture defaults so a user-added `shell_exec(prefix:npm)` allow rule beats whatever the posture would do.

---

## 8. Architecture (code)

### 8.1 Package layout

```
pkg/agent/permissions/         (new package)
├── permissions.go             // Engine, Decide, types
├── rule.go                    // Rule, ParseRule, Match
├── matcher_glob.go            // gitignore-style globs (use github.com/sabhiram/go-gitignore)
├── matcher_prefix.go          // prefix:<...> shell-command matching
├── safety.go                  // SafetyCheck, BypassImmuneList
├── store.go                   // RuleStore: load/save/scope precedence
├── store_session.go           // In-memory session scope
├── store_settings.go          // wconfig-backed user/project scopes
└── permissions_test.go
```

Top-level package because the engine is reusable beyond the agent (future REPL, future MCP-only flows). Tools call into it via a small interface to avoid a package cycle.

### 8.2 Core types

```go
// pkg/agent/permissions/permissions.go

type Decision struct {
    Behavior     RuleBehavior  // "allow" | "deny" | "ask"
    Reason       DecisionReason
    Rule         *Rule          // populated when Behavior decided by a rule
    Suggestions  []Rule         // populated only when Behavior == Ask
    UpdatedInput map[string]any // tool-modified input (rare)
}

type DecisionReason struct {
    Kind         string  // "rule" | "tool" | "safetyCheck" | "posture" | "default"
    Detail       string
    BypassImmune bool    // true for safety checks; false otherwise
}

type CheckRequest struct {
    ToolName string
    Input    map[string]any
    ChatId   string  // for session-scope rules
    Cwd      string  // for project-scope rule loading
    Posture  string  // "default" | "acceptEdits" | "bypass" | "bench"
}

type Engine interface {
    Decide(ctx context.Context, req CheckRequest) Decision
    PersistRules(ctx context.Context, scope RuleScope, rules []Rule) error
    LoadRulesForChat(ctx context.Context, chatId, cwd string) ([]Rule, error)
}
```

Note: no `Mode` field on `CheckRequest`. The only place mode survives is the API-side aliasing of `mode: "bench"` to `posture: "bench"`.

### 8.3 Tool integration — `PermissionAdapter`

```go
// pkg/aiusechat/uctypes/uctypes.go (extension to ToolDefinition)
type ToolDefinition struct {
    // ... existing fields
    Permissions PermissionAdapter  // optional; nil for tools w/o per-call logic
}

type PermissionAdapter interface {
    MatchContent(input map[string]any, pattern string) bool
    CheckSafety(input map[string]any) SafetyResult  // bypass-immune check
    SuggestRules(input map[string]any) []SuggestedRule
    DefaultBehavior() string  // "allow" | "ask" | "deny"
    IsFileEdit() bool         // for posture acceptEdits handling
    TargetPath(input map[string]any) string  // for path-relative-to-cwd checks
}

type SafetyResult struct {
    Triggered bool
    Reason    string  // human-readable, shown in the prompt
}
```

Why duplicate the interface in `uctypes`? The pure permissions package can't import `pkg/agent/tools` without a cycle. `uctypes` defines the contract; `pkg/agent/permissions` adapts it.

Existing tool factories in `pkg/agent/tools/*.go` add a `PermissionAdapter` — most are 20-line implementations.

### 8.4 Replacing the existing approval flow

Today's flow:
```
processToolCallInternal (usechat.go)
  → toolCall.ToolUseData.Approval already set by registry.go (mode.ResolveApproval)
  → if NeedsApproval: WaitForToolApproval
```

New flow (using the BeforeToolHook pipeline shipped in commit "D"):
```
processToolCallInternal
  → engine.Decide(req) → Decision
  → switch Decision.Behavior:
     - Allow: set Approval=AutoApproved, run
     - Deny:  set status=Error("denied: " + reason), skip run
     - Ask:   set Approval=NeedsApproval, attach Decision.Suggestions
              for the FE; WaitForToolApproval; on user-approve, persist
              any selected suggestions via engine.PersistRules
```

**`engine.Decide` is registered as a global `BeforeToolHook`** on `WaveChatOpts.BeforeToolHooks` at agent setup. The hook returns `nil` for Allow (proceed), an error `*AIToolResult` for Deny, or signals "need approval" via the existing `RegisterToolApproval` mechanism.

`mode.ResolveApproval` in `pkg/agent/registry.go` is **gone**. The whole `pkg/agent/modes.go` file goes away — replaced by:
- `pkg/agent/permissions` (new)
- A small system-prompt seeder in `pkg/agent/prompts.go` that picks the right base prompt (no longer keyed by mode name; just one default + user overrides via SYSTEM.md)

### 8.5 Settings schema

```go
// pkg/wconfig/types.go (extension)
type SettingsType struct {
    // ... existing
    AIPermissions *AIPermissionsConfig `json:"ai:permissions,omitempty"`
}

type AIPermissionsConfig struct {
    Allow          []string `json:"allow,omitempty"`          // "shell_exec(prefix:npm)"
    Deny           []string `json:"deny,omitempty"`
    Ask            []string `json:"ask,omitempty"`
    DefaultPosture string   `json:"defaultPosture,omitempty"` // "default"|"acceptEdits"|"bypass". Defaults to "acceptEdits".
}
```

Project-shared rules at `<cwd>/.crest/permissions.json`; per-user-per-project at `<cwd>/.crest/permissions.local.json`. Same JSON shape.

### 8.6 Frontend

- **Status pill** in agent overlay header: `permissions: acceptEdits` (clickable to open `/permissions`). Color-coded: neutral `default`, info `acceptEdits`, warning `bypass`.
- **`Shift+Tab`** keybinding cycles posture (`default → acceptEdits → bypass → default`) when overlay has focus.
- **`/permissions`** opens chooser with the three postures + brief explanation of each, plus a link to the rules editor and "set as default for new chats" checkbox.
- **Approval prompt** extends `term-agent.tsx` `TermAgentApprovalButtons` with suggestion list and destination picker:

```
┌────────────────────────────────────────────────────────────┐
│ shell_exec wants to run:                                    │
│   npm install --save-dev typescript                         │
│                                                             │
│ [ Approve ]  [ Deny ]                                       │
│                                                             │
│ Or remember:                                                │
│   ◯ This command exactly                                    │
│   ◉ All `npm` commands               (prefix:npm)           │
│   ◯ All shell_exec calls             (entire tool)          │
│                                                             │
│ Save to:  ◉ Session  ◯ This project  ◯ Always               │
│                                                             │
│ [ Approve and Remember ]                                    │
└────────────────────────────────────────────────────────────┘
```

Suggestions come from `tool.SuggestRules(input)`. `wshrpc agent:tool-approve` payload extends with `acceptedSuggestions: Rule[]` and `destination: RuleScope`.

---

## 9. Default rules shipped (in-binary)

```
allow:
  - read_text_file(*)
  - read_dir(*)
  - search(*)
  - get_scrollback(*)
  - cmd_history(*)
  - todo_read(*)
  - todo_write(*)
  - shell_exec(prefix:git status)
  - shell_exec(prefix:git diff)
  - shell_exec(prefix:git log)
  - shell_exec(prefix:ls)
  - shell_exec(prefix:pwd)
  # NOT included: prefix:cat, prefix:echo. cat would auto-approve
  # `cat ~/.ssh/id_rsa` / `cat .env`; echo + shell substitution
  # (`echo $(rm -rf /)`) sneaks past the safety substring matcher.
  # `read_text_file` covers safe file reads with proper hooks.

deny:
  - shell_exec(prefix:sudo)
  - shell_exec(prefix:rm -rf /)
  - shell_exec(prefix:rm -rf ~)
  - shell_exec(prefix:rm -rf $HOME)
  - shell_exec(prefix:curl | sh)
  - shell_exec(prefix:wget | sh)
  - edit_text_file(**/.env)
  - edit_text_file(**/.env.*)
  - edit_text_file(**/credentials*)
  - edit_text_file(**/.ssh/**)

ask: (nothing — handled by per-tool DefaultBehavior)
```

These ship as in-binary defaults at the lowest precedence, below `session`. The user can override any one. They only become "real" persisted rules when the user changes them via the UI.

---

## 10. What replaces Mode

The previous Mode axis bundled three things (tool subset, system prompt, default approval policy). After v2 they map roughly as follows:

| Old: Mode value | New: how to get equivalent behavior |
|---|---|
| `ask` (read-only Q&A) | Launch with `--tools read,grep,find,ls`. **Or** add session deny rules: `deny edit_text_file(*)` etc. **Or** set posture `default` and let the agent get refused per-call. |
| `plan` (propose-first) | **Open question — deferred.** v2 does not ship a replacement for plan mode. The bundled system prompt does not change. Users who relied on plan mode will currently have no first-class equivalent. Whether to add one (slash command, system-prompt opt-in, dedicated posture, or something else) is a separate design conversation. |
| `do` (full agent) | Default. Posture `acceptEdits` (the bundled default). |
| `bench` (eval harness) | Stays. API-only. POST `mode: "bench"` to the agent endpoint → backend forces posture to `bench`. Hidden from user-facing UI. |

**No `/plan` slash command, no prompt template system.** Pi's argument: every prompt-template system fails the 90/10 test (90% of users never use it; 10% have 5 own flows it doesn't cover). The combination of `@filename` references + `SYSTEM.md` covers the 10% case without the slash machinery.

This explicitly leaves a gap where `plan` mode used to live. v2 permissions does not try to fill it. The shipped system prompt stays as-is; we are not adding "propose a plan first" guidance to SYSTEM.md as part of this work. If plan-mode loss turns out to be a real problem, the fix is a follow-up design — possibly a posture, possibly something else — informed by actual usage data, not preemptively baked into the default prompt.

**Slash commands that survive** are control-flow only:
- `/permissions` — view rules, change posture
- `/model` — switch model
- `/clear` — clear chat
- `/compact` — manual compaction trigger
- `/login` / `/logout` — OAuth
- `/share` — export session

These don't touch the next prompt's content. They invoke a function. Different category from `/plan` / `/test` / `/refactor` (which we're cutting).

---

## 11. Migration plan

### 11.1 Existing chats / settings

The `Mode` concept disappears from the public API surface for new clients, but old clients posting `mode: "ask"` etc. need to keep working until FE is updated. Backend translates:

| API receives | Engine treats as |
|---|---|
| `mode: "ask"` | (no mode); session-scope deny rules `edit_text_file(*)`, `write_text_file(*)`, `multi_edit(*)`, `shell_exec(*)`. Posture: `default`. |
| `mode: "plan"` | (no mode); same deny rules as `ask`; posture: `default`. The system prompt is not modified — the "propose-first" semantic that plan mode used to add is not preserved. See §10 (open question). |
| `mode: "do"` | (no mode); posture: `defaultPosture` setting. |
| `mode: "bench"` | posture: `bench`; bypass-immune safety off. |

This keeps Harbor unchanged and gives a 1-release window for the FE to drop the mode picker.

### 11.2 Files removed

- `pkg/agent/modes.go` — gone. ApprovalPolicy struct, mode definitions, `ResolveApproval` method all deleted.
- `pkg/agent/registry.go` — `mode.ResolveApproval` calls deleted; tool registration no longer takes an approval-resolver closure (the engine decides at run time, not registration time).

### 11.3 Files added

- `pkg/agent/permissions/` package (per §8.1)
- `pkg/wconfig/types.go` — `AIPermissionsConfig` extension
- `frontend/.../PermissionsPanel.tsx` — `/permissions` UI

### 11.4 Implementation order

1. **Types + parser** — `pkg/agent/permissions/{rule,permissions}.go`. Rule, RuleBehavior, Posture, CheckRequest, Decision. ParseRule/Stringify. Unit tests for the grammar.
2. **Matchers** — glob (path), prefix (shell), exact. Unit tests.
3. **RuleStore** — load/save settings.json; precedence walk; project file loading. Unit tests.
4. **Safety list** — `safety.go` with the §6 patterns hard-coded. Unit tests covering each.
5. **Engine.Decide** — full pipeline (§7). Unit tests covering each posture × rule-set combination.
6. **Tool adapters** — implement `PermissionAdapter` for `shell_exec`, `edit_text_file`, `multi_edit`, `write_text_file`, `read_text_file`, `web_fetch`, `browser.*`, `spawn_task`. Each with `SuggestRules`.
7. **Wire into usechat.go** — register `engine.Decide` as a global `BeforeToolHook` on `WaveChatOpts.BeforeToolHooks` (the hook pipeline from D). Add `Posture` field to `Session` (per-chat state). API endpoint accepts `mode: "bench"` → forces posture to bench. Delete `pkg/agent/modes.go` and `mode.ResolveApproval` callers.
8. **Frontend prompt** — extend `TermAgentApprovalButtons` with suggestions list and destination picker. New wshrpc payload.
9. **Settings UI** — read-only rule list under a new "Permissions" tab. (Editor in a follow-up.)
10. **Default rules** — bundle the §9 list into the engine's `inBinaryRules` source, lowest precedence.
11. **Documentation** — update `claude-code-parity.md` §3 status, write a short user-facing guide on how rules work, document `--tools` + SYSTEM.md as the substitute for `ask` / `plan` modes.

Estimated: ~3 sittings to step 7 (functional parity + mode removal); 1 more sitting for steps 8-11.

---

## 12. Decisions log

| Decision | Why |
|---|---|
| **Drop Mode entirely** | Old design bundled three independent things (tool subset, prompt, approval policy). Splitting them — rules, SYSTEM.md, posture — exposes one knob each. Resolved 2026-04-27. |
| **Keep `bench` as API-only escape** | Eval harnesses (Harbor, TB2) need bypass-immune safety off and don't have a user to prompt. `mode: "bench"` over the API maps to posture `bench`. Never user-selectable. |
| **No `/plan` / `/ask` / prompt-template slashes** | Per pi-mono. Replaced by `@filename` reference (per-call) and SYSTEM.md (persistent). Saves the surface area of a template registry, conflict resolution, etc. |
| **Keep utility slashes** (`/permissions`, `/model`, `/clear`, `/compact`) | These invoke functions, not template expansion. Different category. |
| **Bundled default posture is `acceptEdits`** | Diverges from Claude Code (`default` default). Matches Crest's audience (personal local coding). Risk bounded by file backups, mtime tracking, bypass-immune paths, deny rules, `shell_exec` still prompting. |
| **`Shift+Tab` cycles posture** | Matches Claude. Cycle: `default → acceptEdits → bypass → default`. |
| **Three user-facing posture values** | `default`, `acceptEdits`, `bypass`. (`bench` hidden.) Pi has these same three; we adopt for consistency with the wider ecosystem. |
| **Permissions package at `pkg/agent/permissions`** | Top-level keeps reuse open; adapter interface in uctypes avoids cycle. |
| **`PermissionAdapter` lives on `ToolDefinition`** | Per-tool content matching + suggestions belong with the tool definition; the engine treats them via interface. |
| **In-binary default rules shipped** | Covers the 80% case (`git status` etc. always allowed) without forcing every user to configure. Lowest precedence so they're easy to override. |
| **`localProject` is gitignored, `sharedProject` is committed** | Standard convention from `.env.local` etc. |
| **One unified rule pool (no per-mode/per-posture rule namespaces)** | Simpler mental model. Mode-specific rules confused users in the prior draft. |

---

## 13. Out of scope

- **Classifier-based auto-approve** (Claude's `auto`/YOLO + LLM transcript classifier). Defer indefinitely; rules + posture cover the same UX for far less complexity.
- **PermissionRequest webhook hooks**. Crest's `BeforeToolHook` pipeline already supports custom blocking — webhooks aren't needed.
- **Policy tier** (admin/enterprise managed settings). Skip until a user asks.
- **CLI rule-management commands** (`crest permissions add`). Settings JSON edit is enough for v1.
- **`additionalDirectories`** (Claude's per-extra-dir rules). Useful but adds surface area; defer.
- **Replacement for plan mode.** Removing Mode leaves a gap where `plan` lived; this design intentionally does not fill it. No system-prompt changes ship with v2. If plan-mode-style behavior turns out to matter, that's a separate design pass — likely informed by what users actually miss.

---

## 14. References

Claude Code source paths (reference only — do not import or copy verbatim):

- `src/types/permissions.ts` — types, posture constants, rule shape
- `src/utils/permissions/permissions.ts` — `hasPermissionsToUseToolInner` (decision pipeline)
- `src/utils/permissions/PermissionMode.ts` — posture lifecycle (their "mode" is our "posture")
- `src/utils/permissions/getNextPermissionMode.ts` — Shift+Tab cycle
- `src/utils/permissions/permissionRuleParser.ts` — rule string parser
- `src/utils/permissions/applyPermissionUpdates.ts` — rule mutation
- `src/utils/permissions/persistPermissionUpdates.ts` — save to disk
- `src/components/permissions/PermissionPrompt.tsx` — approval UI
- `src/tools/BashTool/bashPermissions.ts` — Bash content matching
- `src/tools/FileEditTool/filesystem.ts` — file path matching

pi-mono reference (for the simplification stance):

- `packages/coding-agent/README.md` "Philosophy" section — no mode, no plan mode, no permission popups (we keep popups but adopt the rules + posture minimalism)
