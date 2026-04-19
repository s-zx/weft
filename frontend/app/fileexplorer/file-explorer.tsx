// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import { useAtomValue } from "jotai";
import { memo, useEffect, useRef } from "react";
import { focusedBlockCwdAtom, getCachedHome } from "./file-explorer-atoms";
import { FileExplorerModel } from "./file-explorer-model";
import { FileExplorerTree } from "./file-explorer-tree";

function basename(path: string): string {
    if (path == null || path.length === 0) return "";
    const trimmed = path.endsWith("/") && path.length > 1 ? path.slice(0, -1) : path;
    const idx = trimmed.lastIndexOf("/");
    if (idx < 0) return trimmed;
    const tail = trimmed.slice(idx + 1);
    return tail.length > 0 ? tail : trimmed;
}

function prettyRoot(path: string): string {
    const home = getCachedHome();
    if (path === home) return "~";
    if (home && path.startsWith(home + "/")) return "~/" + basename(path);
    return basename(path) || path;
}

export const FileExplorer = memo(() => {
    const model = FileExplorerModel.getInstance();
    const root = useAtomValue(model.rootAtom);
    const { tabId, blockId, cwd } = useAtomValue(focusedBlockCwdAtom);

    const prevTabIdRef = useRef<string | null>(null);
    const prevBlockIdRef = useRef<string | null>(null);

    useEffect(() => {
        const isFirstMount = prevTabIdRef.current === null;
        const tabChanged = tabId !== prevTabIdRef.current;
        const sameBlock = blockId === prevBlockIdRef.current;

        prevTabIdRef.current = tabId;
        prevBlockIdRef.current = blockId;

        if (!cwd) return;

        if (isFirstMount || tabChanged) {
            // First mount or tab switch → follow the new tab's terminal cwd.
            if (cwd !== model.getRootNow()) model.setRoot(cwd);
            return;
        }

        if (!sameBlock) {
            // Same tab, different block (e.g. terminal + preview side-by-side)
            // → keep root unchanged.
            return;
        }

        // Same tab, same block, cwd changed (cd / pushd / popd / any shell cwd change)
        // → update immediately.
        if (cwd !== model.getRootNow()) model.setRoot(cwd);
    }, [tabId, blockId, cwd]);

    const onNewFile = () => model.startNewFile(root);
    const onNewFolder = () => model.startNewFolder(root);

    const onClose = () => {
        WorkspaceLayoutModel.getInstance().setFileExplorerVisible(false);
    };

    return (
        <div className="flex flex-col h-full w-full bg-black/20 text-primary overflow-hidden">
            <div className="flex items-center justify-between h-7 px-2 border-b border-white/10 shrink-0">
                <span
                    className="uppercase text-[11px] text-secondary tracking-wide truncate"
                    title={root}
                >
                    {prettyRoot(root)}
                </span>
                <div className="flex gap-0.5 shrink-0 text-secondary">
                    <button type="button" title="New File" onClick={onNewFile} className="cursor-pointer px-1 hover:text-primary transition-colors">
                        <i className="fa fa-solid fa-file-circle-plus fa-fw text-[11px]" />
                    </button>
                    <button type="button" title="New Folder" onClick={onNewFolder} className="cursor-pointer px-1 hover:text-primary transition-colors">
                        <i className="fa fa-solid fa-folder-plus fa-fw text-[11px]" />
                    </button>
                    <button type="button" title="Close" onClick={onClose} className="cursor-pointer px-1 hover:text-primary transition-colors">
                        <i className="fa fa-solid fa-xmark fa-fw text-[11px]" />
                    </button>
                </div>
            </div>
            <div className="flex-grow overflow-auto">
                <FileExplorerTree />
            </div>
        </div>
    );
});

FileExplorer.displayName = "FileExplorer";
