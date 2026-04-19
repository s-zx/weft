// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { ContextMenuModel } from "@/app/store/contextmenu";
import { atoms, createBlock, getApi } from "@/store/global";
import { fireAndForget } from "@/util/util";
import { useAtomValue } from "jotai";
import { memo, useEffect, useMemo, useRef } from "react";
import { getFileIcon } from "./file-icon";
import { FileExplorerModel, InlineEditState } from "./file-explorer-model";

// ---- Flat tree item ----
type FlatItem = {
    kind: "row";
    path: string;
    name: string;
    depth: number;
    isDir: boolean;
    finfo: FileInfo;
    expanded: boolean;
    selected: boolean;
} | {
    kind: "loading";
    parentPath: string;
    depth: number;
} | {
    kind: "error";
    parentPath: string;
    depth: number;
    error: string;
} | {
    kind: "editing";
    editType: "newfile" | "newfolder";
    parentPath: string;
    depth: number;
};

function buildFlatList(
    children: FileInfo[],
    depth: number,
    expanded: Set<string>,
    childrenCache: Map<string, FileInfo[]>,
    loadingPaths: Set<string>,
    errorMap: Map<string, string>,
    selectedPath: string | null,
    editing: InlineEditState,
    out: FlatItem[]
): void {
    for (const finfo of children) {
        const isDir = finfo.isdir ?? false;
        const isExpanded = isDir && expanded.has(finfo.path);
        out.push({
            kind: "row",
            path: finfo.path,
            name: finfo.name ?? finfo.path,
            depth,
            isDir,
            finfo,
            expanded: isExpanded,
            selected: finfo.path === selectedPath,
        });
        if (isDir && isExpanded) {
            // Inline-edit row (new file/folder) appears at the top of expanded dir
            if (editing && (editing.type === "newfile" || editing.type === "newfolder") && editing.parentPath === finfo.path) {
                out.push({ kind: "editing", editType: editing.type, parentPath: finfo.path, depth: depth + 1 });
            }
            const sub = childrenCache.get(finfo.path);
            if (sub) {
                buildFlatList(sub, depth + 1, expanded, childrenCache, loadingPaths, errorMap, selectedPath, editing, out);
            } else if (loadingPaths.has(finfo.path)) {
                out.push({ kind: "loading", parentPath: finfo.path, depth: depth + 1 });
            } else {
                const err = errorMap.get(finfo.path);
                if (err) out.push({ kind: "error", parentPath: finfo.path, depth: depth + 1, error: err });
            }
        }
    }
}

// ---- Inline edit input ----
const InlineInput = memo(({ depth, placeholder, onCommit, onCancel }: {
    depth: number;
    placeholder: string;
    onCommit: (value: string) => void;
    onCancel: () => void;
}) => {
    const ref = useRef<HTMLInputElement>(null);
    useEffect(() => { ref.current?.focus(); }, []);
    return (
        <div className="flex items-center gap-1 h-[22px]" style={{ paddingLeft: depth * 12 + 22 }}>
            <input
                ref={ref}
                type="text"
                placeholder={placeholder}
                className="flex-1 bg-accent/10 border border-accent/40 rounded px-1.5 text-[12px] text-primary outline-none min-w-0"
                style={{ height: 18 }}
                onKeyDown={(e) => {
                    if (e.key === "Enter") onCommit(e.currentTarget.value);
                    if (e.key === "Escape") onCancel();
                }}
                onBlur={(e) => onCommit(e.currentTarget.value)}
            />
        </div>
    );
});
InlineInput.displayName = "InlineInput";

// ---- Row component — zero atom reads ----
type RowProps = {
    item: Extract<FlatItem, { kind: "row" }>;
    editing: InlineEditState;
    fullConfig: FullConfigType;
    root: string;
};

const Row = memo(({ item, editing, fullConfig, root }: RowProps) => {
    const { path, name, depth, isDir, finfo, expanded, selected } = item;
    const model = FileExplorerModel.getInstance();
    const IconComp = getFileIcon(name, isDir, expanded);

    const isRenaming = editing?.type === "rename" && editing.path === path;

    const onContextMenu = (e: React.MouseEvent) => {
        e.preventDefault();
        e.stopPropagation();
        const menu: ContextMenuItem[] = [];

        if (isDir) {
            menu.push({
                label: "New File",
                click: () => model.startNewFile(path),
            });
            menu.push({
                label: "New Folder",
                click: () => model.startNewFolder(path),
            });
            menu.push({ type: "separator" });
        }

        if (isDir) {
            menu.push({
                label: "cd to directory",
                click: () => model.cdToDir(path),
            });
        }

        menu.push({
            label: "Open in new tab",
            click: () => fireAndForget(() =>
                isDir
                    ? model.openInNewTab(path)
                    : createBlock({ meta: { view: "preview", file: path, connection: "" } })
            ),
        });

        menu.push({
            label: "Reveal in Finder",
            click: () => getApi().openNativePath(isDir ? path : finfo.dir ?? path),
        });

        menu.push({ type: "separator" });

        menu.push({ label: "Rename", click: () => model.startRename(path, name) });
        menu.push({
            label: "Delete",
            click: () => {
                if (window.confirm(`Delete "${name}"?`)) {
                    fireAndForget(() => model.deleteFile(path));
                }
            },
        });

        menu.push({ type: "separator" });

        menu.push({
            label: "Copy Path",
            click: () => navigator.clipboard.writeText(path),
        });
        menu.push({
            label: "Copy Relative Path",
            click: () => {
                const rel = path.startsWith(root + "/") ? path.slice(root.length + 1) : path;
                navigator.clipboard.writeText(rel);
            },
        });

        ContextMenuModel.getInstance().showContextMenu(menu, e);
    };

    if (isRenaming) {
        return (
            <InlineInput
                depth={depth}
                placeholder={name}
                onCommit={(v) => fireAndForget(() => model.commitRename(path, v || name))}
                onCancel={() => model.cancelEditing()}
            />
        );
    }

    return (
        <div
            className={`flex items-center gap-1 h-[22px] pr-2 cursor-pointer select-none text-[13px] ${
                selected ? "bg-accent/20" : "hover:bg-white/5"
            }`}
            style={{ paddingLeft: depth * 12 + 6 }}
            onClick={() => {
                model.setSelected(path);
                if (isDir) fireAndForget(() => model.toggleExpand(path));
            }}
            onDoubleClick={() => {
                if (!isDir) fireAndForget(() => model.openFile(finfo));
            }}
            onContextMenu={onContextMenu}
            title={path}
        >
            <span className="w-3 shrink-0 text-secondary text-[9px] flex items-center justify-center">
                {isDir ? (
                    <i
                        className="fa fa-solid fa-chevron-right"
                        style={{ transform: expanded ? "rotate(90deg)" : "none", transition: "transform 120ms" }}
                    />
                ) : null}
            </span>
            <IconComp size={16} className={`shrink-0 ${isDir ? "text-secondary" : ""}`} />
            <span className="truncate">{name}</span>
        </div>
    );
});
Row.displayName = "Row";

const LoadingRow = memo(({ depth }: { depth: number }) => (
    <div className="flex items-center h-[22px] text-[12px] text-secondary italic" style={{ paddingLeft: depth * 12 + 22 }}>
        Loading…
    </div>
));
LoadingRow.displayName = "LoadingRow";

const ErrorRow = memo(({ depth, error }: { depth: number; error: string }) => (
    <div className="flex items-center h-[22px] text-[12px] text-error truncate" style={{ paddingLeft: depth * 12 + 22 }} title={error}>
        {error}
    </div>
));
ErrorRow.displayName = "ErrorRow";

const EditingRow = memo(({ item, depth }: { item: Extract<FlatItem, { kind: "editing" }>; depth: number }) => {
    const model = FileExplorerModel.getInstance();
    return (
        <InlineInput
            depth={depth}
            placeholder={item.editType === "newfile" ? "filename…" : "foldername…"}
            onCommit={(v) => {
                if (item.editType === "newfile") {
                    fireAndForget(() => model.commitNewFile(item.parentPath, v));
                } else {
                    fireAndForget(() => model.commitNewFolder(item.parentPath, v));
                }
            }}
            onCancel={() => model.cancelEditing()}
        />
    );
});
EditingRow.displayName = "EditingRow";

// ---- Tree root ----
export const FileExplorerTree = memo(() => {
    const model = FileExplorerModel.getInstance();

    const root = useAtomValue(model.rootAtom);
    const expanded = useAtomValue(model.expandedAtom);
    const loadingPaths = useAtomValue(model.loadingPathsAtom);
    const errorMap = useAtomValue(model.errorMapAtom);
    const selectedPath = useAtomValue(model.selectedPathAtom);
    const version = useAtomValue(model.childrenVersionAtom);
    const editing = useAtomValue(model.editingAtom);
    const fullConfig = useAtomValue(atoms.fullConfigAtom);

    useEffect(() => {
        if (!root) return;
        if (!model.getChildren(root) && !model.isLoading(root)) {
            fireAndForget(() => model.fetchChildren(root));
        }
        model.startAutoRefresh();
        return () => { /* keep subscription across re-mounts */ };
    }, [root]);

    const rootChildren = model.getChildren(root);
    const rootLoading = loadingPaths.has(root);
    const rootErr = errorMap.get(root);

    const flatItems = useMemo<FlatItem[]>(() => {
        if (!rootChildren) return [];
        const out: FlatItem[] = [];
        // New file/folder at root level
        if (editing && (editing.type === "newfile" || editing.type === "newfolder") && editing.parentPath === root) {
            out.push({ kind: "editing", editType: editing.type, parentPath: root, depth: 0 });
        }
        buildFlatList(rootChildren, 0, expanded, model.childrenCache, loadingPaths, errorMap, selectedPath, editing, out);
        return out;
    }, [rootChildren, expanded, loadingPaths, errorMap, selectedPath, version, editing]);

    if (rootLoading && !rootChildren) {
        return <div className="px-3 py-2 text-[12px] text-secondary italic">Loading…</div>;
    }
    if (rootErr && !rootChildren) {
        return <div className="px-3 py-2 text-[12px] text-error" title={rootErr}>{rootErr}</div>;
    }
    if (!rootChildren) return null;

    return (
        <>
            {flatItems.map((item) => {
                if (item.kind === "loading") {
                    return <LoadingRow key={`__loading__${item.parentPath}`} depth={item.depth} />;
                }
                if (item.kind === "error") {
                    return <ErrorRow key={`__error__${item.parentPath}`} depth={item.depth} error={item.error} />;
                }
                if (item.kind === "editing") {
                    return <EditingRow key={`__editing__${item.parentPath}`} item={item} depth={item.depth} />;
                }
                return (
                    <Row
                        key={item.path}
                        item={item}
                        editing={editing}
                        fullConfig={fullConfig}
                        root={root}
                    />
                );
            })}
        </>
    );
});
FileExplorerTree.displayName = "FileExplorerTree";
