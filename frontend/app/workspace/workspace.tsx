// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { ErrorBoundary } from "@/app/element/errorboundary";
import { CenteredDiv } from "@/app/element/quickelems";
import { FileExplorer } from "@/app/fileexplorer/file-explorer";
import { ModalsRenderer } from "@/app/modals/modalsrenderer";
import { GitReviewSidebar } from "@/app/codereview/git-panel";
import { TabBar } from "@/app/tab/tabbar";
import { TabContent } from "@/app/tab/tabcontent";
import { VTabBar } from "@/app/tab/vtabbar";
import { TopBar } from "@/app/topbar/topbar";
import { Widgets } from "@/app/workspace/widgets";
import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import { atoms, getSettingsKeyAtom } from "@/store/global";
import { isMacOS } from "@/util/platformutil";
import { useAtomValue } from "jotai";
import { memo, useEffect, useRef } from "react";
import {
    ImperativePanelGroupHandle,
    ImperativePanelHandle,
    Panel,
    PanelGroup,
    PanelResizeHandle,
} from "react-resizable-panels";

const WorkspaceElem = memo(() => {
    const workspaceLayoutModel = WorkspaceLayoutModel.getInstance();
    const tabId = useAtomValue(atoms.staticTabId);
    const ws = useAtomValue(atoms.workspace);
    const tabBarPosition = useAtomValue(getSettingsKeyAtom("app:tabbar")) ?? "top";
    const showLeftTabBar = tabBarPosition === "left";
    const vtabVisible = useAtomValue(workspaceLayoutModel.vtabVisibleAtom);
    const fileExplorerVisible = useAtomValue(workspaceLayoutModel.fileExplorerVisibleAtom);
    const codeReviewVisible = useAtomValue(workspaceLayoutModel.codeReviewVisibleAtom);
    const codeReviewWide = useAtomValue(workspaceLayoutModel.codeReviewWideAtom);
    const widgetsSidebarVisible = useAtomValue(workspaceLayoutModel.widgetsSidebarVisibleAtom);
    const windowWidth = window.innerWidth;
    const vtabInitialPct = workspaceLayoutModel.getVTabInitialPercentage(windowWidth, showLeftTabBar);
    const vtabMinPct = workspaceLayoutModel.getVTabMinPct(windowWidth);
    const fileExplorerInitialPct = workspaceLayoutModel.getFileExplorerInitialPercentage(windowWidth);
    const fileExplorerMinPct = workspaceLayoutModel.getFileExplorerMinPct(windowWidth);
    const contentInitialPct = workspaceLayoutModel.getContentInitialPercentage(windowWidth, showLeftTabBar);

    const outerPanelGroupRef = useRef<ImperativePanelGroupHandle>(null);
    const vtabPanelRef = useRef<ImperativePanelHandle>(null);
    const fileExplorerPanelRef = useRef<ImperativePanelHandle>(null);
    const panelContainerRef = useRef<HTMLDivElement>(null);
    const vtabPanelWrapperRef = useRef<HTMLDivElement>(null);
    const fileExplorerWrapperRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (outerPanelGroupRef.current && panelContainerRef.current) {
            workspaceLayoutModel.registerRefs(
                outerPanelGroupRef.current,
                panelContainerRef.current,
                vtabPanelRef.current ?? undefined,
                vtabPanelWrapperRef.current ?? undefined,
                showLeftTabBar,
                fileExplorerPanelRef.current ?? undefined,
                fileExplorerWrapperRef.current ?? undefined
            );
        }
    }, []);

    useEffect(() => {
        window.addEventListener("resize", workspaceLayoutModel.handleWindowResize);
        return () => window.removeEventListener("resize", workspaceLayoutModel.handleWindowResize);
    }, []);

    useEffect(() => {
        workspaceLayoutModel.setShowLeftTabBar(showLeftTabBar);
    }, [showLeftTabBar]);

    useEffect(() => {
        const handleFocus = () => workspaceLayoutModel.syncVTabWidthFromMeta();
        window.addEventListener("focus", handleFocus);
        return () => window.removeEventListener("focus", handleFocus);
    }, []);

    const vtabHandleVisible = vtabVisible;
    const vtabHandleClass = `bg-transparent hover:bg-zinc-500/20 transition-colors ${vtabHandleVisible ? "w-0.5" : "w-0 pointer-events-none"}`;
    const feHandleVisible = fileExplorerVisible;
    const feHandleClass = `bg-transparent hover:bg-zinc-500/20 transition-colors ${feHandleVisible ? "w-0.5" : "w-0 pointer-events-none"}`;

    return (
        <div className="flex flex-col w-full flex-grow overflow-hidden">
            <TopBar />
            {!showLeftTabBar && <TabBar key={ws.oid} workspace={ws} noTabs={false} />}
            <div ref={panelContainerRef} className="flex flex-row flex-grow overflow-hidden">
                <ErrorBoundary key={tabId}>
                    <PanelGroup
                        direction="horizontal"
                        onLayout={workspaceLayoutModel.handleOuterPanelLayout}
                        ref={outerPanelGroupRef}
                    >
                        <Panel
                            ref={vtabPanelRef}
                            collapsible
                            defaultSize={vtabInitialPct}
                            minSize={vtabMinPct}
                            order={0}
                            className="overflow-hidden"
                        >
                            <div ref={vtabPanelWrapperRef} className="w-full h-full">
                                {showLeftTabBar && <VTabBar workspace={ws} />}
                            </div>
                        </Panel>
                        <PanelResizeHandle className={vtabHandleClass} />
                        <Panel
                            ref={fileExplorerPanelRef}
                            collapsible
                            defaultSize={fileExplorerInitialPct}
                            minSize={fileExplorerMinPct}
                            order={1}
                            className="overflow-hidden"
                        >
                            <div ref={fileExplorerWrapperRef} className="w-full h-full">
                                {tabId !== "" && <FileExplorer />}
                            </div>
                        </Panel>
                        <PanelResizeHandle className={feHandleClass} />
                        <Panel order={2} defaultSize={contentInitialPct}>
                            {tabId === "" ? (
                                <CenteredDiv>No Active Tab</CenteredDiv>
                            ) : (
                                <div className="relative flex flex-row h-full overflow-hidden">
                                    <TabContent key={tabId} tabId={tabId} noTopPadding={showLeftTabBar && isMacOS()} />
                                    {widgetsSidebarVisible && <Widgets />}
                                    {codeReviewVisible && (
                                        <div
                                            className="absolute top-0 right-0 h-full z-10 transition-[width] duration-200"
                                            style={{ width: codeReviewWide ? "100%" : 380 }}
                                        >
                                            <GitReviewSidebar />
                                        </div>
                                    )}
                                </div>
                            )}
                        </Panel>
                    </PanelGroup>
                    <ModalsRenderer />
                </ErrorBoundary>
            </div>
        </div>
    );
});

WorkspaceElem.displayName = "WorkspaceElem";

export { WorkspaceElem as Workspace };
