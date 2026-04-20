# Code Review Panel — Warp-inspired Redesign Spec

**Target file:** `frontend/app/codereview/git-panel.tsx`
**Related:** `frontend/app/codereview/git-model.ts` (do NOT change — read-only reference)
**Framework:** React + Tailwind v4, Jotai atoms via `GitModel.getInstance()`
**Theme:** Dark-only (matches surrounding app). All tokens below should resolve via existing Tailwind theme variables (`text-primary`, `text-secondary`, `bg-accent`, etc.) — do NOT hardcode `#16161e` or raw hex in the component.

---

## 1. Design direction

Warp's interface treats each shell command as a **block** — a self-contained, rounded, hairline-bordered card with generous internal padding, where metadata sits in a compact header row and payload content flows below. The overall feel is:

- **Refined dark**, not pitch black. Surfaces are layered (`bg`, `bg-elevated`, `bg-elevated+1`) with ~3% luminance steps between them.
- **One warm accent**, used sparingly — for the active branch, focused block border, and the primary CTA. Never for decoration.
- **Mono-first numerics**. All counts, SHAs, line numbers, and diff stats are tabular-lining mono. Labels are a geometric sans.
- **Hairlines over fills**. Dividers are 1px at ~8% opacity; rows never get heavy backgrounds except for diff add/remove which use a left-edge indicator bar + very faint tint (4–6% not 30%).
- **Quiet motion**. 120–180 ms ease-out transitions. Nothing bounces. Chevron rotates, block background lightens 2%, and the diff region height-animates on expand.

Apply this to the code review panel by converting the current flat row list into a **stack of file blocks**, elevating the branch summary into a dedicated header card, and reworking the diff viewport to mirror Warp's command-output frame.

---

## 2. Layout anatomy

```
┌───────────────────────────────────────────────┐
│ [◧] Code review                      ⤢    ✕   │   <- Title bar (unchanged behavior)
├───────────────────────────────────────────────┤
│ ╭───────────────────────────────────────────╮ │
│ │  main                      7 files        │ │   <- Branch summary card
│ │  uncommitted               +862  −102     │ │
│ ╰───────────────────────────────────────────╯ │
│                                               │
│  ╭─ git-panel.tsx ───────────── M  +126 −101 ╮│   <- File block (collapsed)
│  ╰────────────────────────────────────────────╯│
│                                               │
│  ╭─ modalregistry.tsx ───────── M    +2  −0  ╮│   <- File block (expanded)
│  │ ┌───────────────────────────────────────┐ ││
│  │ │ @@ -6,7 +6,7 @@                       │ ││
│  │ │  6  import { UpgradeOnboardingModal…  │ ││
│  │ │+11  import { CommandPaletteModal…     │ ││
│  │ │ 12  import { UserInputModal…          │ ││
│  │ └───────────────────────────────────────┘ ││
│  ╰────────────────────────────────────────────╯│
└───────────────────────────────────────────────┘
```

Gutter around blocks: `px-3 py-2`. Space between blocks: `gap-2`. Blocks have `rounded-lg`, 1px hairline border, and internal `bg-white/[0.02]`.

---

## 3. Token map (Tailwind)

Stay within existing tokens; only add the listed utility classes:

| Role                          | Class                                          |
| ----------------------------- | ---------------------------------------------- |
| Panel background              | `bg-panel` (already in theme; fall back to `bg-[var(--panel-bg)]` if needed — do not hardcode hex) |
| Block surface                 | `bg-white/[0.025]`                             |
| Block hover surface           | `bg-white/[0.04]`                              |
| Block border                  | `border border-white/[0.08]`                   |
| Block border (expanded)       | `border-white/[0.14]`                          |
| Hairline divider              | `border-white/[0.06]`                          |
| Diff viewport surface         | `bg-black/30`                                  |
| Add indicator bar             | `bg-emerald-400`                               |
| Remove indicator bar          | `bg-rose-400`                                  |
| Add row tint                  | `bg-emerald-400/[0.06]`                        |
| Remove row tint               | `bg-rose-400/[0.06]`                           |
| Hunk header                   | `text-sky-300/70 bg-sky-400/[0.04]`            |
| Accent (branch, focused)      | `text-accent` / `bg-accent/80`                 |

**Typography** (no font-family changes — just weight/size/tracking):

| Element            | Classes                                             |
| ------------------ | --------------------------------------------------- |
| Panel title        | `text-[13px] font-semibold tracking-tight`          |
| Branch name        | `text-[13px] font-semibold text-primary`            |
| Branch sub-label   | `text-[11px] text-secondary/70 uppercase tracking-wider` |
| File name          | `text-[12px] font-medium text-primary`              |
| Directory prefix   | `text-[12px] text-secondary/55`                     |
| Stat counts        | `text-[11px] font-mono tabular-nums`                |
| Status letter      | `text-[10px] font-bold font-mono`                   |
| Diff line content  | `text-[11px] leading-[18px] font-mono`              |
| Line numbers       | `text-[10px] text-secondary/40 font-mono tabular-nums` |

---

## 4. Component spec

### 4.1 Title bar (unchanged structure, retuned visuals)

```tsx
<div className="flex items-center justify-between h-10 px-3 border-b border-white/[0.06] shrink-0">
  <div className="flex items-center gap-2">
    <i className="fa fa-solid fa-code-pull-request text-accent text-[12px]" />
    <span className="text-[13px] font-semibold tracking-tight text-primary">Code review</span>
  </div>
  <div className="flex items-center gap-1">
    <IconButton icon={isWide ? "fa-compress" : "fa-expand"} onClick={…} title={…} />
    <IconButton icon="fa-xmark" onClick={…} title="Close" />
  </div>
</div>
```

`IconButton` is a local helper (define at top of file):

```tsx
const IconButton = memo(({ icon, onClick, title }: { icon: string; onClick: () => void; title?: string }) => (
  <button
    type="button"
    onClick={onClick}
    title={title}
    className="flex items-center justify-center w-6 h-6 rounded text-[11px] text-secondary/70 hover:text-primary hover:bg-white/[0.06] transition-colors cursor-pointer"
  >
    <i className={cn("fa fa-solid", icon)} />
  </button>
));
IconButton.displayName = "IconButton";
```

### 4.2 Branch summary card

Replaces the existing stats bar. Sits in the gutter like any other block so the visual rhythm is consistent.

```tsx
<div className="mx-3 mt-3 rounded-lg border border-white/[0.08] bg-white/[0.025] px-3 py-2.5">
  <div className="flex items-center justify-between mb-1">
    <div className="flex items-center gap-2 min-w-0">
      <i className="fa fa-solid fa-code-branch text-accent text-[11px] shrink-0" />
      <span className="text-[13px] font-semibold text-primary truncate">{branch || "—"}</span>
    </div>
    <span className="text-[11px] font-mono tabular-nums text-secondary/70 shrink-0">
      {files.length} {files.length === 1 ? "file" : "files"}
    </span>
  </div>
  <div className="flex items-center justify-between">
    <span className="text-[10px] uppercase tracking-[0.08em] text-secondary/55">Uncommitted</span>
    <div className="flex items-center gap-2 font-mono tabular-nums text-[11px]">
      <span className="text-emerald-400">+{totalAdd}</span>
      <span className="text-secondary/30">·</span>
      <span className="text-rose-400">−{totalDel}</span>
    </div>
  </div>
</div>
```

Hide the card entirely when `!isRepo`; keep it visible with zeros when the repo is clean.

### 4.3 File block

Each file is its own rounded block — NOT a row in a table.

```tsx
<div
  className={cn(
    "mx-3 rounded-lg border transition-colors overflow-hidden",
    expanded
      ? "border-white/[0.14] bg-white/[0.03]"
      : "border-white/[0.08] bg-white/[0.02] hover:bg-white/[0.04]"
  )}
>
  {/* header row */}
  <div
    className="flex items-center gap-2 h-10 px-3 cursor-pointer group"
    onClick={() => fireAndForget(() => model.toggleExpand(file.path))}
  >
    <i
      className={cn(
        "fa fa-solid fa-chevron-right text-[9px] text-secondary/50 shrink-0 transition-transform duration-150",
        expanded && "rotate-90"
      )}
    />
    <StatusDot status={file.status} />
    <span className="flex-1 min-w-0 text-[12px] truncate">
      {dir && <span className="text-secondary/55">{dir}/</span>}
      <span className="text-primary font-medium">{name}</span>
    </span>
    <StatBadge add={stats?.add ?? 0} del={stats?.del ?? 0} loading={loading && !stats} />
    <FileActions file={file} />
  </div>

  {/* diff viewport */}
  {expanded && (
    <div className="border-t border-white/[0.06] bg-black/30">
      {/* …see §4.6 */}
    </div>
  )}
</div>
```

Blocks render in a flex column with `gap-2`; don't reuse `border-b` row separators.

### 4.4 StatusDot (replaces the single-letter pill)

A 6px colored dot with a hairline ring. The letter becomes a tooltip. This is the single biggest departure from the current design — it's quieter and matches Warp's language of dot-indicators for command status.

```tsx
const STATUS_META: Record<string, { color: string; label: string }> = {
  M: { color: "bg-amber-400", label: "Modified" },
  A: { color: "bg-emerald-400", label: "Added" },
  D: { color: "bg-rose-400", label: "Deleted" },
  R: { color: "bg-sky-400", label: "Renamed" },
  "??": { color: "bg-secondary/60", label: "Untracked" },
};

const StatusDot = memo(({ status }: { status: string }) => {
  const meta = STATUS_META[status] ?? { color: "bg-secondary/60", label: status };
  return (
    <span
      title={meta.label}
      className={cn("w-1.5 h-1.5 rounded-full shrink-0 ring-1 ring-black/40", meta.color)}
    />
  );
});
StatusDot.displayName = "StatusDot";
```

### 4.5 StatBadge (retuned)

Drop the border and the middle dot separator. Align digits with `tabular-nums`. When loading, render a 3-char-wide skeleton so width doesn't jump when the number arrives.

```tsx
const StatBadge = memo(({ add, del, loading }: { add: number; del: number; loading?: boolean }) => {
  if (loading) {
    return <span className="w-12 h-3 rounded bg-white/[0.06] animate-pulse shrink-0" />;
  }
  return (
    <span className="flex items-center gap-1 text-[11px] font-mono tabular-nums shrink-0">
      <span className="text-emerald-400">+{add}</span>
      <span className="text-rose-400">−{del}</span>
    </span>
  );
});
StatBadge.displayName = "StatBadge";
```

### 4.6 Diff viewport

Two changes from the current implementation:

1. **Left indicator bar instead of a full-row background tint.** A 2px colored bar flush to the left edge of the content area, plus a 6% tint. Much quieter, still scannable.
2. **Line numbers** on the left (mono, tabular, `text-secondary/40`). Use the hunk header to derive numbers; if unavailable, show blanks rather than a prefix character.

```tsx
const DiffLineRow = memo(({ line, oldNum, newNum }: { line: DiffLine; oldNum?: number; newNum?: number }) => {
  if (line.type === "header") return null;          // suppress the `diff --git` header — info is already in the block title
  if (line.type === "hunk") {
    return (
      <div className="px-3 py-1 text-[10px] font-mono text-sky-300/70 bg-sky-400/[0.04] border-y border-white/[0.05]">
        {line.content}
      </div>
    );
  }
  const isAdd = line.type === "add";
  const isDel = line.type === "remove";
  return (
    <div className={cn(
      "flex text-[11px] font-mono leading-[18px] relative",
      isAdd && "bg-emerald-400/[0.06]",
      isDel && "bg-rose-400/[0.06]"
    )}>
      {(isAdd || isDel) && (
        <span className={cn("absolute left-0 top-0 bottom-0 w-[2px]", isAdd ? "bg-emerald-400" : "bg-rose-400")} />
      )}
      <span className="w-8 pl-2 pr-1 text-right text-[10px] text-secondary/40 tabular-nums select-none shrink-0">
        {isAdd ? "" : oldNum ?? ""}
      </span>
      <span className="w-8 pr-2 text-right text-[10px] text-secondary/40 tabular-nums select-none shrink-0">
        {isDel ? "" : newNum ?? ""}
      </span>
      <span className={cn(
        "whitespace-pre-wrap break-all flex-1 pr-3",
        isAdd && "text-emerald-100",
        isDel && "text-rose-100",
        !isAdd && !isDel && "text-primary/75"
      )}>
        {line.content}
      </span>
    </div>
  );
});
```

Wrap lines with a `useMemo` that walks the diff once to compute `(oldNum, newNum)` per line from hunk headers. If that's non-trivial to retrofit (the current `DiffLine` type doesn't carry numbers), it's acceptable to drop both number columns and keep a single-column `+ / − / ·` prefix as a phase-1 fallback — but the indicator bar + 6% tint change is mandatory regardless.

### 4.7 File actions

Currently these appear only on hover, which hurts discoverability. Keep hover-only, but add a subtle always-on affordance: render an "overflow dots" button that's visible at all times; on hover expand the full action row inline.

If that's too large a change, at minimum bump the icons to `w-6 h-6` touch targets inside an `IconButton` wrapper (same component from §4.1) so users get a hover background and proper cursor, and space them with `gap-0.5` instead of `gap-1.5`.

### 4.8 Empty / loading / error states

Match the block rhythm. Each state gets a single centered block with generous padding, a muted icon, and a one-line label. No oversized `text-[32px]` icons — Warp uses 16–20px glyphs even in empty states and lets whitespace do the work.

```tsx
<div className="flex-1 flex items-center justify-center p-8">
  <div className="flex flex-col items-center gap-3 text-secondary/70">
    <i className="fa fa-solid fa-check text-[18px] text-emerald-400/70" />
    <span className="text-[12px]">Working tree clean</span>
  </div>
</div>
```

Loading state: replace the spinner + "Loading…" string with 3 skeleton blocks matching the file-block shape (`h-10 rounded-lg bg-white/[0.03] animate-pulse`), stacked in the gutter.

---

## 5. Behavioral changes

None that affect `GitModel`. The model stays untouched. All changes are presentational:

- Auto-refresh, expand/collapse, discardFile, and copy-path flows are preserved verbatim.
- `globalStore_get_cwd` helper (lines 149–157) stays — do not refactor that DOM lookup in this change.
- `WorkspaceLayoutModel.codeReviewWideAtom` continues to drive the `⤢` toggle.

---

## 6. Acceptance checklist

- [ ] No hardcoded hex colors remain in `git-panel.tsx` (the `#16161e` on line 184 is replaced).
- [ ] File list renders as a gap-separated stack of rounded blocks, not a bordered row list.
- [ ] Branch summary becomes a card with the `Uncommitted · +N · −N` pattern; no file-count icon.
- [ ] Status letter is replaced by a 6px colored dot with a tooltip.
- [ ] Diff rows use a 2px left indicator bar + 6% background tint (not 30%).
- [ ] All mono numerics use `tabular-nums`.
- [ ] Empty, loading, and error states follow the muted 18px-glyph pattern.
- [ ] Chevron uses `rotate-90` transform with a 150 ms transition (not a swap between two icons).
- [ ] Header icon buttons use the shared `IconButton` component with a hover background.
- [ ] Expanded block gets a brighter border (`border-white/[0.14]`) so it reads as focused.

---

## 7. Out of scope (do not do in this pass)

- Adding line numbers if `DiffLine` doesn't already expose them — leave a TODO and ship the prefix fallback.
- Changing `git-model.ts` or any atom shape.
- Adding new icons or an animation library.
- Introducing new CSS custom properties in `tailwindsetup.css`.
- Keyboard shortcuts for file navigation (separate proposal).
