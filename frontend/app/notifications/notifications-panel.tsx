// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { getApi } from "@/store/global";
import { cn, fireAndForget } from "@/util/util";
import { useAtomValue } from "jotai";
import { memo, useEffect } from "react";
import { AppNotification, NotificationsModel } from "./notifications-model";

const AUTO_MARK_READ_DELAY_MS = 1500;

function timeAgo(ts: number): string {
    const secs = Math.floor((Date.now() - ts) / 1000);
    if (secs < 60) return `${secs}s ago`;
    const mins = Math.floor(secs / 60);
    if (mins < 60) return `${mins}m ago`;
    const hrs = Math.floor(mins / 60);
    return `${hrs}h ago`;
}

function tildeify(path: string): string {
    const home = getApi()?.getHomeDir?.();
    if (home && path.startsWith(home)) return "~" + path.slice(home.length);
    return path;
}

const NotificationRow = memo(({ n }: { n: AppNotification }) => {
    const model = NotificationsModel.getInstance();
    const success = n.exitCode === 0;
    const cwd = n.blockCwd ? tildeify(n.blockCwd) : null;

    return (
        <div
            className={cn(
                "flex items-start gap-2 px-3 py-2 border-b border-white/5 cursor-pointer group",
                n.read ? "opacity-50" : "",
                "hover:bg-white/5"
            )}
            onClick={() => {
                model.markRead(n.id);
                model.focusBlock(n.blockId);
            }}
        >
            <i
                className={cn(
                    "fa fa-solid mt-0.5 text-[12px] shrink-0",
                    success ? "fa-circle-check text-green-400" : "fa-circle-xmark text-red-400"
                )}
            />
            <div className="flex-1 min-w-0">
                <div className="text-[12px] text-primary">
                    Terminal {success ? "finished" : `exited (code ${n.exitCode})`}
                </div>
                {cwd && <div className="text-[11px] text-secondary truncate">{cwd}</div>}
            </div>
            <span className="text-[10px] text-secondary/60 shrink-0 pt-0.5">{timeAgo(n.ts)}</span>
        </div>
    );
});
NotificationRow.displayName = "NotificationRow";

export const NotificationsPanel = memo(() => {
    const model = NotificationsModel.getInstance();
    const notifications = useAtomValue(model.notificationsAtom);
    const unread = useAtomValue(model.unreadCountAtom);

    useEffect(() => {
        model.ensureSubscribed();
    }, []);

    useEffect(() => {
        if (unread <= 0) return;
        const t = setTimeout(() => model.markAllRead(), AUTO_MARK_READ_DELAY_MS);
        return () => clearTimeout(t);
    }, [unread]);

    return (
        <div className="flex flex-col w-[320px] max-h-[440px] bg-panel border border-white/10 rounded-lg shadow-2xl overflow-hidden">
            <div className="flex items-center justify-between px-3 py-2 border-b border-white/8 shrink-0">
                <span className="text-[13px] font-semibold text-primary">
                    Notifications{unread > 0 ? ` (${unread})` : ""}
                </span>
                {notifications.length > 0 && (
                    <button
                        type="button"
                        onClick={() => model.clearAll()}
                        className="text-[11px] text-secondary hover:text-primary cursor-pointer transition-colors"
                    >
                        Clear all
                    </button>
                )}
            </div>
            <div className="flex-1 overflow-y-auto">
                {notifications.length === 0 ? (
                    <div className="flex flex-col items-center justify-center py-8 gap-2 text-secondary text-[12px]">
                        <i className="fa fa-solid fa-bell text-[24px] opacity-30" />
                        <span>No notifications yet</span>
                        <span className="text-[11px] opacity-60">Terminal completions will appear here</span>
                    </div>
                ) : (
                    notifications.map((n) => <NotificationRow key={n.id} n={n} />)
                )}
            </div>
        </div>
    );
});
NotificationsPanel.displayName = "NotificationsPanel";
