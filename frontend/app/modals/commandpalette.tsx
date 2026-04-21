// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { focusedBlockCwdAtom, getCachedHome } from "@/app/fileexplorer/file-explorer-atoms";
import { createBlock, createTab, globalStore, replaceBlock } from "@/app/store/global";
import { modalsModel } from "@/app/store/modalmodel";
import {
    genericClose,
    handleCmdN,
    handleSplitHorizontal,
    handleSplitVertical,
    simpleCloseStaticTab,
    switchBlockInDirection,
    switchTab,
} from "@/app/store/keymodel";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import { getLayoutModelForStaticTab, NavigateDirection } from "@/layout/index";
import { cn, fireAndForget, makeIconClass } from "@/util/util";
import { useAtomValue } from "jotai";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ReactDOM from "react-dom";
import "./commandpalette.scss";

// ---- types ----

interface PaletteCommand {
    id: string;
    label: string;
    category: string;
    shortcut?: string[];
    icon: string;
    action: () => void;
}

// ---- static command list ----

function buildCommandList(): PaletteCommand[] {
    return [
        {
            id: "new-terminal",
            label: "New Terminal",
            category: "Create",
            shortcut: ["⌘", "N"],
            icon: "fa-solid fa-terminal",
            action: () => fireAndForget(handleCmdN),
        },
        {
            id: "split-horizontal",
            label: "Split Horizontal",
            category: "Create",
            shortcut: ["⌘", "D"],
            icon: "fa-solid fa-table-columns",
            action: () => fireAndForget(() => handleSplitHorizontal("after")),
        },
        {
            id: "split-vertical",
            label: "Split Vertical",
            category: "Create",
            shortcut: ["⇧", "⌘", "D"],
            icon: "fa-solid fa-table-rows",
            action: () => fireAndForget(() => handleSplitVertical("after")),
        },
        {
            id: "new-tab",
            label: "New Tab",
            category: "Create",
            shortcut: ["⌘", "T"],
            icon: "fa-solid fa-plus",
            action: () => fireAndForget(createTab),
        },
        {
            id: "open-launcher",
            label: "Open Launcher",
            category: "Create",
            shortcut: ["⌃", "⇧", "X"],
            icon: "fa-solid fa-grid-2",
            action: () => {
                const layoutModel = getLayoutModelForStaticTab();
                const node = globalStore.get(layoutModel.focusedNode);
                if (node != null) {
                    fireAndForget(() => replaceBlock(node.data.blockId, { meta: { view: "launcher" } }, true));
                }
            },
        },
        {
            id: "focus-up",
            label: "Focus Block Above",
            category: "Navigate",
            shortcut: ["⌃", "⇧", "↑"],
            icon: "fa-solid fa-arrow-up",
            action: () => switchBlockInDirection(NavigateDirection.Up),
        },
        {
            id: "focus-down",
            label: "Focus Block Below",
            category: "Navigate",
            shortcut: ["⌃", "⇧", "↓"],
            icon: "fa-solid fa-arrow-down",
            action: () => switchBlockInDirection(NavigateDirection.Down),
        },
        {
            id: "focus-left",
            label: "Focus Block Left",
            category: "Navigate",
            shortcut: ["⌃", "⇧", "←"],
            icon: "fa-solid fa-arrow-left",
            action: () => switchBlockInDirection(NavigateDirection.Left),
        },
        {
            id: "focus-right",
            label: "Focus Block Right",
            category: "Navigate",
            shortcut: ["⌃", "⇧", "→"],
            icon: "fa-solid fa-arrow-right",
            action: () => switchBlockInDirection(NavigateDirection.Right),
        },
        {
            id: "next-tab",
            label: "Next Tab",
            category: "Navigate",
            shortcut: ["⌘", "]"],
            icon: "fa-solid fa-chevron-right",
            action: () => switchTab(1),
        },
        {
            id: "prev-tab",
            label: "Previous Tab",
            category: "Navigate",
            shortcut: ["⌘", "["],
            icon: "fa-solid fa-chevron-left",
            action: () => switchTab(-1),
        },
        {
            id: "toggle-file-explorer",
            label: "Toggle File Explorer",
            category: "View",
            shortcut: ["⌘", "B"],
            icon: "fa-solid fa-sidebar",
            action: () => {
                const model = WorkspaceLayoutModel.getInstance();
                model.setVTabVisible(!model.getVTabVisible());
            },
        },
        {
            id: "magnify-block",
            label: "Magnify Current Block",
            category: "View",
            shortcut: ["⌘", "M"],
            icon: "fa-solid fa-magnifying-glass-plus",
            action: () => {
                const layoutModel = getLayoutModelForStaticTab();
                const node = globalStore.get(layoutModel.focusedNode);
                if (node != null) {
                    layoutModel.magnifyNodeToggle(node.id);
                }
            },
        },
        {
            id: "open-settings",
            label: "Open Settings",
            category: "View",
            icon: "fa-solid fa-gear",
            action: () => fireAndForget(() => createBlock({ meta: { view: "waveconfig" } })),
        },
        {
            id: "close-block",
            label: "Close Block",
            category: "Actions",
            shortcut: ["⌘", "W"],
            icon: "fa-solid fa-xmark",
            action: genericClose,
        },
        {
            id: "close-tab",
            label: "Close Tab",
            category: "Actions",
            shortcut: ["⇧", "⌘", "W"],
            icon: "fa-solid fa-circle-xmark",
            action: simpleCloseStaticTab,
        },
        {
            id: "about",
            label: "About",
            category: "Actions",
            icon: "fa-solid fa-circle-info",
            action: () => modalsModel.pushModal("AboutModal"),
        },
    ];
}

function filterCommands(commands: PaletteCommand[], query: string): PaletteCommand[] {
    if (!query.trim()) {
        return commands;
    }
    const lq = query.toLowerCase();
    return commands.filter((cmd) => cmd.label.toLowerCase().includes(lq) || cmd.category.toLowerCase().includes(lq));
}

function groupByCategory(commands: PaletteCommand[]): { category: string; items: PaletteCommand[] }[] {
    const groups = new Map<string, PaletteCommand[]>();
    for (const cmd of commands) {
        if (!groups.has(cmd.category)) {
            groups.set(cmd.category, []);
        }
        groups.get(cmd.category).push(cmd);
    }
    return Array.from(groups.entries()).map(([category, items]) => ({ category, items }));
}

// ---- match highlight ----

function MatchHighlight({ text, matchpos }: { text: string; matchpos?: number[] }) {
    if (!matchpos?.length) {
        return <>{text}</>;
    }
    const posSet = new Set(matchpos);
    return (
        <>
            {Array.from(text).map((ch, i) =>
                posSet.has(i) ? (
                    <span key={i} className="palette-match-char">
                        {ch}
                    </span>
                ) : (
                    <React.Fragment key={i}>{ch}</React.Fragment>
                )
            )}
        </>
    );
}

// ---- file icon helper ----

function fileIcon(suggestion: SuggestionType): string {
    if (suggestion.icon) {
        return makeIconClass(suggestion.icon, false) ?? "fa fa-solid fa-file";
    }
    const mime = suggestion["file:mimetype"] ?? "";
    if (mime === "directory") return "fa fa-solid fa-folder";
    return "fa fa-solid fa-file";
}

// ---- main component ----

const CommandPaletteModal = () => {
    const [query, setQuery] = useState("");
    const [selectedIdx, setSelectedIdx] = useState(0);
    const [fileResults, setFileResults] = useState<SuggestionType[]>([]);
    const [isSearching, setIsSearching] = useState(false);

    const inputRef = useRef<HTMLInputElement>(null);
    const listRef = useRef<HTMLDivElement>(null);
    const reqNumRef = useRef(0);

    const allCommands = useMemo(() => buildCommandList(), []);
    const { cwd } = useAtomValue(focusedBlockCwdAtom);

    const isCommandMode = query.startsWith(">");
    const cmdQuery = isCommandMode ? query.slice(1).trimStart() : "";
    const fileQuery = isCommandMode ? "" : query;

    const filteredCommands = useMemo(
        () => (isCommandMode ? filterCommands(allCommands, cmdQuery) : []),
        [allCommands, cmdQuery, isCommandMode]
    );

    const totalResults = isCommandMode ? filteredCommands.length : fileResults.length;

    // auto-focus input
    useEffect(() => {
        inputRef.current?.focus();
    }, []);

    // file search with debounce
    useEffect(() => {
        if (isCommandMode || !fileQuery) {
            setFileResults([]);
            setIsSearching(false);
            return;
        }
        const rn = ++reqNumRef.current;
        setIsSearching(true);
        let cancelled = false;
        const timer = setTimeout(async () => {
            try {
                const result = await RpcApi.FetchSuggestionsCommand(TabRpcClient, {
                    suggestiontype: "file",
                    query: fileQuery,
                    widgetid: "command-palette-files",
                    reqnum: rn,
                    "file:cwd": cwd ?? getCachedHome(),
                });
                if (cancelled || rn !== reqNumRef.current) return;
                setFileResults(result?.suggestions ?? []);
                setSelectedIdx(0);
            } catch {
                if (!cancelled && rn === reqNumRef.current) setFileResults([]);
            } finally {
                if (!cancelled && rn === reqNumRef.current) setIsSearching(false);
            }
        }, 80);
        return () => {
            cancelled = true;
            clearTimeout(timer);
        };
    }, [fileQuery, cwd, isCommandMode]);

    // reset selected on mode switch
    useEffect(() => {
        setSelectedIdx(0);
    }, [isCommandMode]);

    // scroll selected into view
    useEffect(() => {
        if (listRef.current == null) return;
        const selected = listRef.current.querySelector<HTMLElement>(".palette-item-selected");
        selected?.scrollIntoView({ block: "nearest" });
    }, [selectedIdx]);

    const close = useCallback(() => modalsModel.popModal(), []);

    const executeCommand = useCallback((cmd: PaletteCommand) => {
        // pop first so the modal cleanup runs synchronously before the action
        // (avoids focus-trap conflicts when an action opens another modal)
        modalsModel.popModal();
        cmd.action();
    }, []);

    const openFile = useCallback((suggestion: SuggestionType) => {
        const filePath = suggestion["file:path"];
        if (!filePath) return;
        modalsModel.popModal();
        fireAndForget(() => createBlock({ meta: { view: "preview", file: filePath } }));
    }, []);

    const handleKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (e.key === "Escape") {
                e.stopPropagation();
                close();
                return;
            }
            if (e.key === "ArrowDown") {
                e.preventDefault();
                setSelectedIdx((prev) => (prev + 1) % Math.max(totalResults, 1));
                return;
            }
            if (e.key === "ArrowUp") {
                e.preventDefault();
                setSelectedIdx((prev) => (prev - 1 + Math.max(totalResults, 1)) % Math.max(totalResults, 1));
                return;
            }
            if (e.key === "Enter") {
                e.preventDefault();
                if (isCommandMode) {
                    if (filteredCommands[selectedIdx] != null) {
                        executeCommand(filteredCommands[selectedIdx]);
                    }
                } else {
                    if (fileResults[selectedIdx] != null) {
                        openFile(fileResults[selectedIdx]);
                    }
                }
                return;
            }
        },
        [totalResults, isCommandMode, filteredCommands, fileResults, selectedIdx, close, executeCommand, openFile]
    );

    const groups = useMemo(() => groupByCategory(filteredCommands), [filteredCommands]);
    let globalIdx = -1;

    const placeholder = isCommandMode ? "Search commands..." : "Search files...";

    return ReactDOM.createPortal(
        <div className="command-palette-wrapper" onMouseDown={close}>
            <div className="command-palette" onMouseDown={(e) => e.stopPropagation()} onKeyDown={handleKeyDown}>
                {/* input row */}
                <div className="command-palette-input-row">
                    {isCommandMode ? (
                        <span className="command-palette-mode-icon">{">"}</span>
                    ) : (
                        <i className="fa-solid fa-magnifying-glass command-palette-search-icon" />
                    )}
                    <input
                        ref={inputRef}
                        type="text"
                        className="command-palette-input"
                        placeholder={placeholder}
                        value={query}
                        onChange={(e) => setQuery(e.target.value)}
                    />
                    {isSearching && <i className="fa-solid fa-spinner fa-spin command-palette-spinner" />}
                    {query && !isSearching && (
                        <button className="command-palette-clear-btn cursor-pointer" onClick={() => setQuery("")}>
                            <i className="fa-solid fa-xmark" />
                        </button>
                    )}
                </div>

                {/* file results */}
                {!isCommandMode && fileQuery && (
                    <div ref={listRef} className="command-palette-list">
                        {fileResults.map((s, idx) => (
                            <div
                                key={s.suggestionid}
                                className={cn("command-palette-item cursor-pointer", {
                                    "palette-item-selected": idx === selectedIdx,
                                })}
                                onMouseEnter={() => setSelectedIdx(idx)}
                                onClick={() => openFile(s)}
                            >
                                <i className={cn(fileIcon(s), "command-palette-item-icon")} />
                                <span className="command-palette-item-label">
                                    <MatchHighlight text={s.display} matchpos={s.matchpos} />
                                </span>
                                {s.subtext && (
                                    <span className="command-palette-file-subtext">{s.subtext}</span>
                                )}
                            </div>
                        ))}
                        {!isSearching && fileResults.length === 0 && (
                            <div className="command-palette-empty">No files found for &ldquo;{fileQuery}&rdquo;</div>
                        )}
                    </div>
                )}

                {/* command results */}
                {isCommandMode && (
                    <div ref={listRef} className="command-palette-list">
                        {groups.map(({ category, items }) => (
                            <div key={category}>
                                <div className="command-palette-category">{category}</div>
                                {items.map((cmd) => {
                                    globalIdx += 1;
                                    const idx = globalIdx;
                                    return (
                                        <div
                                            key={cmd.id}
                                            className={cn("command-palette-item cursor-pointer", {
                                                "palette-item-selected": idx === selectedIdx,
                                            })}
                                            onMouseEnter={() => setSelectedIdx(idx)}
                                            onClick={() => executeCommand(cmd)}
                                        >
                                            <i className={cn(cmd.icon, "command-palette-item-icon")} />
                                            <span className="command-palette-item-label">{cmd.label}</span>
                                            {cmd.shortcut != null && (
                                                <div className="command-palette-shortcut">
                                                    {cmd.shortcut.map((key, i) => (
                                                        <kbd key={i} className="command-palette-key">
                                                            {key}
                                                        </kbd>
                                                    ))}
                                                </div>
                                            )}
                                        </div>
                                    );
                                })}
                            </div>
                        ))}
                        {filteredCommands.length === 0 && cmdQuery && (
                            <div className="command-palette-empty">No commands found for &ldquo;{cmdQuery}&rdquo;</div>
                        )}
                    </div>
                )}

                {/* footer hint */}
                <div className="command-palette-footer">
                    {isCommandMode ? (
                        <span>Delete &lsquo;&gt;&rsquo; to search files</span>
                    ) : (
                        <span>
                            Type <kbd className="command-palette-key">&gt;</kbd> for commands
                        </span>
                    )}
                    <span className="command-palette-footer-nav">
                        <kbd className="command-palette-key">↑</kbd>
                        <kbd className="command-palette-key">↓</kbd> navigate &nbsp;
                        <kbd className="command-palette-key">↵</kbd> open
                    </span>
                </div>
            </div>
        </div>,
        document.getElementById("main") ?? document.body
    );
};

CommandPaletteModal.displayName = "CommandPaletteModal";

export { CommandPaletteModal };
