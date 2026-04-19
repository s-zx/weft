// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { getLayoutModelForStaticTab } from "@/layout/lib/layoutModelHooks";
import { atoms, getApi, getBlockMetaKeyAtom } from "@/store/global";
import * as jotai from "jotai";

// Snapshot the user's home dir once; getApi().getHomeDir() is synchronous IPC
// that returns the same value for the life of the process (same pattern used in
// vtabbar.tsx:172 and termblocks.tsx:68).
const CachedHome: string = getApi().getHomeDir() ?? "~";

export function getCachedHome(): string {
    return CachedHome;
}

// Tracks tabId + blockId + cwd together so the file explorer can distinguish
// three events with different desired behaviours:
//
//   tabId changed          → user switched tabs     → update explorer root
//   tabId same, blockId changed → user clicked a different block in same tab
//                                                   → keep explorer root
//   tabId same, blockId same, cwd changed → user ran `cd` → update explorer root
export type FocusedBlockCwd = {
    tabId: string | null;
    blockId: string | null;
    cwd: string | null;
};

export const focusedBlockCwdAtom: jotai.Atom<FocusedBlockCwd> = jotai.atom((get) => {
    const tabId = get(atoms.staticTabId);  // subscribe to tab switches
    const layout = getLayoutModelForStaticTab();
    if (layout == null) return { tabId, blockId: null, cwd: null };
    const node = get(layout.focusedNode);
    const blockId = node?.data?.blockId ?? null;
    if (blockId == null) return { tabId, blockId: null, cwd: null };
    const cwd = get(getBlockMetaKeyAtom(blockId, "cmd:cwd"));
    return { tabId, blockId, cwd: cwd ?? null };
});

// Kept for backward-compat with GitModel.syncCwd() which still reads this.
export const focusedCwdAtom: jotai.Atom<string | null> = jotai.atom((get) => {
    const { cwd } = get(focusedBlockCwdAtom);
    return cwd;
});
