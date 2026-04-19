// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { waveEventSubscribeSingle } from "@/app/store/wps";
import * as WOS from "@/app/store/wos";
import { atoms, refocusNode } from "@/store/global";
import { getLayoutModelForStaticTab } from "@/layout/lib/layoutModelHooks";
import * as jotai from "jotai";

export type AppNotification = {
    id: string;
    blockId: string;
    blockCwd?: string;
    exitCode: number;
    ts: number;
    read: boolean;
};

const MAX_NOTIFICATIONS = 50;

export class NotificationsModel {
    private static instance: NotificationsModel | null = null;
    private unsubscribe: (() => void) | null = null;

    notificationsAtom: jotai.PrimitiveAtom<AppNotification[]>;
    unreadCountAtom: jotai.Atom<number>;

    private constructor() {
        this.notificationsAtom = jotai.atom([]) as jotai.PrimitiveAtom<AppNotification[]>;
        this.unreadCountAtom = jotai.atom((get) => get(this.notificationsAtom).filter((n) => !n.read).length);
        // WPS subscription deferred — only start listening when user first opens
        // the Notifications panel.  Calling subscribe() at construction added a
        // global stream for every active block, costing unnecessary round-trips.
    }

    // Called lazily when Notifications panel first opens.
    ensureSubscribed(): void {
        if (this.unsubscribe) return;
        this.subscribe();
    }

    static getInstance(): NotificationsModel {
        if (!NotificationsModel.instance) {
            NotificationsModel.instance = new NotificationsModel();
        }
        return NotificationsModel.instance;
    }

    private subscribe(): void {
        this.unsubscribe = waveEventSubscribeSingle({
            eventType: "block:jobstatus",
            handler: (event) => {
                const data = event.data;
                if (!data || data.status !== "done") return;
                if (data.cmdexitcode == null) return;
                // Get the block's cwd from object store
                const blockOref = WOS.makeORef("block", data.blockid);
                const blockAtom = WOS.getWaveObjectAtom<Block>(blockOref);
                const block = globalStore.get(blockAtom);
                const cwd = block?.meta?.["cmd:cwd"] as string | undefined;

                const note: AppNotification = {
                    id: `${data.blockid}:${Date.now()}`,
                    blockId: data.blockid,
                    blockCwd: cwd,
                    exitCode: data.cmdexitcode,
                    ts: Date.now(),
                    read: false,
                };

                const current = globalStore.get(this.notificationsAtom);
                const next = [note, ...current].slice(0, MAX_NOTIFICATIONS);
                globalStore.set(this.notificationsAtom, next);
            },
        });
    }

    markRead(id: string): void {
        globalStore.set(
            this.notificationsAtom,
            globalStore.get(this.notificationsAtom).map((n) => (n.id === id ? { ...n, read: true } : n))
        );
    }

    markAllRead(): void {
        globalStore.set(
            this.notificationsAtom,
            globalStore.get(this.notificationsAtom).map((n) => ({ ...n, read: true }))
        );
    }

    clearAll(): void {
        globalStore.set(this.notificationsAtom, []);
    }

    focusBlock(blockId: string): void {
        const layoutModel = getLayoutModelForStaticTab();
        const node = layoutModel?.getNodeByBlockId(blockId);
        if (node?.id) {
            layoutModel.focusNode(node.id);
            refocusNode(blockId);
        }
    }
}
