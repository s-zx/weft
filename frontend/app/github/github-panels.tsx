// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { getApi } from "@/store/global";
import { cn, fireAndForget } from "@/util/util";
import { useAtomValue } from "jotai";
import { memo, useEffect, useRef, useState } from "react";
import {
    fetchNotifications,
    fetchPRsToReview,
    GitHubNotification,
    PullRequest,
    subjectHtmlUrl,
} from "./github-api";
import { GitHubModel } from "./github-model";

// ---- shared panel shell ----

type PanelShellProps = {
    title: string;
    onClose: () => void;
    loading?: boolean;
    error?: string | null;
    headerRight?: React.ReactNode;
    children: React.ReactNode;
};

export const PanelShell = memo(
    ({ title, onClose, loading, error, headerRight, children }: PanelShellProps) => (
        <div className="flex flex-col w-[340px] max-h-[500px] bg-[#1e1e2e] border border-white/10 rounded-lg shadow-2xl overflow-hidden">
            <div className="flex items-center justify-between px-3 py-2 border-b border-white/8 shrink-0">
                <span className="text-[13px] font-semibold text-primary">{title}</span>
                <div className="flex items-center gap-2">
                    {headerRight}
                    <button
                        type="button"
                        onClick={onClose}
                        className="text-secondary hover:text-primary cursor-pointer transition-colors text-[12px]"
                    >
                        <i className="fa fa-solid fa-xmark" />
                    </button>
                </div>
            </div>
            {loading && (
                <div className="flex items-center justify-center py-6 text-secondary text-[12px]">
                    <i className="fa fa-solid fa-spinner fa-spin mr-2" />
                    Loading…
                </div>
            )}
            {!loading && error && (
                <div className="px-3 py-4 text-[12px] text-error">{error}</div>
            )}
            {!loading && !error && (
                <div className="flex-1 overflow-y-auto">{children}</div>
            )}
        </div>
    )
);
PanelShell.displayName = "PanelShell";

// ---- User Panel ----

export const UserPanel = memo(() => {
    const model = GitHubModel.getInstance();
    // Lazy-initialize GitHub API calls the first time user opens this panel.
    useEffect(() => { model.ensureInitialized(); }, []);
    const token = useAtomValue(model.tokenAtom);
    const user = useAtomValue(model.userAtom);
    const loading = useAtomValue(model.loadingAtom);
    const error = useAtomValue(model.errorAtom);
    const [tokenInput, setTokenInput] = useState("");
    const [saving, setSaving] = useState(false);

    const onSave = async () => {
        if (!tokenInput.trim()) return;
        setSaving(true);
        await model.saveToken(tokenInput.trim());
        setSaving(false);
        setTokenInput("");
    };

    return (
        <PanelShell
            title="GitHub Account"
            onClose={() => model.closePanel()}
            loading={loading && !user}
            error={!token ? null : error}
        >
            {!token ? (
                <div className="px-3 py-4 flex flex-col gap-3">
                    <p className="text-[12px] text-secondary leading-relaxed">
                        Paste a GitHub Personal Access Token to enable notifications and code review.
                        Needs scopes: <code className="text-accent">repo, notifications</code>.
                    </p>
                    <a
                        className="text-[12px] text-accent hover:underline cursor-pointer"
                        onClick={() => getApi().openExternal("https://github.com/settings/tokens/new")}
                    >
                        Generate token on GitHub →
                    </a>
                    <div className="flex gap-2 mt-1">
                        <input
                            type="password"
                            value={tokenInput}
                            onChange={(e) => setTokenInput(e.target.value)}
                            placeholder="ghp_xxxxxxxxxxxx"
                            className="flex-1 bg-white/5 border border-white/10 rounded px-2 py-1.5 text-[12px] text-primary outline-none focus:border-accent/60 placeholder:text-secondary/40"
                            onKeyDown={(e) => e.key === "Enter" && !saving && onSave()}
                        />
                        <button
                            type="button"
                            onClick={onSave}
                            disabled={saving || !tokenInput.trim()}
                            className={cn(
                                "px-3 py-1.5 rounded text-[12px] font-medium transition-colors cursor-pointer",
                                saving || !tokenInput.trim()
                                    ? "bg-white/5 text-secondary cursor-not-allowed"
                                    : "bg-accent/80 text-white hover:bg-accent"
                            )}
                        >
                            {saving ? "…" : "Save"}
                        </button>
                    </div>
                </div>
            ) : user ? (
                <div className="px-3 py-4 flex flex-col gap-3">
                    <div className="flex items-center gap-3">
                        <img
                            src={user.avatar_url}
                            alt={user.login}
                            className="w-12 h-12 rounded-full border border-white/10"
                        />
                        <div>
                            <div className="text-[13px] font-semibold text-primary">{user.name || user.login}</div>
                            <div className="text-[12px] text-secondary">@{user.login}</div>
                        </div>
                    </div>
                    <div className="flex gap-4 text-[12px] text-secondary">
                        <span>{user.public_repos} repos</span>
                        <span>{user.followers} followers</span>
                    </div>
                    <div className="flex gap-2 pt-1">
                        <button
                            type="button"
                            onClick={() => getApi().openExternal(user.html_url)}
                            className="flex-1 py-1.5 rounded text-[12px] text-secondary border border-white/10 hover:border-white/20 hover:text-primary transition-colors cursor-pointer"
                        >
                            View Profile
                        </button>
                        <button
                            type="button"
                            onClick={() => model.logout()}
                            className="flex-1 py-1.5 rounded text-[12px] text-secondary border border-white/10 hover:border-red-400/40 hover:text-red-400 transition-colors cursor-pointer"
                        >
                            Log out
                        </button>
                    </div>
                </div>
            ) : (
                <div className="px-3 py-4 text-[12px] text-secondary">Loading user…</div>
            )}
        </PanelShell>
    );
});
UserPanel.displayName = "UserPanel";

// ---- Notifications Panel ----

const NotificationRow = memo(({ n, onRead }: { n: GitHubNotification; onRead: () => void }) => {
    const iconClass =
        n.subject.type === "PullRequest"
            ? "fa-code-pull-request text-[#a78bfa]"
            : n.subject.type === "Release"
            ? "fa-tag text-[#22c55e]"
            : "fa-circle-dot text-[#3b82f6]";

    return (
        <div
            className={cn(
                "flex items-start gap-2 px-3 py-2 hover:bg-white/5 cursor-pointer group border-b border-white/5",
                !n.unread && "opacity-50"
            )}
            onClick={() => {
                getApi().openExternal(subjectHtmlUrl(n));
                if (n.unread) onRead();
            }}
        >
            <i className={`fa fa-solid ${iconClass} mt-0.5 text-[11px] shrink-0`} />
            <div className="flex-1 min-w-0">
                <div className="text-[12px] text-primary truncate">{n.subject.title}</div>
                <div className="text-[11px] text-secondary truncate">{n.repository.full_name}</div>
            </div>
            {n.unread && (
                <button
                    type="button"
                    onClick={(e) => {
                        e.stopPropagation();
                        onRead();
                    }}
                    className="text-[10px] text-secondary/50 hover:text-secondary opacity-0 group-hover:opacity-100 transition-opacity cursor-pointer"
                    title="Mark as read"
                >
                    <i className="fa fa-check" />
                </button>
            )}
        </div>
    );
});
NotificationRow.displayName = "NotificationRow";

export const NotificationsPanel = memo(() => {
    const model = GitHubModel.getInstance();
    const token = useAtomValue(model.tokenAtom);
    const notifications = useAtomValue(model.notificationsAtom);
    const loading = useAtomValue(model.loadingAtom);
    const error = useAtomValue(model.errorAtom);
    const unreadCount = useAtomValue(model.unreadCountAtom);

    if (!token) {
        return (
            <PanelShell title="Notifications" onClose={() => model.closePanel()}>
                <div className="px-3 py-4 text-[12px] text-secondary">Sign in with GitHub to see notifications.</div>
            </PanelShell>
        );
    }

    return (
        <PanelShell
            title={`Notifications${unreadCount > 0 ? ` (${unreadCount})` : ""}`}
            onClose={() => model.closePanel()}
            loading={loading && notifications.length === 0}
            error={error}
            headerRight={
                unreadCount > 0 ? (
                    <button
                        type="button"
                        onClick={() => fireAndForget(() => model.markAllRead())}
                        className="text-[11px] text-secondary hover:text-primary cursor-pointer transition-colors"
                        title="Mark all as read"
                    >
                        Mark all read
                    </button>
                ) : null
            }
        >
            {notifications.length === 0 ? (
                <div className="px-3 py-6 text-[12px] text-secondary text-center">
                    <i className="fa fa-check-circle text-[24px] text-accent/40 block mb-2" />
                    All caught up!
                </div>
            ) : (
                notifications.map((n) => (
                    <NotificationRow key={n.id} n={n} onRead={() => fireAndForget(() => model.markRead(n.id))} />
                ))
            )}
        </PanelShell>
    );
});
NotificationsPanel.displayName = "NotificationsPanel";

// ---- Code Review Panel ----

const PRRow = memo(({ pr }: { pr: PullRequest }) => (
    <div
        className="flex items-start gap-2 px-3 py-2 hover:bg-white/5 cursor-pointer border-b border-white/5"
        onClick={() => getApi().openExternal(pr.html_url)}
    >
        <img src={pr.user.avatar_url} alt={pr.user.login} className="w-6 h-6 rounded-full shrink-0 mt-0.5 border border-white/10" />
        <div className="flex-1 min-w-0">
            <div className="flex items-center gap-1.5">
                {pr.draft && (
                    <span className="text-[9px] px-1 py-0.5 rounded bg-white/10 text-secondary uppercase font-semibold">
                        Draft
                    </span>
                )}
                <span className="text-[12px] text-primary truncate">{pr.title}</span>
            </div>
            <div className="text-[11px] text-secondary truncate">
                {pr.base.repo.full_name} #{pr.number} · {pr.user.login}
            </div>
        </div>
        <i className="fa fa-solid fa-arrow-up-right-from-square text-[10px] text-secondary/50 mt-1 shrink-0" />
    </div>
));
PRRow.displayName = "PRRow";

export const CodeReviewPanel = memo(() => {
    const model = GitHubModel.getInstance();
    const token = useAtomValue(model.tokenAtom);
    const prs = useAtomValue(model.prsAtom);
    const loading = useAtomValue(model.loadingAtom);
    const error = useAtomValue(model.errorAtom);

    if (!token) {
        return (
            <PanelShell title="Code Review" onClose={() => model.closePanel()}>
                <div className="px-3 py-4 text-[12px] text-secondary">Sign in with GitHub to see review requests.</div>
            </PanelShell>
        );
    }

    return (
        <PanelShell
            title={`Review Requests${prs.length > 0 ? ` (${prs.length})` : ""}`}
            onClose={() => model.closePanel()}
            loading={loading && prs.length === 0}
            error={error}
            headerRight={
                <button
                    type="button"
                    onClick={() => fireAndForget(() => model.refresh())}
                    className="text-[11px] text-secondary hover:text-primary cursor-pointer transition-colors"
                    title="Refresh"
                >
                    <i className="fa fa-rotate-right" />
                </button>
            }
        >
            {prs.length === 0 ? (
                <div className="px-3 py-6 text-[12px] text-secondary text-center">
                    <i className="fa fa-code-pull-request text-[24px] text-accent/40 block mb-2" />
                    No review requests
                </div>
            ) : (
                prs.map((pr) => <PRRow key={pr.id} pr={pr} />)
            )}
        </PanelShell>
    );
});
CodeReviewPanel.displayName = "CodeReviewPanel";
