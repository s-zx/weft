// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { Tooltip } from "@/app/element/tooltip";
import { NotificationsPanel } from "@/app/notifications/notifications-panel";
import { NotificationsModel } from "@/app/notifications/notifications-model";
import { UserPanel } from "@/app/github/github-panels";
import { GitHubModel } from "@/app/github/github-model";
import { atoms } from "@/app/store/global";
import { WorkspaceSwitcher } from "@/app/tab/workspaceswitcher";
import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import { cn } from "@/util/util";
import { isMacOS, isMacOSTahoeOrLater } from "@/util/platformutil";
import { FloatingPortal, flip, offset, shift, useClick, useDismiss, useFloating, useInteractions } from "@floating-ui/react";
import { useAtomValue } from "jotai";
import { memo, useRef } from "react";

const MacOSTrafficLightsWidth = 74;
const MacOSTahoeTrafficLightsWidth = 80;

// ---- Generic icon button ----
type ToolbarButtonProps = {
    icon: string;
    label: string;
    active?: boolean;
    badgeCount?: number;
    onClick?: () => void;
};

const ToolbarButton = memo(({ icon, label, active, badgeCount, onClick }: ToolbarButtonProps) => (
    <Tooltip
        content={label}
        placement="bottom"
        hideOnClick
        divClassName={cn(
            "relative flex items-center justify-center h-7 w-7 rounded-md text-[13px] transition-colors cursor-pointer",
            active ? "text-accent bg-white/10" : "text-secondary hover:text-primary hover:bg-white/5"
        )}
        divStyle={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
        divOnClick={onClick}
    >
        <i className={`fa fa-solid ${icon}`} />
        {badgeCount != null && badgeCount > 0 && (
            <span className="absolute top-0.5 right-0.5 min-w-[14px] h-[14px] rounded-full bg-red-500 text-white text-[9px] font-bold flex items-center justify-center px-0.5 leading-none">
                {badgeCount > 99 ? "99+" : badgeCount}
            </span>
        )}
    </Tooltip>
));
ToolbarButton.displayName = "ToolbarButton";

// ---- Panel wrapper using floating-ui click popover ----
type PanelAnchorProps = {
    children: React.ReactNode; // button trigger
    panel: React.ReactNode;    // panel content
    isOpen: boolean;
    onOpenChange: (open: boolean) => void;
};

const PanelAnchor = memo(({ children, panel, isOpen, onOpenChange }: PanelAnchorProps) => {
    const { refs, floatingStyles, context } = useFloating({
        open: isOpen,
        onOpenChange,
        placement: "bottom-end",
        middleware: [offset(6), flip(), shift({ padding: 8 })],
    });
    const click = useClick(context);
    const dismiss = useDismiss(context);
    const { getReferenceProps, getFloatingProps } = useInteractions([click, dismiss]);

    return (
        <>
            <div
                ref={refs.setReference}
                style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
                {...getReferenceProps()}
            >
                {children}
            </div>
            {isOpen && (
                <FloatingPortal>
                    <div
                        ref={refs.setFloating}
                        style={{ ...floatingStyles, zIndex: 1000 }}
                        {...getFloatingProps()}
                    >
                        {panel}
                    </div>
                </FloatingPortal>
            )}
        </>
    );
});
PanelAnchor.displayName = "PanelAnchor";

// ---- Right panel buttons ----
const RightPanelButtons = memo(() => {
    const layoutModel = WorkspaceLayoutModel.getInstance();
    // GitHubModel / NotificationsModel instances are cheap to create (constructor
    // does NO network calls, NO WPS subscriptions). Heavy work is deferred to the
    // first time the user opens the panel.
    const ghModel = GitHubModel.getInstance();
    const notifModel = NotificationsModel.getInstance();
    const ghUser = useAtomValue(ghModel.userAtom);
    const ghActivePanel = useAtomValue(ghModel.activePanelAtom);
    const notifUnread = useAtomValue(notifModel.unreadCountAtom);
    const codeReviewVisible = useAtomValue(layoutModel.codeReviewVisibleAtom);

    return (
        <>
            {/* Code Review: toggles right sidebar */}
            <ToolbarButton
                icon="fa-code-branch"
                label="Code Review"
                active={codeReviewVisible}
                onClick={() => layoutModel.setCodeReviewVisible(!codeReviewVisible)}
            />

            {/* Notifications: floating popover */}
            <PanelAnchor
                isOpen={ghActivePanel === "notifications"}
                onOpenChange={(open) => ghModel.togglePanel(open ? "notifications" : null)}
                panel={<NotificationsPanel />}
            >
                <ToolbarButton
                    icon="fa-bell"
                    label="Notifications"
                    active={ghActivePanel === "notifications"}
                    badgeCount={notifUnread}
                />
            </PanelAnchor>

            <div className="w-px h-5 bg-white/10 mx-1" />

            {/* User: GitHub account popover */}
            <PanelAnchor
                isOpen={ghActivePanel === "user"}
                onOpenChange={(open) => ghModel.togglePanel(open ? "user" : null)}
                panel={<UserPanel />}
            >
                {ghUser ? (
                    <div
                        className={cn(
                            "flex items-center justify-center h-7 w-7 rounded-md cursor-pointer transition-colors",
                            ghActivePanel === "user" ? "bg-white/10 ring-1 ring-accent/60" : "hover:bg-white/5"
                        )}
                        style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
                    >
                        <img src={ghUser.avatar_url} alt={ghUser.login} className="w-5 h-5 rounded-full" />
                    </div>
                ) : (
                    <ToolbarButton
                        icon="fa-user"
                        label="GitHub Account"
                        active={ghActivePanel === "user"}
                    />
                )}
            </PanelAnchor>
        </>
    );
});
RightPanelButtons.displayName = "RightPanelButtons";

// ---- VTabBar toggle ----
const VTabToggleButton = memo(() => {
    const model = WorkspaceLayoutModel.getInstance();
    const visible = useAtomValue(model.vtabVisibleAtom);
    return (
        <ToolbarButton
            icon="fa-sidebar"
            label="Toggle Sidebar  ⌘B"
            active={visible}
            onClick={() => model.setVTabVisible(!model.getVTabVisible())}
        />
    );
});
VTabToggleButton.displayName = "VTabToggleButton";

// ---- FileExplorer toggle ----
const FileExplorerToggleButton = memo(() => {
    const model = WorkspaceLayoutModel.getInstance();
    const visible = useAtomValue(model.fileExplorerVisibleAtom);
    return (
        <ToolbarButton
            icon="fa-folder-tree"
            label="Toggle File Explorer"
            active={visible}
            onClick={() => model.setFileExplorerVisible(!model.getFileExplorerVisible())}
        />
    );
});
FileExplorerToggleButton.displayName = "FileExplorerToggleButton";

// ---- Search trigger ----
const SearchTrigger = memo(() => (
    <button
        type="button"
        className="flex items-center gap-2 h-7 w-full max-w-[400px] px-3 rounded-md bg-white/5 text-[12px] text-secondary hover:bg-white/10 hover:text-primary transition-colors cursor-pointer"
        style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
        onClick={() => console.log("command palette — coming soon")}
    >
        <i className="fa fa-solid fa-magnifying-glass text-[11px]" />
        <span className="flex-1 text-left">Search files, commands...</span>
        <span className="text-[10px] text-secondary/60 border border-white/10 rounded px-1 py-0.5 leading-none">⌘K</span>
    </button>
));
SearchTrigger.displayName = "SearchTrigger";

// ---- TopBar ----
export const TopBar = memo(() => {
    const isFullScreen = useAtomValue(atoms.isFullScreen);
    const workspaceSwitcherRef = useRef<HTMLDivElement>(null);
    const mac = isMacOS();
    const trafficLightsWidth = isMacOSTahoeOrLater() ? MacOSTahoeTrafficLightsWidth : MacOSTrafficLightsWidth;

    return (
        <div
            className="flex items-center h-9 shrink-0 w-full border-b border-white/5 select-none"
            style={{ WebkitAppRegion: "drag", backdropFilter: "blur(20px)", background: "rgba(0,0,0,0.35)" } as React.CSSProperties}
        >
            {mac && !isFullScreen && <div className="shrink-0" style={{ width: trafficLightsWidth }} />}

            {/* Left: sidebar + explorer toggles + workspace switcher */}
            <div
                className="flex items-center gap-1 shrink-0 pl-1"
                style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
            >
                <VTabToggleButton />
                <FileExplorerToggleButton />
                <div className="w-px h-5 bg-white/10 mx-1" />
                <Tooltip content="Workspace Switcher" placement="bottom" hideOnClick divRef={workspaceSwitcherRef} divClassName="flex items-center">
                    <WorkspaceSwitcher />
                </Tooltip>
            </div>

            {/* Middle: search */}
            <div className="flex-1 flex justify-center px-4">
                <SearchTrigger />
            </div>

            {/* Right: panels */}
            <div
                className="flex items-center gap-0.5 shrink-0 pr-2"
                style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
            >
                <RightPanelButtons />
            </div>
        </div>
    );
});
TopBar.displayName = "TopBar";
