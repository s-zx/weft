// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { createBlock, getApi, getFocusedBlockId } from "@/store/global";
import { fireAndForget, sleep, stringToBase64 } from "@/util/util";
import { formatRemoteUri } from "@/util/waveutil";
import * as jotai from "jotai";
import { debounce } from "throttle-debounce";
import { quote as shellQuote } from "shell-quote";
import { getCachedHome } from "./file-explorer-atoms";

function compareEntries(a: FileInfo, b: FileInfo): number {
    const aDir = a.isdir ? 1 : 0;
    const bDir = b.isdir ? 1 : 0;
    if (aDir !== bDir) return bDir - aDir;
    const an = (a.name ?? "").toLowerCase();
    const bn = (b.name ?? "").toLowerCase();
    if (an < bn) return -1;
    if (an > bn) return 1;
    return 0;
}

export type InlineEditState =
    | { type: "rename"; path: string; name: string }
    | { type: "newfile"; parentPath: string }
    | { type: "newfolder"; parentPath: string }
    | null;

export class FileExplorerModel {
    private static instance: FileExplorerModel | null = null;

    rootAtom: jotai.PrimitiveAtom<string>;
    expandedAtom: jotai.PrimitiveAtom<Set<string>>;
    selectedPathAtom: jotai.PrimitiveAtom<string | null>;
    loadingPathsAtom: jotai.PrimitiveAtom<Set<string>>;
    errorMapAtom: jotai.PrimitiveAtom<Map<string, string>>;
    childrenVersionAtom: jotai.PrimitiveAtom<number>;
    editingAtom: jotai.PrimitiveAtom<InlineEditState>;

    childrenCache: Map<string, FileInfo[]>;
    inFlight: Map<string, Promise<void>>;
    private watchedPaths: Set<string> = new Set();
    // Queue of directories that need refreshing — batched per debounce window.
    private pendingRefreshPaths: Set<string> = new Set();
    private debouncedFlushRefresh: () => void;
    // Keep old name for the rare case where we do need a full tree refresh (setRoot).
    private debouncedRefreshAll: () => void;

    private constructor() {
        this.rootAtom = jotai.atom(getCachedHome()) as jotai.PrimitiveAtom<string>;
        this.expandedAtom = jotai.atom(new Set<string>()) as jotai.PrimitiveAtom<Set<string>>;
        this.selectedPathAtom = jotai.atom(null) as jotai.PrimitiveAtom<string | null>;
        this.loadingPathsAtom = jotai.atom(new Set<string>()) as jotai.PrimitiveAtom<Set<string>>;
        this.errorMapAtom = jotai.atom(new Map<string, string>()) as jotai.PrimitiveAtom<Map<string, string>>;
        this.childrenVersionAtom = jotai.atom(0) as jotai.PrimitiveAtom<number>;
        this.editingAtom = jotai.atom(null) as jotai.PrimitiveAtom<InlineEditState>;
        this.childrenCache = new Map();
        this.inFlight = new Map();

        // Flush only the directories that actually changed (targeted refresh).
        this.debouncedFlushRefresh = debounce(400, () => {
            const paths = new Set(this.pendingRefreshPaths);
            this.pendingRefreshPaths.clear();
            for (const p of paths) {
                this.childrenCache.delete(p);
                this.inFlight.delete(p);
                fireAndForget(() => this.fetchChildren(p));
            }
        });
        // Full-tree refresh — kept for setRoot() only.
        this.debouncedRefreshAll = debounce(800, () => this.refreshAll());
    }

    static getInstance(): FileExplorerModel {
        if (!FileExplorerModel.instance) {
            FileExplorerModel.instance = new FileExplorerModel();
        }
        return FileExplorerModel.instance;
    }

    // ---- Auto-refresh via native fs.watch ----
    // VS Code / Warp use OS-level filesystem events (FSEvents on macOS, inotify on
    // Linux). We expose the same capability via the Electron preload's fs.watch().
    // A single watch per directory — no terminal-command coupling.

    startAutoRefresh(): void {
        const root = globalStore.get(this.rootAtom);
        this.watchPath(root);
    }

    private watchPath(path: string): void {
        if (this.watchedPaths.has(path)) return;
        this.watchedPaths.add(path);
        getApi().watchDir(path, () => {
            // Schedule a refresh for THIS directory only — not the whole tree.
            // Multiple rapid events for the same or different dirs are batched
            // in a single 400 ms debounce window.
            this.pendingRefreshPaths.add(path);
            this.debouncedFlushRefresh();
        });
    }

    private unwatchPath(path: string): void {
        if (!this.watchedPaths.has(path)) return;
        this.watchedPaths.delete(path);
        getApi().unwatchDir(path);
    }

    stopAutoRefresh(): void {
        for (const p of this.watchedPaths) getApi().unwatchDir(p);
        this.watchedPaths.clear();
    }

    private refreshAll(): void {
        const root = globalStore.get(this.rootAtom);
        const expanded = globalStore.get(this.expandedAtom);
        // Refresh every cached directory (root + all expanded dirs).
        const paths = [root, ...Array.from(expanded)];
        for (const p of paths) {
            if (this.childrenCache.has(p)) {
                this.childrenCache.delete(p);
                this.inFlight.delete(p);
                fireAndForget(() => this.fetchChildren(p));
            }
        }
    }

    // ---- Core ----

    getRootNow(): string {
        return globalStore.get(this.rootAtom);
    }

    getChildren(path: string): FileInfo[] | undefined {
        return this.childrenCache.get(path);
    }

    isExpanded(path: string): boolean {
        return globalStore.get(this.expandedAtom).has(path);
    }

    isLoading(path: string): boolean {
        return globalStore.get(this.loadingPathsAtom).has(path);
    }

    getError(path: string): string | undefined {
        return globalStore.get(this.errorMapAtom).get(path);
    }

    setSelected(path: string | null): void {
        globalStore.set(this.selectedPathAtom, path);
    }

    private setLoading(path: string, loading: boolean): void {
        const next = new Set(globalStore.get(this.loadingPathsAtom));
        if (loading) next.add(path); else next.delete(path);
        globalStore.set(this.loadingPathsAtom, next);
    }

    private setError(path: string, msg: string | null): void {
        const next = new Map(globalStore.get(this.errorMapAtom));
        if (msg == null) next.delete(path); else next.set(path, msg);
        globalStore.set(this.errorMapAtom, next);
    }

    async fetchChildren(path: string): Promise<void> {
        const existing = this.inFlight.get(path);
        if (existing) return existing;
        const p = (async () => {
            this.setLoading(path, true);
            this.setError(path, null);
            const entries: FileInfo[] = [];
            try {
                const uri = formatRemoteUri(path, "local");
                const stream = RpcApi.FileListStreamCommand(TabRpcClient, { path: uri }, null);
                for await (const chunk of stream) {
                    if (chunk?.fileinfo) entries.push(...chunk.fileinfo);
                }
                entries.sort(compareEntries);
                this.childrenCache.set(path, entries);
                globalStore.set(this.childrenVersionAtom, globalStore.get(this.childrenVersionAtom) + 1);
            } catch (e: any) {
                this.setError(path, e?.message ?? String(e));
            } finally {
                this.setLoading(path, false);
                this.inFlight.delete(path);
            }
        })();
        this.inFlight.set(path, p);
        return p;
    }

    async toggleExpand(path: string): Promise<void> {
        const current = globalStore.get(this.expandedAtom);
        const next = new Set(current);
        if (next.has(path)) {
            next.delete(path);
            globalStore.set(this.expandedAtom, next);
            this.unwatchPath(path);
            return;
        }
        next.add(path);
        globalStore.set(this.expandedAtom, next);
        this.watchPath(path);
        if (!this.childrenCache.has(path)) {
            await this.fetchChildren(path);
        }
    }

    setRoot(newRoot: string): void {
        const current = globalStore.get(this.rootAtom);
        if (current === newRoot) return;
        globalStore.set(this.rootAtom, newRoot);
        globalStore.set(this.expandedAtom, new Set());
        globalStore.set(this.errorMapAtom, new Map());
        this.childrenCache.clear();
        this.inFlight.clear();
        globalStore.set(this.childrenVersionAtom, globalStore.get(this.childrenVersionAtom) + 1);
        fireAndForget(() => this.fetchChildren(newRoot));
    }

    refresh(path: string): void {
        this.childrenCache.delete(path);
        this.inFlight.delete(path);
        fireAndForget(() => this.fetchChildren(path));
    }

    async openFile(finfo: FileInfo): Promise<void> {
        if (finfo.isdir) { await this.toggleExpand(finfo.path); return; }
        const blockDef: BlockDef = {
            meta: { view: "preview", file: finfo.path, connection: "" },
        };
        await createBlock(blockDef);
    }

    // ---- Inline editing ----

    startRename(path: string, name: string): void {
        globalStore.set(this.editingAtom, { type: "rename", path, name });
    }

    startNewFile(parentPath: string): void {
        if (!this.isExpanded(parentPath)) {
            fireAndForget(() => this.toggleExpand(parentPath));
        }
        globalStore.set(this.editingAtom, { type: "newfile", parentPath });
    }

    startNewFolder(parentPath: string): void {
        if (!this.isExpanded(parentPath)) {
            fireAndForget(() => this.toggleExpand(parentPath));
        }
        globalStore.set(this.editingAtom, { type: "newfolder", parentPath });
    }

    cancelEditing(): void {
        globalStore.set(this.editingAtom, null);
    }

    // ---- File operations ----

    private parentDir(path: string): string {
        const idx = path.lastIndexOf("/");
        return idx > 0 ? path.slice(0, idx) : path;
    }

    async commitRename(oldPath: string, newName: string): Promise<void> {
        globalStore.set(this.editingAtom, null);
        if (!newName.trim()) return;
        const dir = this.parentDir(oldPath);
        const newPath = `${dir}/${newName.trim()}`;
        try {
            await RpcApi.FileMoveCommand(TabRpcClient, {
                srcuri: formatRemoteUri(oldPath, "local"),
                desturi: formatRemoteUri(newPath, "local"),
            });
        } catch (e) {
            console.error("rename failed:", e);
        }
        this.refresh(dir);
    }

    async commitNewFile(parentPath: string, name: string): Promise<void> {
        globalStore.set(this.editingAtom, null);
        if (!name.trim()) return;
        const newPath = `${parentPath}/${name.trim()}`;
        try {
            await RpcApi.FileCreateCommand(TabRpcClient, { info: { path: formatRemoteUri(newPath, "local") } });
        } catch (e) {
            console.error("create file failed:", e);
        }
        this.refresh(parentPath);
    }

    async commitNewFolder(parentPath: string, name: string): Promise<void> {
        globalStore.set(this.editingAtom, null);
        if (!name.trim()) return;
        const newPath = `${parentPath}/${name.trim()}`;
        try {
            await RpcApi.FileMkdirCommand(TabRpcClient, { info: { path: formatRemoteUri(newPath, "local") } });
        } catch (e) {
            console.error("mkdir failed:", e);
        }
        this.refresh(parentPath);
    }

    async deleteFile(path: string): Promise<void> {
        const dir = this.parentDir(path);
        try {
            await RpcApi.FileDeleteCommand(TabRpcClient, {
                path: formatRemoteUri(path, "local"),
                recursive: true,
            });
        } catch (e) {
            console.error("delete failed:", e);
        }
        this.refresh(dir);
    }

    async cdToDir(dir: string): Promise<void> {
        const blockId = getFocusedBlockId();
        if (blockId) {
            // Inject "cd <dir>" into the currently focused terminal — same as typing it.
            const cmd = `cd ${shellQuote([dir])}\n`;
            RpcApi.ControllerInputCommand(TabRpcClient, {
                blockid: blockId,
                inputdata64: stringToBase64(cmd),
            });
        } else {
            // No focused terminal — open a new one at the target dir.
            await createBlock({ meta: { controller: "shell", view: "term", "cmd:cwd": dir } });
        }
    }

    async openInNewTab(dir: string): Promise<void> {
        // Create a new Wave tab, then open a terminal block at the given directory.
        getApi().createTab();
        await sleep(200); // wait for the new tab to initialize
        await createBlock({ meta: { controller: "shell", view: "term", "cmd:cwd": dir } });
    }
}
