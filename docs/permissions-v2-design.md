# Permissions v2 — Design Doc

**Status:** draft for review · **Owner:** native-agent worktree · **Date:** 2026-04-27
**Tracker:** [`claude-code-parity.md`](./claude-code-parity.md) §3
**Reference design:** Claude Code (`/Users/user/Documents/Claude-Code/`), particularly:
- `src/types/permissions.ts` — types, modes, rule shape
- `src/utils/permissions/permissions.ts` — decision pipeline (`hasPermissionsToUseToolInner`)
- `src/utils/permissions/PermissionMode.ts`, `getNextPermissionMode.ts`
- `src/components/permissions/PermissionPrompt.tsx` — approval UI
- `src/hooks/toolPermission/PermissionContext.ts` — hook integration

---

## 1. Context

Today's permission system in Crest is mode-only:

```go
// pkg/agent/modes.go
type ApprovalPolicy struct {
    AutoApproveAll   bool
    AutoApproveTools map[string]bool
    RequireApproval  map[string]bool
}
```

Each tool call → `mode.ResolveApproval(toolName)` → `auto-approved` or
`needs-approval`. The frontend shows Approve/Deny buttons
(`term-agent.tsx:163`). One-shot per call: no remembering, no path
patterns, no shell-command patterns, no persistence.

This is fine for short interactive sessions but breaks down for:
- **Real coding work** — every `edit_text_file` needs a click. Users
  burn out and switch to `bench` (`AutoApproveAll`), losing all safety.
- **Repeated commands** — running `npm install` 8 times in a debugging
  session = 8 approval prompts.
- **Path-aware safety** — currently can't say "auto-allow writes
  inside cwd, prompt for everything else."
- **No record** — chat ends, every preference learned during it is
  gone.

**Goal:** match Claude Code's permission expressiveness with rules ×
scopes × modes, while staying within Crest's terminal/Go/React stack
and keeping the existing mode-based UX as the default.

---

## 2. Goals and Non-Goals

### v1 in-scope

- **Rules** with tool-level + content-specific matchers (path globs for
  file tools, prefix patterns for `shell_exec`).
- **Three behaviors:** `allow`, `deny`, `ask`.
- **Four scopes** (in precedence order): `session` < `localProject` <
  `sharedProject` < `user`.
- **Decision pipeline** with deny-first ordering and per-tool
  `CheckPermissions` extension point.
- **Approval prompt with suggestions** — "Approve, and remember:
  `shell_exec(prefix:npm)`" with destination picker.
- **Settings persistence** in Crest's `wconfig` settings.json.
- **Bypass mode** (opt-in, with bypass-immune safety checks).
- **Plan mode** = read-only enforcement via tool-side policy (matches
  Claude's approach).
- **Migration path** — existing 4 modes (`ask/plan/do/bench`) become
  rule presets so existing chats keep working.

### v1 out-of-scope (pushed to v2)

- **Classifier-based auto-approve** (Claude's `auto`/YOLO mode + LLM
  transcript classifier). Defer until we ship rules first; classifier
  is a separate large lift.
- **PermissionRequest hooks** (webhook callouts). Crest has no hook
  framework yet; building one is out of scope for permissions.
- **Policy tier** (admin/enterprise managed settings). Skip until a
  user asks.
- **CLI rule management commands** (`crest permissions add`). Settings
  JSON edit is enough for v1; CLI is a v3 polish item.
- **Per-additional-directory rules** (Claude's `additionalDirectories`).
  Useful but adds surface area; defer.
- **Mode cycling UX** (Shift+Tab to cycle through modes inline).
  Frontend polish, not core mechanism.

### Non-goals (probably never)

- 1:1 type/method copy of Claude's TypeScript code. Adapt idioms to
  Go (e.g. struct embedding instead of discriminated unions; channels
  instead of async/await).
- Statsig-style feature gates (`tengu_iron_gate_closed`,
  `tengu_auto_mode_config`). Crest is single-tenant — these are
  Anthropic-internal concerns.

---

## 3. Open Product Questions — Answered

### 3.1 Rule grammar — globs, prefixes, structured?

**Decision:** All three, matching Claude's surface but Go-typed.

```go
// pkg/agent/permissions/rule.go
type Rule struct {
    Behavior   RuleBehavior // "allow" | "deny" | "ask"
    ToolName   string       // e.g. "shell_exec", "edit_text_file", "*" for any tool
    Content    string       // optional; tool-specific matcher syntax
    Source     RuleSource   // who set it (precedence)
    AddedAt    int64        // unix ms; for UI only
}

type RuleBehavior string
const (
    RuleAllow RuleBehavior = "allow"
    RuleDeny  RuleBehavior = "deny"
    RuleAsk   RuleBehavior = "ask"
)
```

**Tool-name format** — exact tool name or `*`. MCP tools use the
existing `mcp__server__tool` convention; `mcp__server__*` matches all
tools from one server. (Claude does this same thing.)

**Content matcher** — interpreted by each tool's `MatchContent` method:

| Tool | Matcher syntax | Examples |
|---|---|---|
| `shell_exec` | exact, or `prefix:<cmd>` | `npm install`, `prefix:git`, `prefix:cargo build` |
| `edit_text_file`, `write_text_file`, `multi_edit` | gitignore-style glob over absolute path | `/Users/me/repo/**`, `**/*.go`, `!**/secrets/**` |
| `read_text_file`, `read_dir` | gitignore-style glob | same |
| `web_fetch` | URL host/path glob | `https://api.github.com/**`, `https://*.internal/**` |
| `browser.navigate` | URL host glob | `https://github.com/*` |
| `spawn_task` | mode name | `do`, `ask` |
| any other | exact-match content | (rarely needed) |

Empty `Content` matches *any* call to that tool.

**Wire format** in settings.json (compact, human-friendly):

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
    "defaultMode": "do"
  }
}
```

(No `bypassEnabled` flag — `bypassPermissions` is freely selectable
like any other mode. The act of picking it is the consent.)

Parser: `ParseRule(s string) (Rule, error)` splits on the first `(`,
escapes `\)` and `\\`. Rejects malformed strings at load time so
typos don't silently neuter a rule.

### 3.2 Scopes — what persists where, who wins?

**Decision:** Four runtime scopes + one CLI scope. Strict precedence
(higher beats lower):

| # | Scope | Where it lives | Who writes |
|---|---|---|---|
| 5 | `cliArg` | runtime, never persisted | `--allow-tool`, `--deny-tool` flags (future) |
| 4 | `user` | `~/.config/waveterm/settings.json` `ai:permissions` | Settings UI / hand-edit |
| 3 | `sharedProject` | `<cwd>/.crest/permissions.json` | committed to repo |
| 2 | `localProject` | `<cwd>/.crest/permissions.local.json` | gitignored |
| 1 | `session` | in-memory, lifetime of the agent chat | Approval prompt |

**Decision rule:** when multiple scopes have rules for the same tool,
**deny in any scope wins**, then highest-precedence ask, then
highest-precedence allow. (Matches Claude's "deny is uncancellable"
principle.) Inside the same scope, more-specific content patterns
beat broader ones (e.g. `shell_exec(prefix:git push)` ask beats
`shell_exec(prefix:git)` allow).

**Why this layout:**
- `user` scope = personal preferences across all projects
  (`prefix:git status`, etc.)
- `sharedProject` = team conventions, committed (e.g. "always allow
  `npm test`, never allow `npm publish`")
- `localProject` = my-machine personal overrides per repo (gitignored)
- `session` = "remember for this chat only"
- No `policySettings` tier — Crest isn't enterprise-managed today.

### 3.3 Per-mode rules vs unified ruleset?

**Decision:** Modes are **rule presets**, not parallel rule namespaces.

Rationale: maintaining per-mode rule lists doubles user-facing
surface and creates confusion ("why doesn't my `npm install` rule
work — oh, I'm in `plan` mode now"). Claude's approach is the same:
a single rule pool, modes flip the *defaults* and the *posture*.

Concretely:
- **Mode** sets the global posture: which tools are even available,
  what the default behavior is when no rule matches, what
  bypass-immune safety checks fire.
- **Rules** override either way. A `deny` rule beats a permissive
  mode; an `allow` rule beats a restrictive mode.

Modes map as follows — `bench` stays unchanged (it's used by Harbor /
TB2 harnesses), and a new `bypassPermissions` mode joins the user-facing
set:

| Mode | Tools | Default on no-match | Safety checks | Audience |
|---|---|---|---|---|
| `ask` | read-only set | allow (reads are safe) | n/a | interactive read-only |
| `plan` | read-only + write_plan | allow for reads, deny mutating tools at tool-side | enforced | interactive planning |
| `do` | full mutation set | ask | enforced | interactive coding (default) |
| `bypassPermissions` | full mutation set | allow | enforced (bypass-immune list still fires) | "trust me" interactive |
| `bench` | bench set | allow | **off** | non-interactive eval harnesses (Harbor/TB2) |

Five modes total. Both `bypassPermissions` and `bench` auto-approve;
the difference is whether bypass-immune safety checks (`.git/`, `.ssh/`,
`rm -rf /`, etc.) force a prompt. Bench has to skip them so eval
tasks can do destructive things in their test sandbox without
prompting; bypass keeps them so a real user typing `:bypass` doesn't
accidentally lose their `.git` directory.

### 3.4 UI surface

**Decision:** Two surfaces, both inline in existing UI.

#### 3.4.1 Approval prompt with suggestions

Today: two buttons (Approve / Deny). New (matches Claude's prompt):

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

The suggestions come from the tool's `SuggestRules(input)` method.
Each tool implements its own suggestion logic (e.g. `shell_exec`
suggests prefix-based, exact, and tool-wide; `edit_text_file`
suggests parent-dir glob, exact-path, and tool-wide).

Save destinations:
- **Session** — in-memory, lasts until chat ends
- **This project** — `localProject` scope (gitignored)
- **Always** — `user` scope (~/.config)

Implementation: extend the existing `term-agent.tsx`
`TermAgentApprovalButtons` component. The existing wshrpc command
that sends approval back already exists; we extend the payload to
include suggested rules and destination.

#### 3.4.2 Settings rule editor

Crest has a Settings panel for AI providers; add a "Permissions" tab.
List all rules across all scopes, grouped by scope, with edit/delete
actions. Settings already round-trips through `wconfig` so writes are
covered.

(For v1 we can ship with read-only rule list; full editor in a
follow-up. The session-prompt-with-suggestions covers 80% of UX.)

### 3.5 Bypass mode — yes? when?

**Decision:** Yes, as a new mode `bypassPermissions` (Claude's name).
**Distinct from `bench`** — they look similar but serve different
purposes and we keep both.

| Mode | Audience | Safety checks | Used by |
|---|---|---|---|
| `bench` | Automated test harnesses (Harbor / TB2) | **all checks off** — pure auto-approve. Needs to allow `rm -rf` in test dirs, `sudo` in containers, etc. without prompting. | `eval/harbor/crest_agent.py` |
| `bypassPermissions` | Interactive user saying "I trust the agent" | bypass-immune safety checks **still fire** (`.git`/`.ssh`/`.env`/`rm -rf /`/`curl|sh`/etc. force a prompt) | `:bypass` overlay command |

The original draft conflated these because both auto-approve. They're
actually different: bench needs to be **fully** unguarded so eval
tasks can do whatever the task author wrote; bypass is a user-facing
"YOLO with a seatbelt" — auto-approves the 99% of safe things while
still blocking the catastrophic ones.

**No gating flag.** `bypassPermissions` is selectable like any other
mode. Users invoke it explicitly via the mode picker or a `:bypass`
prefix; the act of choosing it is the consent.

**Bypass-immune list** (only applies to `bypassPermissions`, not `bench`):

- `shell_exec`: `rm -rf /`, `rm -rf ~`, `rm -rf $HOME`,
  `git push --force` to main/master, anything containing
  `curl|sh` / `wget|sh`, `:(){:|:&};:` and the obvious fork-bomb
  patterns, `prefix:sudo`.
- File tools: writes to `.git/`, `.crest/`, `.ssh/`, `.aws/`,
  `.gnupg/`, OS shell configs (`.bashrc`, `.zshrc`, `.profile`),
  `.env*`, files containing `credentials` or `secret` in the name.
- `web_fetch` / `browser.navigate`: URLs with `localhost` /
  `127.0.0.1` on common dev ports (defer to v2; can be footgun for
  local dev).

Safety checks emit an `ask` decision with reason `"safetyCheck"`;
the prompt explains why bypass was overridden.

---

## 4. Architecture

### 4.1 Package layout

```
pkg/agent/permissions/         (new package)
├── permissions.go             // Engine, Decide, types
├── rule.go                    // Rule, ParseRule, Match
├── matcher_glob.go            // gitignore-style globs (use github.com/sabhiram/go-gitignore)
├── matcher_prefix.go          // prefix:<...> shell-command matching
├── safety.go                  // SafetyCheck, BypassImmuneList
├── store.go                   // RuleStore: load/save/scope precedence
├── store_session.go           // In-memory session scope
├── store_settings.go          // wconfig-backed user/project scope
└── permissions_test.go
```

Why a new top-level package: keeps the engine reusable beyond the
agent (e.g. future REPL mode, future MCP-only flows). Today's modes.go
imports it; tools call into it via a small interface to avoid a
package cycle.

### 4.2 Core types

```go
// pkg/agent/permissions/permissions.go

type Decision struct {
    Behavior  RuleBehavior  // "allow" | "deny" | "ask"
    Reason    DecisionReason
    Rule      *Rule          // populated when Behavior decided by a rule
    Mode      string         // populated when Behavior decided by mode
    Suggestions []Rule       // populated only when Behavior == Ask
    UpdatedInput map[string]any // tool-modified input (rare)
}

type DecisionReason struct {
    Kind   string  // "rule" | "mode" | "tool" | "safetyCheck" | "default"
    Detail string
    BypassImmune bool // safety checks: true; everything else false
}

type CheckRequest struct {
    ToolName string
    Input    map[string]any
    ChatId   string  // for session-scope rules
    Cwd      string  // for project-scope rule loading
    Mode     string  // current mode name
}

type Engine interface {
    Decide(ctx context.Context, req CheckRequest) Decision
    PersistRules(ctx context.Context, scope RuleScope, rules []Rule) error
    LoadRulesForChat(ctx context.Context, chatId, cwd string) ([]Rule, error)
}
```

### 4.3 Decision pipeline

Mirrors Claude Code's `hasPermissionsToUseToolInner` order:

```
Decide(req):
  1. Load rules from all scopes for (chat=req.ChatId, cwd=req.Cwd)
     → flat []Rule sorted by (scope precedence desc, content specificity desc)

  2. Tool-level deny?
     - any rule where ToolName matches and Content == "" and Behavior == Deny
     → Decision{Deny, reason=rule}

  3. Content-specific match (across all scopes)?
     - run tool.MatchContent(input, rule) for each rule with non-empty Content
     - first match (highest precedence) wins
     → Decision{rule.Behavior, reason=rule}

  4. Tool-level CheckPermissions?
     - per-tool hook, returns its own Decision
     - file tools: check bypass-immune paths → ask{safetyCheck, bypassImmune=true}
     - shell_exec: check bypass-immune commands → same
     - default: passthrough
     → if non-passthrough, return that decision

  5. Mode posture:
     a. bench/bypass mode + decision NOT bypass-immune → Decision{Allow, reason=mode}
     b. plan mode + tool is mutating → Decision{Deny, reason=mode}
     c. tool-level allow rule (no Content) → Decision{Allow, reason=rule}
     d. ask mode → tools default per old AutoApproveTools/RequireApproval

  6. Default: tool's static default
     - read tools: Allow
     - mutation tools: Ask
     → Decision{tool.DefaultBehavior, reason=default}

  7. Behavior == Ask?
     - tool.SuggestRules(input) populates Decision.Suggestions
     - return for UI to prompt user

  8. Behavior == Allow/Deny? return immediately, no prompt.
```

### 4.4 Tool integration — `PermissionedTool` interface

```go
// pkg/agent/permissions/tool_iface.go
type PermissionedTool interface {
    // MatchContent returns true if the rule's Content pattern matches
    // this specific tool input. Empty Content matches anything.
    MatchContent(input map[string]any, content string) bool

    // CheckPermissions runs tool-specific safety logic before the rule
    // engine's mode-based step. Most tools return Passthrough.
    CheckPermissions(input map[string]any) Decision

    // SuggestRules returns rule patterns that would auto-allow this
    // exact call in the future. Ordered most-specific to least.
    SuggestRules(input map[string]any) []Rule

    // DefaultBehavior is the fallback when no rule matches and no
    // mode posture applies.
    DefaultBehavior() RuleBehavior
}
```

Existing tool factories in `pkg/agent/tools/*.go` add these methods.
The `ToolDefinition` struct gets one new field:

```go
// pkg/aiusechat/uctypes/uctypes.go
type ToolDefinition struct {
    // ... existing fields
    Permissions PermissionAdapter  // optional; nil for tools w/o per-call logic
}

type PermissionAdapter interface {
    MatchContent(input map[string]any, pattern string) bool
    CheckPermissions(input map[string]any) (behavior, reason string, bypassImmune bool)
    SuggestRules(input map[string]any) []SuggestedRule
}
```

(Why duplicate the interface in `uctypes`? The pure permissions
package can't import `pkg/agent/tools` without a cycle. We define
the contract in `uctypes` and adapt it in `pkg/agent/permissions`.)

### 4.5 Replacing the existing approval flow

Today's flow:
```
processToolCallInternal (usechat.go)
  → toolCall.ToolUseData.Approval already set by registry.go
  → if NeedsApproval: WaitForToolApproval
```

New flow:
```
processToolCallInternal
  → engine.Decide(req) → Decision
  → switch Decision.Behavior:
     - Allow: set Approval=AutoApproved, run
     - Deny: set status=Error("denied: " + reason), skip run
     - Ask: set Approval=NeedsApproval, attach Decision.Suggestions
            for the FE; WaitForToolApproval; on user-approve, persist
            any selected suggestions via engine.PersistRules
```

The `mode.ResolveApproval` call in `pkg/agent/registry.go` is **gone**.
Modes are now consulted by the engine, not pre-baked into approval
strings at registration time.

### 4.6 Settings schema

```go
// pkg/wconfig/types.go (extension)
type SettingsType struct {
    // ... existing
    AIPermissions *AIPermissionsConfig `json:"ai:permissions,omitempty"`
}

type AIPermissionsConfig struct {
    Allow       []string `json:"allow,omitempty"`       // "shell_exec(prefix:npm)"
    Deny        []string `json:"deny,omitempty"`
    Ask         []string `json:"ask,omitempty"`
    DefaultMode string   `json:"defaultMode,omitempty"` // "ask"|"plan"|"do"|"bypassPermissions"|"bench"
}
```

Project-shared rules live at `<cwd>/.crest/permissions.json` (committed
to the repo); per-user-per-project overrides live at
`<cwd>/.crest/permissions.local.json` (gitignored). Same JSON shape as
the `ai:permissions` block above.

### 4.7 Frontend — approval prompt extension

Existing component: `frontend/app/view/term/term-agent.tsx`,
`TermAgentApprovalButtons`.

New props:
```ts
interface ApprovalProps {
    toolCallId: string
    toolName: string
    suggestions: SuggestedRule[]   // from Decision.Suggestions
    defaultDestination: 'session' | 'localProject' | 'user'
}
```

New buttons: existing Approve/Deny → "Approve and Remember" picker
with destination dropdown. New wshrpc command
`agent:tool-approve` carries optional `acceptedSuggestions: Rule[]`
and `destination: RuleScope` fields.

---

## 5. Migration & Rollout

### 5.1 Existing chats

The `ApprovalPolicy` struct on `Mode` becomes a *seed* rule set, not a
runtime policy. On first load, modes that had `RequireApproval`/`AutoApproveTools`
maps emit equivalent rules into the session scope so behavior is
identical out of the box. Users who never touch the new settings see
the same UX they have today.

### 5.2 Default rules shipped

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
  - shell_exec(prefix:cat)        # informational reads only
  - shell_exec(prefix:echo)

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

ask:                                # nothing — handled by mode default
```

These ship as in-binary defaults (lowest precedence, below `session`)
so the user can override any one. They only become "real" rules in
settings.json when the user persists a change via the UI.

### 5.3 Implementation order

1. **Types + parser** — `pkg/agent/permissions/{rule,permissions}.go`
   types, ParseRule/Stringify, unit tests for the grammar.
2. **Matchers** — glob (path), prefix (shell), exact. Unit tests.
3. **RuleStore** — load/save settings.json; precedence walk; project
   file loading. Unit tests.
4. **Engine.Decide** — full pipeline minus per-tool CheckPermissions.
   Unit tests against a fixture rule set.
5. **Tool adapters** — implement `PermissionAdapter` for `shell_exec`,
   `edit_text_file`, `multi_edit`, `write_text_file`, `read_text_file`,
   `web_fetch`, `browser.*`, `spawn_task`. Each with `SuggestRules`.
6. **Wire into usechat.go** — replace `mode.ResolveApproval` call with
   `engine.Decide(req)`. Bench-mode test parity.
7. **Frontend prompt** — extend `TermAgentApprovalButtons` with
   suggestions list and destination picker. New wshrpc payload.
8. **Settings UI** — read-only rule list under a new "Permissions"
   tab. (Editor in a follow-up.)
9. **Default rules** — bundle the v5.2 list into the engine's
   `inBinaryRules` source, lowest precedence.
10. **Documentation** — update `claude-code-parity.md` §3 status,
    write a short user-facing guide on how rules work.

Estimated: 2 sittings to step 6 (functional parity with mode behavior
+ rules), 1 more sitting for steps 7-10 (UI polish + defaults).

---

## 6. Decisions Log (v1 specific)

| Decision | Why |
|---|---|
| Skip classifier for v1 | Matches user's "ship rules first" call; classifier is a separate >1 sitting lift involving prompt design + Statsig-equivalent gating |
| `bypassPermissions` is a new mode separate from `bench` | They look similar (both auto-approve) but the audiences and safety posture differ — bench for non-interactive evals (no checks at all, can do `rm -rf` in test sandboxes); bypass for interactive "trust me" (still blocks `rm -rf /`, `.git`, `.ssh`, etc.). Resolved 2026-04-27 per user direction |
| No `bypassEnabled` gating flag | Picking the mode IS the consent; gating adds friction without safety value when the destructive paths are already bypass-immune. Resolved 2026-04-27 |
| Rules in a top-level `pkg/agent/permissions` package | Cleaner cycle story; reusable beyond the agent loop |
| `PermissionAdapter` lives on `ToolDefinition` (uctypes) | Keeps the engine pure; avoids `permissions → tools → permissions` cycle |
| In-binary defaults shipped (Allow git-status etc.) | Most users never customize anything; sane defaults set the floor |
| No CLI rule commands | Settings UI + JSON edit is sufficient; CLI is later polish |
| One unified rule pool, modes are presets | Mirrors Claude; per-mode rules confuse users |
| `localProject` is gitignored, `sharedProject` is committed | Standard convention from `.env.local` etc. |

---

## 7. Open Questions — Resolved

All four resolved 2026-04-27:

- **Q1 — bypass mode name and gating.** The "yolo with safety" mode is
  `bypassPermissions` (Claude's name), distinct from `bench`. Both
  auto-approve, but `bench` skips safety checks (for eval harnesses)
  and `bypassPermissions` keeps them. **No** `bypassEnabled` gating
  flag — the mode is freely selectable; choosing it is the consent.
- **Q2 — project rule file location.** `<cwd>/.crest/permissions.json`
  (shared) and `<cwd>/.crest/permissions.local.json` (gitignored).
  Matches the `.crest-plans` and `.crest-trajectories` convention.
- **Q3 — default "save to" destination.** `session` — least
  commitment; users move up the ladder (project / user) as they learn
  what they actually want persistent.
- **Q4 — `:bench` migration error vs fallback.** Moot, since (Q1)
  there's no `bypassEnabled` gating to fail against. Both `bench` and
  `bypassPermissions` are always selectable.

---

## 8. References

Claude Code source paths (reference only — do not import or copy
verbatim):

- `src/types/permissions.ts` — types, mode constants, rule shape
- `src/utils/permissions/permissions.ts` — `hasPermissionsToUseTool`,
  `hasPermissionsToUseToolInner` (the decision pipeline)
- `src/utils/permissions/PermissionMode.ts` — mode lifecycle
- `src/utils/permissions/getNextPermissionMode.ts` — Shift+Tab cycle
- `src/utils/permissions/permissionRuleParser.ts` — rule string parser
- `src/utils/permissions/applyPermissionUpdates.ts` — rule mutation
- `src/utils/permissions/permissionSetup.ts` — load, gates
- `src/utils/permissions/persistPermissionUpdates.ts` — save to disk
- `src/components/permissions/PermissionPrompt.tsx` — approval UI
- `src/hooks/toolPermission/PermissionContext.ts` — hook integration
- `src/tools/BashTool/bashPermissions.ts` — Bash content matching
- `src/tools/FileEditTool/filesystem.ts` — file path matching
