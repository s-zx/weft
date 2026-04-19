// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import { cn, fireAndForget } from "@/util/util";
import { getApi } from "@/store/global";
import { useAtomValue } from "jotai";
import { memo, useEffect } from "react";
import { DiffLine, FileStats, GitChangedFile, GitModel } from "./git-model";

// ---- Stat badge: `+0 • -80` ----
const StatBadge = memo(({ add, del }: { add: number; del: number }) => (
    <span className="flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-mono bg-white/8 border border-white/10">
        <span className="text-green-400">+{add}</span>
        <span className="text-secondary/50 mx-0.5">•</span>
        <span className="text-red-400">-{del}</span>
    </span>
));
StatBadge.displayName = "StatBadge";

// ---- Status indicator ----
const STATUS_LABEL: Record<string, { label: string; color: string }> = {
    M: { label: "M", color: "text-yellow-400" },
    A: { label: "A", color: "text-green-400" },
    D: { label: "D", color: "text-red-400" },
    R: { label: "R", color: "text-blue-400" },
    "??": { label: "U", color: "text-secondary" },
};

// ---- Single diff line ----
const DiffLineRow = memo(({ line }: { line: DiffLine }) => {
    if (line.type === "header") {
        return <div className="px-3 py-px text-[10px] text-secondary/50 font-mono">{line.content}</div>;
    }
    if (line.type === "hunk") {
        return (
            <div className="px-3 py-px text-[10px] font-mono text-blue-400/80 bg-blue-950/20">
                {line.content}
            </div>
        );
    }
    const prefix = line.type === "add" ? "+" : line.type === "remove" ? "-" : " ";
    const cls =
        line.type === "add"
            ? "text-green-300 bg-green-950/30"
            : line.type === "remove"
            ? "text-red-300 bg-red-950/30"
            : "text-primary/70";
    return (
        <div className={cn("flex text-[11px] font-mono leading-[18px]", cls)}>
            <span className="w-5 shrink-0 text-center select-none opacity-60">{prefix}</span>
            <span className="whitespace-pre-wrap break-all flex-1 pr-2">{line.content}</span>
        </div>
    );
});
DiffLineRow.displayName = "DiffLineRow";

// ---- File row ----
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
    const s = STATUS_LABEL[file.status] ?? { label: file.status, color: "text-secondary" };

    return (
        <div className="border-b border-white/5">
            {/* Row header */}
            <div
                className="flex items-center gap-1.5 h-10 px-3 cursor-pointer hover:bg-white/5 group"
                onClick={() => fireAndForget(() => model.toggleExpand(file.path))}
            >
                {/* Chevron */}
                <i
                    className={cn(
                        "fa fa-solid text-[9px] text-secondary/60 shrink-0 transition-transform",
                        expanded ? "fa-chevron-down" : "fa-chevron-right"
                    )}
                />
                {/* Filename */}
                <span className="flex-1 min-w-0 text-[12px] truncate">
                    {dir && <span className="text-secondary/60">{dir}/</span>}
                    <span className="text-primary">{name}</span>
                </span>
                {/* Status */}
                <span className={cn("text-[10px] font-bold shrink-0", s.color)}>{s.label}</span>
                {/* Stats badge */}
                {stats ? (
                    <StatBadge add={stats.add} del={stats.del} />
                ) : loading ? (
                    <i className="fa fa-spinner fa-spin text-[10px] text-secondary/50" />
                ) : null}
                {/* Action icons — show on hover */}
                <div className="flex items-center gap-1.5 opacity-0 group-hover:opacity-100 transition-opacity ml-1">
                    <i
                        className="fa fa-solid fa-copy text-[10px] text-secondary/60 hover:text-primary cursor-pointer"
                        title="Copy path"
                        onClick={(e) => {
                            e.stopPropagation();
                            navigator.clipboard.writeText(file.path);
                        }}
                    />
                    <i
                        className="fa fa-solid fa-rotate-left text-[10px] text-secondary/60 hover:text-red-400 cursor-pointer"
                        title="Discard changes"
                        onClick={(e) => {
                            e.stopPropagation();
                            fireAndForget(() => model.discardFile(file.path));
                        }}
                    />
                    <i
                        className="fa fa-solid fa-arrow-up-right-from-square text-[10px] text-secondary/60 hover:text-primary cursor-pointer"
                        title="Open file"
                        onClick={(e) => {
                            e.stopPropagation();
                            const cwd = globalStore_get_cwd();
                            if (cwd) getApi().openNativePath(`${cwd}/${file.path}`);
                        }}
                    />
                </div>
            </div>
            {/* Inline diff */}
            {expanded && (
                <div className="bg-black/20 overflow-x-auto">
                    {loading && !diff ? (
                        <div className="px-3 py-2 text-[11px] text-secondary/60 italic">Loading diff…</div>
                    ) : diff && diff.length > 0 ? (
                        diff.map((line, i) => <DiffLineRow key={i} line={line} />)
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
        <div className="flex flex-col h-full border-l border-white/8 bg-[#16161e]">
            {/* ---- Header ---- */}
            <div className="flex items-center justify-between px-3 py-2 border-b border-white/8 shrink-0">
                <div className="flex items-center gap-2 text-[13px]">
                    <i className="fa fa-solid fa-code-pull-request text-secondary text-[11px]" />
                    <span className="font-semibold text-primary">Code review</span>
                </div>
                <div className="flex items-center gap-2">
                    {/* Expand/compress panel width */}
                    <button
                        type="button"
                        onClick={() => globalStore.set(layoutModel.codeReviewWideAtom, !isWide)}
                        className="text-[11px] text-secondary hover:text-primary cursor-pointer transition-colors"
                        title={isWide ? "Collapse panel" : "Expand panel"}
                    >
                        <i className={cn("fa fa-solid", isWide ? "fa-compress" : "fa-expand")} />
                    </button>
                    <button
                        type="button"
                        onClick={() => layoutModel.setCodeReviewVisible(false)}
                        className="text-[11px] text-secondary hover:text-primary cursor-pointer transition-colors"
                    >
                        <i className="fa fa-solid fa-xmark" />
                    </button>
                </div>
            </div>

            {/* ---- Stats bar ---- */}
            <div className="flex items-center gap-2 px-3 py-2 border-b border-white/8 shrink-0">
                <i className="fa fa-solid fa-code-branch text-accent text-[11px]" />
                <span className="text-[12px] font-semibold text-primary">{branch || "—"}</span>
                {files.length > 0 && (
                    <>
                        <i className="fa fa-solid fa-file text-secondary/40 text-[10px]" />
                        <span className="text-[12px] text-secondary">{files.length}</span>
                        <span className="text-green-400 text-[12px] font-mono">+{totalAdd}</span>
                        <span className="text-red-400 text-[12px] font-mono">-{totalDel}</span>
                    </>
                )}
            </div>

            {/* ---- Body ---- */}
            {loading && files.length === 0 ? (
                <div className="flex-1 flex items-center justify-center text-secondary text-[12px]">
                    <i className="fa fa-spinner fa-spin mr-2" />
                    Loading…
                </div>
            ) : error ? (
                <div className="flex-1 p-3 text-[12px] text-error">{error}</div>
            ) : !isRepo ? (
                <div className="flex-1 flex flex-col items-center justify-center gap-2 text-secondary text-[12px]">
                    <i className="fa fa-brands fa-git-alt text-[32px] opacity-20" />
                    <span>Not a git repository</span>
                </div>
            ) : files.length === 0 ? (
                <div className="flex-1 flex flex-col items-center justify-center gap-2 text-secondary text-[12px]">
                    <i className="fa fa-solid fa-check-circle text-[32px] text-accent/30" />
                    <span>No uncommitted changes</span>
                </div>
            ) : (
                <div className="flex-1 overflow-y-auto">
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
