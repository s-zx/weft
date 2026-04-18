// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { refocusNode } from "@/app/store/global";
import { validateCssColor } from "@/util/color-validator";
import { cn } from "@/util/util";
import { useCallback, useEffect, useRef, useState } from "react";
import { TabBadges } from "./tabbadges";

const RenameFocusDelayMs = 50;

export interface VTabItem {
    id: string;
    name: string;
    badge?: Badge | null;
    badges?: Badge[] | null;
    flagColor?: string | null;
    subtitle?: string;
    gitBranch?: string;
    gitAdds?: number;
    gitDels?: number;
    gitChangedFiles?: number;
}

interface VTabProps {
    tab: VTabItem;
    active: boolean;
    showDivider?: boolean;
    isDragging: boolean;
    isReordering: boolean;
    onSelect: () => void;
    onClose?: () => void;
    onRename?: (newName: string) => void;
    onContextMenu?: (event: React.MouseEvent<HTMLDivElement>) => void;
    onDragStart: (event: React.DragEvent<HTMLDivElement>) => void;
    onDragOver: (event: React.DragEvent<HTMLDivElement>) => void;
    onDrop: (event: React.DragEvent<HTMLDivElement>) => void;
    onDragEnd: () => void;
    onHoverChanged?: (isHovered: boolean) => void;
    renameRef?: React.RefObject<(() => void) | null>;
}

export function VTab({
    tab,
    active,
    showDivider = true,
    isDragging,
    isReordering,
    onSelect,
    onClose,
    onRename,
    onContextMenu,
    onDragStart,
    onDragOver,
    onDrop,
    onDragEnd,
    onHoverChanged,
    renameRef,
}: VTabProps) {
    const [originalName, setOriginalName] = useState(tab.name);
    const [isEditable, setIsEditable] = useState(false);
    const editableRef = useRef<HTMLDivElement>(null);
    const editableTimeoutRef = useRef<NodeJS.Timeout | null>(null);
    const badges = tab.badges ?? (tab.badge ? [tab.badge] : null);

    const rawFlagColor = tab.flagColor;
    let flagColor: string | null = null;
    if (rawFlagColor) {
        try {
            validateCssColor(rawFlagColor);
            flagColor = rawFlagColor;
        } catch {
            flagColor = null;
        }
    }

    useEffect(() => {
        setOriginalName(tab.name);
    }, [tab.name]);

    useEffect(() => {
        return () => {
            if (editableTimeoutRef.current) {
                clearTimeout(editableTimeoutRef.current);
            }
        };
    }, []);

    const selectEditableText = useCallback(() => {
        if (!editableRef.current) {
            return;
        }
        editableRef.current.focus();
        const range = document.createRange();
        const selection = window.getSelection();
        if (!selection) {
            return;
        }
        range.selectNodeContents(editableRef.current);
        selection.removeAllRanges();
        selection.addRange(range);
    }, []);

    const startRename = useCallback(() => {
        if (onRename == null || isReordering) {
            return;
        }
        if (editableTimeoutRef.current) {
            clearTimeout(editableTimeoutRef.current);
        }
        setIsEditable(true);
        editableTimeoutRef.current = setTimeout(() => {
            selectEditableText();
        }, RenameFocusDelayMs);
    }, [isReordering, onRename, selectEditableText]);

    if (renameRef != null) {
        renameRef.current = startRename;
    }

    const handleBlur = () => {
        if (!editableRef.current) {
            return;
        }
        const newText = editableRef.current.textContent?.trim() || originalName;
        editableRef.current.textContent = newText;
        setIsEditable(false);
        if (newText !== originalName) {
            onRename?.(newText);
        }
        setTimeout(() => refocusNode(null), 10);
    };

    const handleKeyDown: React.KeyboardEventHandler<HTMLDivElement> = (event) => {
        if (!editableRef.current) {
            return;
        }
        if (event.key === "Enter") {
            event.preventDefault();
            event.stopPropagation();
            editableRef.current.blur();
            return;
        }
        if (event.key !== "Escape") {
            return;
        }
        editableRef.current.textContent = originalName;
        editableRef.current.blur();
        event.preventDefault();
        event.stopPropagation();
    };

    return (
        <div
            draggable
            data-tabid={tab.id}
            onClick={onSelect}
            onDoubleClick={(event) => {
                event.stopPropagation();
                startRename();
            }}
            onContextMenu={onContextMenu}
            onDragStart={onDragStart}
            onDragOver={onDragOver}
            onDrop={onDrop}
            onDragEnd={onDragEnd}
            onMouseEnter={() => onHoverChanged?.(true)}
            onMouseLeave={() => onHoverChanged?.(false)}
            className={cn(
                "group relative flex w-full shrink-0 cursor-pointer items-center pl-3 text-xs transition-colors select-none",
                "whitespace-nowrap",
                tab.subtitle ? "min-h-[44px]" : "h-9",
                active ? "text-primary" : isReordering ? "text-secondary" : "text-secondary hover:text-primary",
                isDragging && "opacity-50"
            )}
        >
            {active && (
                <div
                    className={cn(
                        "pointer-events-none absolute inset-x-1 inset-y-[4px] rounded-[8px]",
                        "border border-white/25",
                        "bg-white/[0.06]"
                    )}
                />
            )}
            {!active && !isReordering && (
                <div className="pointer-events-none absolute inset-x-1 inset-y-[4px] rounded-[8px] bg-transparent transition-colors group-hover:bg-white/[0.05]" />
            )}
            <div
                className={cn(
                    "pointer-events-none absolute bottom-0 left-[5%] right-[5%] h-px bg-border/70",
                    !showDivider && "opacity-0"
                )}
            />
            <TabBadges
                badges={badges}
                flagColor={flagColor}
                className="mr-1 min-w-[16px] shrink-0 static top-auto left-auto z-auto h-[16px] w-auto translate-y-0 justify-start px-[2px] py-[1px] [&_i]:text-[10px]"
            />
            <div className={cn("min-w-0 flex-1 flex flex-col justify-center pr-3 gap-[2px]",
                onClose && !isReordering && "group-hover:pr-6")}>
                <div
                    ref={editableRef}
                    className={cn(
                        "overflow-hidden text-ellipsis whitespace-nowrap",
                        isEditable && "rounded-[2px] bg-white/15 outline-none px-[3px]"
                    )}
                    contentEditable={isEditable}
                    role="textbox"
                    aria-label="Tab name"
                    aria-readonly={!isEditable}
                    onBlur={handleBlur}
                    onKeyDown={handleKeyDown}
                    suppressContentEditableWarning={true}
                >
                    {tab.name}
                </div>
                {tab.subtitle && (
                    <div className="flex items-center gap-[6px] text-[10px] text-secondary/80 overflow-hidden whitespace-nowrap">
                        <span className="overflow-hidden text-ellipsis">{tab.subtitle}</span>
                        {tab.gitBranch && (
                            <span className="inline-flex items-center gap-[3px] shrink-0 text-[#b8f2c0]">
                                <i className="fa-solid fa-code-branch text-[9px] opacity-80" aria-hidden />
                                {tab.gitBranch}
                            </span>
                        )}
                        {tab.gitChangedFiles != null && tab.gitChangedFiles > 0 && (
                            <span className="shrink-0">
                                {tab.gitAdds != null && tab.gitAdds > 0 && (
                                    <span className="text-[#4caf50]">+{tab.gitAdds}</span>
                                )}
                                {tab.gitDels != null && tab.gitDels > 0 && (
                                    <span className="text-[#e57373] ml-[3px]">-{tab.gitDels}</span>
                                )}
                            </span>
                        )}
                    </div>
                )}
            </div>
            {onClose && (
                <button
                    type="button"
                    className={cn(
                        "absolute top-1/2 right-0 shrink-0 -translate-y-1/2 cursor-pointer py-1 pl-1 pr-3 text-secondary transition",
                        isReordering ? "opacity-0" : "opacity-0 group-hover:opacity-100 hover:text-primary"
                    )}
                    onClick={(event) => {
                        event.stopPropagation();
                        onClose();
                    }}
                    aria-label="Close tab"
                >
                    <i className="fa fa-solid fa-xmark" />
                </button>
            )}
        </div>
    );
}
