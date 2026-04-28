// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { TabCmdStateStore } from "@/app/store/tabcmdstate";
import { waveEventSubscribeSingle } from "@/app/store/wps";
import * as WOS from "@/app/store/wos";
import { atoms, getApi, refocusNode } from "@/store/global";
import * as jotai from "jotai";
import { ToastModel } from "./toast-model";

export type AppNotification = {
    id: string;
    blockId: string;
    tabId?: string;
    title?: string;
    body: string;
    ts: number;
    read: boolean;
};

const MAX_NOTIFICATIONS = 50;
const CliAgentTitle = "warp://cli-agent";

type CliAgentPayload = {
    agent?: string;
    event?: string;
    title?: string;
    body?: string;
    message?: string;
    summary?: string;
    error?: string;
    requires_user_action?: boolean;
    needs_user_action?: boolean;
    awaiting_input?: boolean;
    approval_required?: boolean;
};

type NormalizedNotification = {
    title?: string;
    body: string;
};

function parseCliAgentPayload(title: string | undefined, body: string): CliAgentPayload | null {
    if (!title?.startsWith(CliAgentTitle)) {
        return null;
    }
    try {
        const parsed = JSON.parse(body);
        if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
            return null;
        }
        return parsed as CliAgentPayload;
    } catch {
        return null;
    }
}

function formatCliAgentName(agent: string | undefined): string {
    const normalized = agent?.trim().toLowerCase();
    if (!normalized) {
        return "CLI Agent";
    }
    if (normalized === "claude") {
        return "Claude Code";
    }
    return normalized
        .split(/[\s_-]+/)
        .filter(Boolean)
        .map((part) => part[0].toUpperCase() + part.slice(1))
        .join(" ");
}

function isCliAgentUserActionEvent(eventName: string): boolean {
    return /(attention|input|approval|confirm|permission|auth|login|question|prompt|choose|required|review)/.test(
        eventName
    );
}

function isCliAgentCompletionEvent(eventName: string): boolean {
    return /(complete|completed|finish|finished|end|ended|done|stop|stopped|cancel|cancelled|abort|aborted|exit|exited|error|failed|blocked)/.test(
        eventName
    );
}

// Warp/Claude-style CLI agents can emit a stream of lifecycle notifications
// (session_start, turn_start, etc.). Those are noisy in Crest; only surface
// completion or explicit user-attention events.
export function normalizeCmdBlockNotification(title: string | undefined, body: string): NormalizedNotification | null {
    const payload = parseCliAgentPayload(title, body);
    if (payload == null) {
        return { title: title || undefined, body };
    }

    const eventName = payload.event?.trim().toLowerCase() ?? "";
    const needsUserAction =
        payload.requires_user_action === true ||
        payload.needs_user_action === true ||
        payload.awaiting_input === true ||
        payload.approval_required === true ||
        (eventName !== "" && isCliAgentUserActionEvent(eventName));
    const isCompletion = eventName !== "" && isCliAgentCompletionEvent(eventName);
    if (!needsUserAction && !isCompletion && eventName !== "") {
        return null;
    }

    const agentName = formatCliAgentName(payload.agent);
    const detail =
        payload.message?.trim() ||
        payload.summary?.trim() ||
        payload.body?.trim() ||
        payload.error?.trim() ||
        "";

    if (detail !== "") {
        return { title: agentName, body: detail };
    }
    if (needsUserAction) {
        return { title: agentName, body: `${agentName} needs your attention` };
    }
    if (isCompletion) {
        return { title: agentName, body: `${agentName} task finished` };
    }
    if (eventName !== "") {
        return { title: agentName, body: `${agentName}: ${eventName.replace(/[_-]+/g, " ")}` };
    }
    return { title: agentName, body };
}

export class NotificationsModel {
    private static instance: NotificationsModel | null = null;
    private unsubscribe: (() => void) | null = null;

    notificationsAtom: jotai.PrimitiveAtom<AppNotification[]>;
    unreadCountAtom: jotai.Atom<number>;

    private constructor() {
        this.notificationsAtom = jotai.atom([]) as jotai.PrimitiveAtom<AppNotification[]>;
        this.unreadCountAtom = jotai.atom((get) => get(this.notificationsAtom).filter((n) => !n.read).length);
    }

    // Called lazily when Notifications panel first opens, and also ensured at
    // app startup (so completions that happen before the panel is first opened
    // still surface as toasts + populate the feed).
    ensureSubscribed(): void {
        TabCmdStateStore.getInstance().ensureSubscribed();
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
            eventType: "cmdblock:notify",
            handler: (event) => {
                const ev = event.data as CmdBlockNotifyEvent | undefined;
                if (!ev?.blockid || !ev.body) return;
                const normalized = normalizeCmdBlockNotification(ev.title, ev.body);
                if (normalized == null) return;

                // Skip when the user is already looking at this block's tab —
                // the agent output is visible inline; no need for an alert.
                const activeTabId = globalStore.get(atoms.staticTabId);
                const activeTabAtom = WOS.getWaveObjectAtom<Tab>(WOS.makeORef("tab", activeTabId));
                const activeTab = globalStore.get(activeTabAtom);
                if (activeTab?.blockids?.includes(ev.blockid)) return;

                // Best-effort: find which tab owns this block so clicking the
                // notification can switch tabs.
                let tabId: string | undefined;
                const ws = globalStore.get(atoms.workspace);
                for (const tid of ws?.tabids ?? []) {
                    const tabAtom = WOS.getWaveObjectAtom<Tab>(WOS.makeORef("tab", tid));
                    const tab = globalStore.get(tabAtom);
                    if (tab?.blockids?.includes(ev.blockid)) {
                        tabId = tid;
                        break;
                    }
                }

                const now = Date.now();
                const note: AppNotification = {
                    id: `${ev.blockid}:${now}:${Math.random().toString(36).slice(2, 7)}`,
                    blockId: ev.blockid,
                    tabId,
                    title: normalized.title,
                    body: normalized.body,
                    ts: now,
                    read: false,
                };

                const current = globalStore.get(this.notificationsAtom);
                const next = [note, ...current].slice(0, MAX_NOTIFICATIONS);
                globalStore.set(this.notificationsAtom, next);

                ToastModel.getInstance().push(note);
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

    focusBlock(blockId: string, tabId?: string): void {
        const activeTabId = globalStore.get(atoms.staticTabId);
        if (tabId && tabId !== activeTabId) {
            // Switch to the tab that owns this block, then focus it once the
            // staticTabId atom settles (main-process round-trip).
            getApi().setActiveTab(tabId);
            let tries = 20;
            const poll = () => {
                if (globalStore.get(atoms.staticTabId) === tabId) {
                    refocusNode(blockId);
                    return;
                }
                if (--tries > 0) setTimeout(poll, 50);
                else refocusNode(blockId);
            };
            setTimeout(poll, 50);
            return;
        }
        refocusNode(blockId);
    }
}
