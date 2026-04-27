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

### 3.3 Two orthogonal axes — Mode vs Permission Posture

**Decision:** Treat **mode** and **permission posture** as two
separate, independently-toggleable axes. This is the bigger lesson
from Claude Code's design: permissions are not the same concept as
"what kind of work am I doing." Lumping `bypassPermissions` next to
`ask`/`plan`/`do` in a single mode picker confuses both axes — `ask`
is a *work mode*, `bypassPermissions` is a *permission posture*.

| Axis | Values | What it controls | How user changes it |
|---|---|---|---|
| **Mode** | `ask`, `plan`, `do` (+ hidden `bench`) | Tool list, system prompt, step budget — i.e. *what kind of work* the agent is doing | Mode picker UI / `:ask`/`:plan`/`:do` prefix |
| **Posture** | `default`, `acceptEdits`, `bypassPermissions` (+ hidden `bench` for eval) | *How strictly* tool calls are approved | `Shift+Tab` cycles / `/permission` command |

The two are orthogonal. The agent overlay shows both — e.g.
`do · default permissions` or `do · bypass`.

#### Modes (the work axis)

| Mode | Tools | Default behavior on no rule match | Visible | Audience |
|---|---|---|---|---|
| `ask` | read-only set | allow (reads are safe) | yes | interactive read-only |
| `plan` | read-only + `write_plan` | reads allowed; mutating tools refused at tool-side | yes | interactive planning |
| `do` | full mutation set | ask (or auto, depending on posture) | yes | interactive coding (default) |
| `bench` | bench-tuned set, 100-step budget | allow | **no** (API-only) | non-interactive eval harnesses (Harbor/TB2) |

`bench` stays a top-level mode rather than collapsing into a posture
of `do` because it carries different *budgets* (100 steps vs 40) and
a different *tool list* (no UI-only tools that can't work headless).
It also implies a special posture (no checks at all). Harbor adapter
posts `mode: "bench"` unchanged — backward-compatible.

#### Permission Postures (the strictness axis)

| Posture | Behavior on calls the rules don't match | Bypass-immune safety checks | Default? | Audience |
|---|---|---|---|---|
| `default` | Fall back to mode default (`do` → ask, `ask` → allow, etc.) | n/a (rules-only) | **yes** | normal use — the agent asks before every mutation |
| `acceptEdits` | Auto-allow **file-edit tools** (`edit_text_file`, `write_text_file`, `multi_edit`) when the target path is inside `cwd`. Everything else falls through to the `default` behavior. | **fire** (won't auto-allow edits to `.env`, `.git/`, `.ssh/`, etc.) | no | iterative file work — let the agent rewrite my code without clicking every diff, but keep shell prompts |
| `bypassPermissions` | Auto-allow everything | **fire** (`.git/`, `.ssh/`, `.env`, `rm -rf /`, `curl|sh`, `sudo`) | no | "trust me" — let the agent run without clicking every prompt |
| `bench` | Auto-allow everything | **off** | no — eval-only, not user-selectable | non-interactive eval; activated implicitly by `mode: "bench"` |

Posture state lives **per-chat** in the session. Reset on every new
chat to the user's `defaultPosture` setting (which itself defaults to
`default`). The user flips it with `Shift+Tab` (cycles
`default` → `acceptEdits` → `bypassPermissions` → `default`,
matching Claude Code's cycle minus the `plan` step we already split
out into the mode axis) or `/permission` (opens the chooser). The
overlay status indicator displays the current posture so the user
always knows what they're in.

**Posture × Mode interaction:** posture only changes behavior for
calls the rule engine would otherwise *ask* about. In `ask` mode
everything is reads → already auto-allowed → posture is a no-op. In
`plan` mode mutating tools are refused regardless of posture
(plan-mode tool restriction is bypass-immune). The posture really
matters in `do` mode, which is where users spend most of their time.

**The `acceptEdits` rationale:** the most common pain point in
interactive use is clicking through every `edit_text_file` while the
agent iterates on code. `acceptEdits` solves that without giving up
shell-command safety — the agent can rewrite your files freely but
must still ask before running anything. Bypass-immune file paths
(`.env`, `.git/`, `.ssh/`, files containing `credentials`/`secret`)
still prompt even in `acceptEdits` so the agent can't accidentally
overwrite something dangerous.

#### Per-mode rules

There is **one unified rule pool**, not per-mode rule namespaces.
Same rule set evaluated under any mode. Modes set the *default
behavior* when no rule matches; rules override either way. Rationale:
per-mode rule lists double the user-facing surface and create
confusion ("why doesn't my `npm install` rule work — oh, I'm in
`plan` mode"). Matches Claude's design.

#### Why the separation matters

- **Composability.** A user can flip into `bypassPermissions` and
  back without disturbing the agent's current work — the tool list,
  prompt, and budget all stay constant; only the approval strictness
  changes. With the previous (lumped) design, the user had to switch
  *modes* — losing budget continuity, tool availability, etc.
- **UI clarity.** Mode picker is a short list of qualitatively
  different work modes. Posture toggle is a 2-state knob with clear
  safety implications. Two axes, two affordances.
- **Settings layout.** `defaultMode` and `defaultPosture` are
  separate config keys; users can independently set per-axis defaults
  (e.g. "default mode = do, default posture = bypass on this trusted
  machine").

#### Wire format updates

Backend request body grows a new optional field:

```jsonc
// POST /api/post-agent-message
{
  "mode": "do",                       // ask | plan | do | bench
  "permissionPosture": "default",     // default | acceptEdits | bypassPermissions | bench
                                      // (omittable; falls back to settings.defaultPosture or "default")
  // ... existing fields
}
```

For backward compatibility, `mode: "bench"` is accepted as shorthand
for `mode: "bench", permissionPosture: "bench"` (the only way to set
the bench posture). Harbor adapter needs no changes.

### 3.4 UI surface

**Decision:** Three surfaces — approval prompt, posture toggle, and
settings rule list.

#### 3.4.0 Posture toggle

The mode picker UI lists the three work modes (`ask`/`plan`/`do`).
Permission posture is a separate affordance:

- **Status pill** in the agent overlay header, e.g.
  `do · permissions: default` (clickable to open `/permission`).
  Color-coded: neutral for `default`, info for `acceptEdits`,
  warning for `bypassPermissions` so the user always knows when
  they're in a looser posture.
- **Shift+Tab** keybinding cycles posture
  (`default` → `acceptEdits` → `bypassPermissions` → `default`).
  Matches Claude Code's cycle minus the `plan` step we already split
  out into the mode axis. Only active when the agent overlay has
  focus.
- **`/permission`** slash command opens a chooser dialog with the
  three postures + a brief explanation of each. Also offers a link
  to the rules editor and a checkbox for "set as default for new
  chats" (writes to `ai:permissions.defaultPosture`).
- **Persistence:** posture is a per-chat state (resets on new chat).
  Default posture for new chats is read from
  `ai:permissions.defaultPosture` (defaults to `default`).

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

### 3.5 Bypass-immune safety check list

The `acceptEdits` and `bypassPermissions` postures auto-approve calls
the rules don't match — but a fixed safety list overrides that and
forces a prompt regardless. `bench` posture (eval-only, no user to
prompt) skips this list entirely.

- **`shell_exec`:** `rm -rf /`, `rm -rf ~`, `rm -rf $HOME`,
  `git push --force` to `main`/`master`, anything containing
  `curl | sh` / `wget | sh`, fork-bomb patterns (`:(){:|:&};:` and
  obvious siblings), `prefix:sudo`.
- **File tools** (`edit_text_file`, `write_text_file`, `multi_edit`):
  writes to `.git/`, `.crest/`, `.ssh/`, `.aws/`, `.gnupg/`, OS shell
  configs (`.bashrc`, `.zshrc`, `.profile`, `.bash_profile`), `.env*`,
  files containing `credentials` or `secret` in the name.
- **`web_fetch` / `browser.navigate`:** URLs with `localhost` /
  `127.0.0.1` on common dev ports — defer to v2; can be a footgun for
  local dev where the agent is helping debug a server.

Safety checks emit an `ask` decision with reason `"safetyCheck"`. The
prompt UI explains why the looser posture was overridden so the user
isn't surprised by a sudden approval prompt mid-stream.

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
    Mode     string  // current mode name (ask/plan/do/bench)
    Posture  string  // current permission posture (default/acceptEdits/bypassPermissions/bench)
}

type Engine interface {
    Decide(ctx context.Context, req CheckRequest) Decision
    PersistRules(ctx context.Context, scope RuleScope, rules []Rule) error
    LoadRulesForChat(ctx context.Context, chatId, cwd string) ([]Rule, error)
}
```

### 4.3 Decision pipeline

Mirrors Claude Code's `hasPermissionsToUseToolInner` order. The
**mode** axis is consulted first (it can hard-deny mutating tools in
`plan`); then **rules**; then **per-tool safety checks**; then the
**posture** axis fills in unmatched cases. Bypass-immune safety wins
over any posture.

```
Decide(req):
  0. Mode-side hard refusal:
     - plan mode + tool is mutating → Decision{Deny, reason=mode-plan}
     (matches Claude's plan-mode read-only enforcement)

  1. Load rules from all scopes for (chat=req.ChatId, cwd=req.Cwd)
     → flat []Rule sorted by (scope precedence desc, content specificity desc)

  2. Tool-level deny?
     - any rule where ToolName matches and Content == "" and Behavior == Deny
     → Decision{Deny, reason=rule}

  3. Content-specific rule match (across all scopes)?
     - run tool.MatchContent(input, rule) for each rule with non-empty Content
     - first match (highest precedence) wins
     → Decision{rule.Behavior, reason=rule}

  4. Per-tool CheckPermissions (safety checks)?
     - file tools: bypass-immune paths → ask{safetyCheck, bypassImmune=true}
     - shell_exec: bypass-immune commands → same
     - default: passthrough
     → if non-passthrough, return that decision (immune to posture overrides)

  5. Tool-level allow rule?
     → Decision{Allow, reason=rule}

  6. Posture-driven default for unmatched calls:
     a. posture == bench → Decision{Allow, reason=posture-bench}
     b. posture == bypassPermissions → Decision{Allow, reason=posture-bypass}
     c. posture == acceptEdits AND tool is a file-edit AND target inside cwd
        → Decision{Allow, reason=posture-acceptEdits}
     d. posture == default → fall through

  7. Mode default:
     - ask mode → reads default Allow, anything else default Ask
     - do mode → mutations default Ask, reads default Allow
     → Decision{tool.DefaultBehavior, reason=default}

  8. Behavior == Ask?
     - tool.SuggestRules(input) populates Decision.Suggestions
     - return for UI to prompt user

  9. Behavior == Allow/Deny? return immediately, no prompt.
```

Step 4 runs *before* posture-driven auto-allow (step 6) so safety
checks remain bypass-immune in `acceptEdits` and `bypassPermissions`.
Step 0 runs first because plan-mode refusal is a property of the work
mode, not the permission system — a `bypassPermissions` posture
shouldn't unlock writes in `plan`.

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
    Allow          []string `json:"allow,omitempty"`          // "shell_exec(prefix:npm)"
    Deny           []string `json:"deny,omitempty"`
    Ask            []string `json:"ask,omitempty"`
    DefaultMode    string   `json:"defaultMode,omitempty"`    // "ask"|"plan"|"do" — bench is API-only
    DefaultPosture string   `json:"defaultPosture,omitempty"` // "default"|"acceptEdits"|"bypassPermissions"
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
   types (Rule, RuleBehavior, Posture, CheckRequest, Decision),
   ParseRule/Stringify, unit tests for the grammar.
2. **Matchers** — glob (path), prefix (shell), exact. Unit tests.
3. **RuleStore** — load/save settings.json; precedence walk; project
   file loading. Unit tests.
4. **Engine.Decide** — full pipeline including posture handling and
   the Step 0 plan-mode hard refusal. Unit tests against a fixture
   rule set covering each posture × mode combination.
5. **Tool adapters** — implement `PermissionAdapter` for `shell_exec`,
   `edit_text_file`, `multi_edit`, `write_text_file`, `read_text_file`,
   `web_fetch`, `browser.*`, `spawn_task`. Each with `SuggestRules`
   and (for file-edit tools) an `IsFileEdit() bool` flag so
   `acceptEdits` posture knows what to auto-allow.
6. **Wire into usechat.go** — replace `mode.ResolveApproval` call with
   `engine.Decide(req)`. Add `Posture` field to `Session` (per-chat
   state). API endpoint accepts `permissionPosture` field, defaulting
   to `settings.defaultPosture` then to `"default"`. Bench-mode test
   parity (Harbor still works unchanged).
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
| **Mode and Posture are orthogonal axes** | Conflating them (single picker with `ask`/`plan`/`do`/`bypassPermissions`) mixes two unrelated concepts. Mode = what work the agent does (tool list, prompt, budget); Posture = how strict approvals are. The user can flip into `bypassPermissions` mid-session without disturbing their work mode. Resolved 2026-04-27 per user direction |
| Posture set: `default` / `acceptEdits` / `bypassPermissions` (+ hidden `bench`) | Matches Claude Code's permission modes minus `plan` (which is a Crest *mode*, not a posture) and `dontAsk` (niche; defer to v2). `acceptEdits` is the highest-value addition — clicking through every code edit is the #1 friction in interactive use. Resolved 2026-04-27 per user direction |
| `Shift+Tab` cycles posture | Matches Claude's keybinding. Cycle order: `default` → `acceptEdits` → `bypassPermissions` → `default`. The `/permission` slash command is the alternative for users who don't know the keybinding |
| `bench` mode + `bench` posture stay implicitly coupled | When the API receives `mode: "bench"`, posture is forced to `bench` regardless of any explicit value. Eval harnesses don't think about posture; they just say "I'm running benchmarks." Backward compatible with Harbor adapter |
| `bench` hidden from user-facing mode picker (API-only) | A user picking `bench` from a picker would unwittingly disable safety. Harbor/eval is the only legitimate audience and they POST `mode: "bench"` directly. Frontend mode list: `[ask, plan, do]`. Posture toggle is the user-facing knob for strictness. Resolved 2026-04-27 per user direction |
| No `bypassEnabled` gating flag | Picking a posture IS the consent; gating adds friction without safety value when the destructive paths are already bypass-immune. Resolved 2026-04-27 |
| Rules in a top-level `pkg/agent/permissions` package | Cleaner cycle story; reusable beyond the agent loop |
| `PermissionAdapter` lives on `ToolDefinition` (uctypes) | Keeps the engine pure; avoids `permissions → tools → permissions` cycle |
| In-binary defaults shipped (Allow git-status etc.) | Most users never customize anything; sane defaults set the floor |
| No CLI rule commands | Settings UI + JSON edit is sufficient; CLI is later polish |
| One unified rule pool, modes are presets | Mirrors Claude; per-mode rules confuse users |
| `localProject` is gitignored, `sharedProject` is committed | Standard convention from `.env.local` etc. |

---

## 7. Open Questions — Resolved

All four original questions resolved 2026-04-27, plus a fifth round
of structural feedback resolved same day:

- **Q1 — bypass mode name and gating.** `bypassPermissions` is the
  user-facing "trust me" posture (Claude's name). `bench` stays as a
  separate eval-only construct. **No** gating flag — picking it IS
  the consent.
- **Q2 — project rule file location.** `<cwd>/.crest/permissions.json`
  (shared) and `<cwd>/.crest/permissions.local.json` (gitignored).
  Matches the `.crest-plans` and `.crest-trajectories` convention.
- **Q3 — default "save to" destination.** `session` — least
  commitment; users move up the ladder (project / user) as they learn
  what they actually want persistent.
- **Q4 — `:bench` migration error vs fallback.** Moot — no gating
  flag to fail against. `bench` is always API-accepted; never
  user-selectable.
- **Q5 — Mode vs Posture separation.** User feedback: lumping
  `bypassPermissions` next to `ask`/`plan`/`do` in a single picker
  conflates two unrelated concepts. Resolution: split into two
  orthogonal axes (mode = tools/prompt/budget; posture =
  strictness). User-facing posture set is `default` (default),
  `acceptEdits` (Shift+Tab), `bypassPermissions`. Bench remains a
  privileged mode+posture pair the API can request but the UI
  doesn't expose.

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
