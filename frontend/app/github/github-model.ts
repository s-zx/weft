// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { getApi } from "@/store/global";
import * as jotai from "jotai";
import {
    fetchNotifications,
    fetchPRsToReview,
    fetchUser,
    GitHubNotification,
    GitHubUser,
    markAllNotificationsRead,
    markNotificationRead,
    PullRequest,
} from "./github-api";

export type GitHubPanel = "user" | "notifications" | "codereview" | null;

export class GitHubModel {
    private static instance: GitHubModel | null = null;

    tokenAtom: jotai.PrimitiveAtom<string | null>;
    userAtom: jotai.PrimitiveAtom<GitHubUser | null>;
    notificationsAtom: jotai.PrimitiveAtom<GitHubNotification[]>;
    prsAtom: jotai.PrimitiveAtom<PullRequest[]>;
    loadingAtom: jotai.PrimitiveAtom<boolean>;
    errorAtom: jotai.PrimitiveAtom<string | null>;
    activePanelAtom: jotai.PrimitiveAtom<GitHubPanel>;
    unreadCountAtom: jotai.Atom<number>;

    private constructor() {
        this.tokenAtom = jotai.atom(null) as jotai.PrimitiveAtom<string | null>;
        this.userAtom = jotai.atom(null) as jotai.PrimitiveAtom<GitHubUser | null>;
        this.notificationsAtom = jotai.atom([]) as jotai.PrimitiveAtom<GitHubNotification[]>;
        this.prsAtom = jotai.atom([]) as jotai.PrimitiveAtom<PullRequest[]>;
        this.loadingAtom = jotai.atom(false) as jotai.PrimitiveAtom<boolean>;
        this.errorAtom = jotai.atom(null) as jotai.PrimitiveAtom<string | null>;
        this.activePanelAtom = jotai.atom(null) as jotai.PrimitiveAtom<GitHubPanel>;
        this.unreadCountAtom = jotai.atom((get) => get(this.notificationsAtom).filter((n) => n.unread).length);
        // Do NOT call initialize() here — deferring GitHub API calls until user
        // actually opens a panel eliminates startup network overhead.
    }

    // Called lazily when user first opens a GitHub-backed panel.
    ensureInitialized(): void {
        if (globalStore.get(this.tokenAtom) != null) return;
        this.initialize();
    }

    static getInstance(): GitHubModel {
        if (!GitHubModel.instance) {
            GitHubModel.instance = new GitHubModel();
        }
        return GitHubModel.instance;
    }

    private initialize(): void {
        // Priority: localStorage → GITHUB_TOKEN env var
        const savedToken = localStorage.getItem("crest:github:token");
        const envToken = getApi().getEnv("GITHUB_TOKEN");
        const token = savedToken || envToken || null;
        if (token) {
            globalStore.set(this.tokenAtom, token);
            this.fetchAll(token);
        }
    }

    private getToken(): string | null {
        return globalStore.get(this.tokenAtom);
    }

    async saveToken(token: string): Promise<void> {
        const trimmed = token.trim();
        if (!trimmed) return;
        globalStore.set(this.tokenAtom, trimmed);
        globalStore.set(this.errorAtom, null);
        localStorage.setItem("crest:github:token", trimmed);
        await this.fetchAll(trimmed);
    }

    logout(): void {
        globalStore.set(this.tokenAtom, null);
        globalStore.set(this.userAtom, null);
        globalStore.set(this.notificationsAtom, []);
        globalStore.set(this.prsAtom, []);
        globalStore.set(this.errorAtom, null);
        globalStore.set(this.activePanelAtom, null);
        localStorage.removeItem("crest:github:token");
    }

    async fetchAll(token?: string): Promise<void> {
        const t = token ?? this.getToken();
        if (!t) return;
        globalStore.set(this.loadingAtom, true);
        globalStore.set(this.errorAtom, null);
        try {
            const [user, notifications] = await Promise.all([fetchUser(t), fetchNotifications(t)]);
            globalStore.set(this.userAtom, user);
            globalStore.set(this.notificationsAtom, notifications);
            // Fetch review-requested PRs using the authenticated user's login
            const prs = await fetchPRsToReview(t, user.login);
            globalStore.set(this.prsAtom, prs);
        } catch (e: any) {
            globalStore.set(this.errorAtom, e?.message ?? String(e));
        } finally {
            globalStore.set(this.loadingAtom, false);
        }
    }

    async refresh(): Promise<void> {
        await this.fetchAll();
    }

    async markRead(id: string): Promise<void> {
        const token = this.getToken();
        if (!token) return;
        try {
            await markNotificationRead(token, id);
            globalStore.set(
                this.notificationsAtom,
                globalStore.get(this.notificationsAtom).map((n) => (n.id === id ? { ...n, unread: false } : n))
            );
        } catch (e) {
            console.warn("Failed to mark notification as read:", e);
        }
    }

    async markAllRead(): Promise<void> {
        const token = this.getToken();
        if (!token) return;
        try {
            await markAllNotificationsRead(token);
            globalStore.set(
                this.notificationsAtom,
                globalStore.get(this.notificationsAtom).map((n) => ({ ...n, unread: false }))
            );
        } catch (e) {
            console.warn("Failed to mark all notifications as read:", e);
        }
    }

    togglePanel(panel: GitHubPanel): void {
        const current = globalStore.get(this.activePanelAtom);
        globalStore.set(this.activePanelAtom, current === panel ? null : panel);
    }

    closePanel(): void {
        globalStore.set(this.activePanelAtom, null);
    }
}
