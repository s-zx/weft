// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { focusedCwdAtom } from "@/app/fileexplorer/file-explorer-atoms";
import { globalStore } from "@/app/store/jotaiStore";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { fireAndForget } from "@/util/util";
import { getApi } from "@/store/global";
import { debounce } from "throttle-debounce";
import * as jotai from "jotai";

export type GitChangedFile = {
    status: string;
    path: string;
    origPath?: string;
};

export type DiffLine = {
    type: "header" | "hunk" | "add" | "remove" | "context";
    content: string;
};

export type FileStats = {
    add: number;
    del: number;
};

export function parseStatusOutput(raw: string): GitChangedFile[] {
    const files: GitChangedFile[] = [];
    for (const line of raw.split("\n")) {
        if (!line.trim()) continue;
        const status = line.slice(0, 2).trim() || "M";
        const rest = line.slice(3);
        if (!rest) continue;
        if (rest.includes(" -> ")) {
            const [origPath, path] = rest.split(" -> ");
            files.push({ status, path, origPath });
        } else {
            files.push({ status, path: rest });
        }
    }
    return files;
}

export function parseDiffOutput(raw: string): DiffLine[] {
    const lines: DiffLine[] = [];
    for (const line of raw.split("\n")) {
        if (line.startsWith("diff --git") || line.startsWith("index ") || line.startsWith("--- ") || line.startsWith("+++ ")) {
            lines.push({ type: "header", content: line });
        } else if (line.startsWith("@@")) {
            lines.push({ type: "hunk", content: line });
        } else if (line.startsWith("+")) {
            lines.push({ type: "add", content: line.slice(1) });
        } else if (line.startsWith("-")) {
            lines.push({ type: "remove", content: line.slice(1) });
        } else if (line.length > 0) {
            lines.push({ type: "context", content: line.slice(1) });
        }
    }
    return lines;
}

export function countStats(lines: DiffLine[]): FileStats {
    let add = 0;
    let del = 0;
    for (const l of lines) {
        if (l.type === "add") add++;
        if (l.type === "remove") del++;
    }
    return { add, del };
}

export class GitModel {
    private static instance: GitModel | null = null;

    isRepoAtom: jotai.PrimitiveAtom<boolean>;
    branchAtom: jotai.PrimitiveAtom<string>;
    totalAddAtom: jotai.PrimitiveAtom<number>;
    totalDelAtom: jotai.PrimitiveAtom<number>;
    filesAtom: jotai.PrimitiveAtom<GitChangedFile[]>;
    expandedFilesAtom: jotai.PrimitiveAtom<Set<string>>;
    fileDiffsAtom: jotai.PrimitiveAtom<Map<string, DiffLine[]>>;
    fileStatsAtom: jotai.PrimitiveAtom<Map<string, FileStats>>;
    loadingAtom: jotai.PrimitiveAtom<boolean>;
    loadingFilesAtom: jotai.PrimitiveAtom<Set<string>>;
    errorAtom: jotai.PrimitiveAtom<string | null>;
    cwdAtom: jotai.PrimitiveAtom<string>;

    private watchedGitDir: string | null = null;
    private debouncedRefresh!: () => void;

    private constructor() {
        this.isRepoAtom = jotai.atom(false) as jotai.PrimitiveAtom<boolean>;
        this.branchAtom = jotai.atom("") as jotai.PrimitiveAtom<string>;
        this.totalAddAtom = jotai.atom(0) as jotai.PrimitiveAtom<number>;
        this.totalDelAtom = jotai.atom(0) as jotai.PrimitiveAtom<number>;
        this.filesAtom = jotai.atom([]) as jotai.PrimitiveAtom<GitChangedFile[]>;
        this.expandedFilesAtom = jotai.atom(new Set<string>()) as jotai.PrimitiveAtom<Set<string>>;
        this.fileDiffsAtom = jotai.atom(new Map()) as jotai.PrimitiveAtom<Map<string, DiffLine[]>>;
        this.fileStatsAtom = jotai.atom(new Map()) as jotai.PrimitiveAtom<Map<string, FileStats>>;
        this.loadingAtom = jotai.atom(false) as jotai.PrimitiveAtom<boolean>;
        this.loadingFilesAtom = jotai.atom(new Set<string>()) as jotai.PrimitiveAtom<Set<string>>;
        this.errorAtom = jotai.atom(null) as jotai.PrimitiveAtom<string | null>;
        this.cwdAtom = jotai.atom("~") as jotai.PrimitiveAtom<string>;
        this.debouncedRefresh = debounce(1000, () => fireAndForget(() => this.refresh()));
    }

    startAutoRefresh(): void {
        const cwd = globalStore.get(this.cwdAtom);
        // Watch the .git directory — any git operation (add, commit, checkout...)
        // modifies files under .git, triggering an immediate refresh.
        const gitDir = `${cwd}/.git`;
        if (this.watchedGitDir === gitDir) return;
        if (this.watchedGitDir) getApi().unwatchDir(this.watchedGitDir);
        this.watchedGitDir = gitDir;
        getApi().watchDir(gitDir, () => this.debouncedRefresh());
        // Also watch the working tree root for new/deleted untracked files.
        getApi().watchDir(cwd, () => this.debouncedRefresh());
    }

    stopAutoRefresh(): void {
        if (this.watchedGitDir) { getApi().unwatchDir(this.watchedGitDir); this.watchedGitDir = null; }
        const cwd = globalStore.get(this.cwdAtom);
        getApi().unwatchDir(cwd);
    }

    static getInstance(): GitModel {
        if (!GitModel.instance) {
            GitModel.instance = new GitModel();
        }
        return GitModel.instance;
    }

    syncCwd(): void {
        const cwd = globalStore.get(focusedCwdAtom);
        if (cwd && cwd !== globalStore.get(this.cwdAtom)) {
            globalStore.set(this.cwdAtom, cwd);
            globalStore.set(this.expandedFilesAtom, new Set());
            globalStore.set(this.fileDiffsAtom, new Map());
            globalStore.set(this.fileStatsAtom, new Map());
        }
    }

    async refresh(): Promise<void> {
        const cwd = globalStore.get(this.cwdAtom);
        if (!cwd) return;
        globalStore.set(this.loadingAtom, true);
        globalStore.set(this.errorAtom, null);
        try {
            const [info, statusResult] = await Promise.all([
                RpcApi.GetGitInfoCommand(TabRpcClient, cwd),
                RpcApi.RunLocalCmdCommand(TabRpcClient, {
                    cmd: "git",
                    args: ["status", "--short", "--porcelain"],
                    cwd,
                }),
            ]);
            globalStore.set(this.isRepoAtom, info.isrepo);
            globalStore.set(this.branchAtom, info.branch ?? "");
            globalStore.set(this.totalAddAtom, info.additions ?? 0);
            globalStore.set(this.totalDelAtom, info.deletions ?? 0);
            if (info.isrepo) {
                const files = parseStatusOutput(statusResult.stdout);
                globalStore.set(this.filesAtom, files);
                // Reload diffs for already-expanded files
                const expanded = globalStore.get(this.expandedFilesAtom);
                for (const path of expanded) {
                    fireAndForget(() => this.loadDiff(path));
                }
            }
        } catch (e: any) {
            globalStore.set(this.errorAtom, e?.message ?? String(e));
        } finally {
            globalStore.set(this.loadingAtom, false);
        }
    }

    private async loadDiff(path: string): Promise<void> {
        const cwd = globalStore.get(this.cwdAtom);
        const loading = new Set(globalStore.get(this.loadingFilesAtom));
        loading.add(path);
        globalStore.set(this.loadingFilesAtom, loading);
        try {
            const result = await RpcApi.RunLocalCmdCommand(TabRpcClient, {
                cmd: "git",
                args: ["diff", "--unified=3", "HEAD", "--", path],
                cwd,
            });
            let diffText = result.stdout;
            // If file is untracked, diff against /dev/null
            if (!diffText && result.exitcode !== 0) {
                diffText = result.stderr;
            }
            const lines = parseDiffOutput(diffText);
            const stats = countStats(lines);
            const diffs = new Map(globalStore.get(this.fileDiffsAtom));
            diffs.set(path, lines);
            const fileStats = new Map(globalStore.get(this.fileStatsAtom));
            fileStats.set(path, stats);
            globalStore.set(this.fileDiffsAtom, diffs);
            globalStore.set(this.fileStatsAtom, fileStats);
        } catch (e: any) {
            console.warn(`git diff ${path} failed:`, e);
        } finally {
            const l = new Set(globalStore.get(this.loadingFilesAtom));
            l.delete(path);
            globalStore.set(this.loadingFilesAtom, l);
        }
    }

    async toggleExpand(path: string): Promise<void> {
        const expanded = new Set(globalStore.get(this.expandedFilesAtom));
        if (expanded.has(path)) {
            expanded.delete(path);
            globalStore.set(this.expandedFilesAtom, expanded);
            return;
        }
        expanded.add(path);
        globalStore.set(this.expandedFilesAtom, expanded);
        const diffs = globalStore.get(this.fileDiffsAtom);
        if (!diffs.has(path)) {
            await this.loadDiff(path);
        }
    }

    expandAll(): void {
        const files = globalStore.get(this.filesAtom);
        const expanded = new Set(files.map((f) => f.path));
        globalStore.set(this.expandedFilesAtom, expanded);
        for (const f of files) {
            const diffs = globalStore.get(this.fileDiffsAtom);
            if (!diffs.has(f.path)) {
                fireAndForget(() => this.loadDiff(f.path));
            }
        }
    }

    collapseAll(): void {
        globalStore.set(this.expandedFilesAtom, new Set());
    }

    async discardFile(path: string): Promise<void> {
        const cwd = globalStore.get(this.cwdAtom);
        await RpcApi.RunLocalCmdCommand(TabRpcClient, {
            cmd: "git",
            args: ["checkout", "--", path],
            cwd,
        });
        await this.refresh();
    }
}
