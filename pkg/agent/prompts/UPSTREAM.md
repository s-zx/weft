# ForgeCode prompt upstream

The three agent-mode prompts in this directory (`ask.md`, `plan.md`, `do.md`)
and `shared_header.md` adapt structural patterns and several non-negotiable
rules from ForgeCode (https://github.com/tailcallhq/forgecode), Apache
License 2.0. See `../NOTICE` for attribution details.

What we borrowed:
- Three-mode decomposition (sage/muse/forge → ask/plan/do).
- Non-negotiable rules pattern: "Do what has been asked; nothing more, nothing less",
  no-unrequested-docs, code-citation format `filepath:startLine-endLine`, no-emojis.
- Tool usage guidelines: batched parallel calls, avoid mentioning tool names,
  loop-detection when repeating calls.
- `<terminal_context>` injection shape inspired by ForgeCode's system-info partial.

What diverges:
- All Crest-specific primitives (blockcontroller, cmdblock history, CreateBlock)
  are native to Crest, not ForgeCode.
- Plans target `.crest-plans/` rather than ForgeCode's `plans/` to make the
  project affordance explicit.
- Shell execution runs via a Crest-owned block (visible to the user) rather
  than a hidden subprocess — this is the opposite of ForgeCode's default.

## Upstream reference

- Upstream repo: https://github.com/tailcallhq/forgecode
- Pinned commit for this version of our prompts: `main@2026-04-23`
  (replace with SHA on next re-sync)
- Partial templates consulted: `forge-custom-agent-template.md`,
  `forge-partial-system-info.md`, `forge-doom-loop-reminder.md`.

## Re-sync cadence

Review ForgeCode's prompt directory quarterly. Look for net-new
non-negotiable rules, improved loop-detection language, or new response-shape
guidance that applies to Crest's UX. Do not blindly port — Crest's tool
surface and block-based UX warrant different framing in several places.
