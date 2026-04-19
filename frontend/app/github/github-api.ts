// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

const GITHUB_API = "https://api.github.com";

export type GitHubUser = {
    login: string;
    name: string;
    avatar_url: string;
    html_url: string;
    public_repos: number;
    followers: number;
};

export type GitHubNotification = {
    id: string;
    unread: boolean;
    reason: string;
    subject: {
        title: string;
        url: string;
        latest_comment_url: string;
        type: "Issue" | "PullRequest" | "Release" | "Commit" | string;
    };
    repository: {
        full_name: string;
        html_url: string;
    };
    updated_at: string;
};

export type PullRequest = {
    id: number;
    number: number;
    title: string;
    html_url: string;
    state: string;
    draft: boolean;
    user: { login: string; avatar_url: string };
    base: { repo: { full_name: string; html_url: string } };
    created_at: string;
    updated_at: string;
    requested_reviewers: { login: string }[];
};

async function ghFetch<T>(token: string, path: string, opts: RequestInit = {}): Promise<T> {
    const resp = await fetch(`${GITHUB_API}${path}`, {
        ...opts,
        headers: {
            Authorization: `Bearer ${token}`,
            Accept: "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
            ...(opts.headers ?? {}),
        },
    });
    if (!resp.ok) {
        const msg = await resp.text().catch(() => resp.statusText);
        throw new Error(`GitHub API ${path}: ${resp.status} ${msg}`);
    }
    return resp.json();
}

export async function fetchUser(token: string): Promise<GitHubUser> {
    return ghFetch<GitHubUser>(token, "/user");
}

export async function fetchNotifications(token: string): Promise<GitHubNotification[]> {
    return ghFetch<GitHubNotification[]>(token, "/notifications?per_page=30");
}

export async function fetchPRsToReview(token: string, login: string): Promise<PullRequest[]> {
    const data = await ghFetch<{ items: PullRequest[] }>(
        token,
        `/search/issues?q=is:open+is:pr+review-requested:${login}&per_page=20&sort=updated`
    );
    return data.items;
}

export async function markNotificationRead(token: string, id: string): Promise<void> {
    await fetch(`${GITHUB_API}/notifications/threads/${id}`, {
        method: "PATCH",
        headers: {
            Authorization: `Bearer ${token}`,
            Accept: "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    });
}

export async function markAllNotificationsRead(token: string): Promise<void> {
    await fetch(`${GITHUB_API}/notifications`, {
        method: "PUT",
        headers: {
            Authorization: `Bearer ${token}`,
            Accept: "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
        body: JSON.stringify({ last_read_at: new Date().toISOString() }),
    });
}

// Convert a GitHub API resource URL (like /repos/:owner/:repo/issues/:num) to HTML URL
export function subjectHtmlUrl(notification: GitHubNotification): string {
    const repoHtml = notification.repository.html_url;
    const type = notification.subject.type;
    const apiUrl = notification.subject.url;
    if (!apiUrl) return repoHtml;
    const match = apiUrl.match(/\/(\d+)$/);
    if (!match) return repoHtml;
    const num = match[1];
    if (type === "PullRequest") return `${repoHtml}/pull/${num}`;
    if (type === "Issue") return `${repoHtml}/issues/${num}`;
    return repoHtml;
}
