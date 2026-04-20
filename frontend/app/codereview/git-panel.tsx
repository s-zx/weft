// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import { cn, fireAndForget } from "@/util/util";
import { getApi } from "@/store/global";
import { useAtomValue } from "jotai";
import { memo, useEffect, useMemo } from "react";
import { DiffLine, FileStats, GitChangedFile, GitModel } from "./git-model";

// ---- Icon button (shared header/action affordance) ----
const IconButton = memo(
    ({
        icon,
        onClick,
        title,
        danger,
    }: {
        icon: string;
        onClick: (e: React.MouseEvent) => void;
        title?: string;
        danger?: boolean;
    }) => (
        <button
            type="button"
            onClick={onClick}
            title={title}
            className={cn(
                "flex items-center justify-center w-6 h-6 rounded text-[11px] text-secondary/70 hover:bg-white/[0.06] transition-colors cursor-pointer",
                danger ? "hover:text-rose-400" : "hover:text-primary"
            )}
        >
            <i className={cn("fa fa-solid", icon)} />
        </button>
    )
);
IconButton.displayName = "IconButton";

// ---- Status dot (replaces single-letter pill) ----
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

// ---- Stat badge: `+0 −80` (tabular-nums, skeleton while loading) ----
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

// ---- Compute per-line old/new numbers by walking hunk headers ----
type NumberedLine = { line: DiffLine; oldNum?: number; newNum?: number };

function numberDiffLines(diff: DiffLine[]): NumberedLine[] {
    const out: NumberedLine[] = [];
    let oldNum = 0;
    let newNum = 0;
    const hunkRe = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;
    for (const line of diff) {
        if (line.type === "hunk") {
            const m = hunkRe.exec(line.content);
            if (m) {
                oldNum = parseInt(m[1], 10);
                newNum = parseInt(m[2], 10);
            }
            out.push({ line });
            continue;
        }
        if (line.type === "header") {
            out.push({ line });
            continue;
        }
        if (line.type === "add") {
            out.push({ line, newNum });
            newNum++;
        } else if (line.type === "remove") {
            out.push({ line, oldNum });
            oldNum++;
        } else {
            out.push({ line, oldNum, newNum });
            oldNum++;
            newNum++;
        }
    }
    return out;
}

// ---- Single diff line ----
const DiffLineRow = memo(({ item }: { item: NumberedLine }) => {
    const { line, oldNum, newNum } = item;
    if (line.type === "header") return null;
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
        <div
            className={cn(
                "flex text-[11px] font-mono leading-[18px] relative",
                isAdd && "bg-emerald-400/[0.06]",
                isDel && "bg-rose-400/[0.06]"
            )}
        >
            {(isAdd || isDel) && (
                <span
                    className={cn(
                        "absolute left-0 top-0 bottom-0 w-[2px]",
                        isAdd ? "bg-emerald-400" : "bg-rose-400"
                    )}
                />
            )}
            <span className="w-9 pl-2 pr-1 text-right text-[10px] text-secondary/40 tabular-nums select-none shrink-0">
                {isAdd ? "" : oldNum ?? ""}
            </span>
            <span className="w-9 pr-2 text-right text-[10px] text-secondary/40 tabular-nums select-none shrink-0">
                {isDel ? "" : newNum ?? ""}
            </span>
            <span
                className={cn(
                    "whitespace-pre-wrap break-all flex-1 pr-3",
                    isAdd && "text-emerald-100",
                    isDel && "text-rose-100",
                    !isAdd && !isDel && "text-primary/75"
                )}
            >
                {line.content}
            </span>
        </div>
    );
});
DiffLineRow.displayName = "DiffLineRow";

// ---- File block ----
type FileRowProps = {
    file: GitChangedFile;
    expanded: boolean;
    loading: boolean;
    stats?: FileStats;
    diff?: DiffLine[];
};

const FileRow = memo(({ file, expanded, loading, stats, diff }: FileRowProps) => {
    const model = GitModel.getInstance();
    const parts = file.path.split("/");
    const name = parts.pop() ?? file.path;
    const dir = parts.length > 0 ? parts.slice(-1)[0] : "";

    const numbered = useMemo(() => (diff ? numberDiffLines(diff) : []), [diff]);

    return (
        <div
            className={cn(
                "mx-3 rounded-lg border transition-colors overflow-hidden",
                expanded
                    ? "border-white/[0.14] bg-white/[0.03]"
                    : "border-white/[0.08] bg-white/[0.02] hover:bg-white/[0.04]"
            )}
        >
            {/* Header row */}
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
                {/* Action icons — show on hover */}
                <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity ml-1">
                    <IconButton
                        icon="fa-copy"
                        title="Copy path"
                        onClick={(e) => {
                            e.stopPropagation();
                            navigator.clipboard.writeText(file.path);
                        }}
                    />
                    <IconButton
                        icon="fa-rotate-left"
                        title="Discard changes"
                        danger
                        onClick={(e) => {
                            e.stopPropagation();
                            fireAndForget(() => model.discardFile(file.path));
                        }}
                    />
                    <IconButton
                        icon="fa-arrow-up-right-from-square"
                        title="Open file"
                        onClick={(e) => {
                            e.stopPropagation();
                            const cwd = globalStore_get_cwd();
                            if (cwd) getApi().openNativePath(`${cwd}/${file.path}`);
                        }}
                    />
                </div>
            </div>
            {/* Diff viewport */}
            {expanded && (
                <div className="border-t border-white/[0.06] bg-black/30 overflow-x-auto">
                    {loading && !diff ? (
                        <div className="px-3 py-2 text-[11px] text-secondary/60 italic">Loading diff…</div>
                    ) : numbered.length > 0 ? (
                        numbered.map((item, i) => <DiffLineRow key={i} item={item} />)
                    ) : (
                        <div className="px-3 py-2 text-[11px] text-secondary/60 italic">No diff available</div>
                    )}
                </div>
            )}
        </div>
    );
});
FileRow.displayName = "FileRow";

// Helper: grab cwd from model
function globalStore_get_cwd(): string | null {
    try {
        return GitModel.getInstance()
            ? (document.querySelector("[data-git-cwd]") as HTMLElement | null)?.dataset?.gitCwd ?? null
            : null;
    } catch {
        return null;
    }
}

// ---- Skeleton file block (loading state) ----
const FileSkeleton = memo(() => (
    <div className="mx-3 h-10 rounded-lg border border-white/[0.06] bg-white/[0.02] animate-pulse" />
));
FileSkeleton.displayName = "FileSkeleton";

// ---- Code Review right sidebar ----
export const GitReviewSidebar = memo(() => {
    const model = GitModel.getInstance();
    const isRepo = useAtomValue(model.isRepoAtom);
    const branch = useAtomValue(model.branchAtom);
    const totalAdd = useAtomValue(model.totalAddAtom);
    const totalDel = useAtomValue(model.totalDelAtom);
    const files = useAtomValue(model.filesAtom);
    const expanded = useAtomValue(model.expandedFilesAtom);
    const diffs = useAtomValue(model.fileDiffsAtom);
    const fileStats = useAtomValue(model.fileStatsAtom);
    const loadingFiles = useAtomValue(model.loadingFilesAtom);
    const loading = useAtomValue(model.loadingAtom);
    const error = useAtomValue(model.errorAtom);

    useEffect(() => {
        model.syncCwd();
        fireAndForget(() => model.refresh());
        model.startAutoRefresh();
    }, []);

    const layoutModel = WorkspaceLayoutModel.getInstance();
    const isWide = useAtomValue(layoutModel.codeReviewWideAtom);

    return (
        <div className="flex flex-col h-full border-l border-white/[0.08] bg-panel">
            {/* ---- Title bar ---- */}
            <div className="flex items-center justify-between h-10 px-3 border-b border-white/[0.06] shrink-0">
                <div className="flex items-center gap-2">
                    <i className="fa fa-solid fa-code-pull-request text-accent text-[12px]" />
                    <span className="text-[13px] font-semibold tracking-tight text-primary">Code review</span>
                </div>
                <div className="flex items-center gap-1">
                    <IconButton
                        icon={isWide ? "fa-compress" : "fa-expand"}
                        title={isWide ? "Collapse panel" : "Expand panel"}
                        onClick={() => globalStore.set(layoutModel.codeReviewWideAtom, !isWide)}
                    />
                    <IconButton
                        icon="fa-xmark"
                        title="Close"
                        onClick={() => layoutModel.setCodeReviewVisible(false)}
                    />
                </div>
            </div>

            {/* ---- Branch summary card ---- */}
            {isRepo && (
                <div className="mx-3 mt-3 rounded-lg border border-white/[0.08] bg-white/[0.025] px-3 py-2.5 shrink-0">
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
            )}

            {/* ---- Body ---- */}
            {loading && files.length === 0 ? (
                <div className="flex flex-col gap-2 mt-3 shrink-0">
                    <FileSkeleton />
                    <FileSkeleton />
                    <FileSkeleton />
                </div>
            ) : error ? (
                <div className="flex-1 flex items-center justify-center p-8">
                    <div className="flex flex-col items-center gap-3 text-center">
                        <i className="fa fa-solid fa-triangle-exclamation text-[18px] text-rose-400/80" />
                        <span className="text-[12px] text-secondary/80 max-w-[240px]">{error}</span>
                    </div>
                </div>
            ) : !isRepo ? (
                <div className="flex-1 flex items-center justify-center p-8">
                    <div className="flex flex-col items-center gap-3 text-secondary/70">
                        <i className="fa fa-brands fa-git-alt text-[18px] opacity-70" />
                        <span className="text-[12px]">Not a git repository</span>
                    </div>
                </div>
            ) : files.length === 0 ? (
                <div className="flex-1 flex items-center justify-center p-8">
                    <div className="flex flex-col items-center gap-3 text-secondary/70">
                        <i className="fa fa-solid fa-check text-[18px] text-emerald-400/70" />
                        <span className="text-[12px]">Working tree clean</span>
                    </div>
                </div>
            ) : (
                <div className="flex-1 overflow-y-auto py-3 flex flex-col gap-2">
                    {files.map((file) => (
                        <FileRow
                            key={file.path}
                            file={file}
                            expanded={expanded.has(file.path)}
                            loading={loadingFiles.has(file.path)}
                            stats={fileStats.get(file.path)}
                            diff={diffs.get(file.path)}
                        />
                    ))}
                </div>
            )}
        </div>
    );
});
GitReviewSidebar.displayName = "GitReviewSidebar";

// Keep old export for compat
export { GitReviewSidebar as GitReviewPanel };
