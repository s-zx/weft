// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { isBuilderWindow } from "@/app/store/windowtype";
import * as WOS from "@/app/store/wos";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { getLayoutModelForStaticTab } from "@/layout/lib/layoutModelHooks";
import { atoms, getOrefMetaKeyAtom, getSettingsKeyAtom, refocusNode } from "@/store/global";
import * as jotai from "jotai";
import { debounce } from "lodash-es";
import { ImperativePanelGroupHandle, ImperativePanelHandle } from "react-resizable-panels";

const VTabBar_DefaultWidth = 220;
const VTabBar_MinWidth = 150;
const VTabBar_MaxWidth = 320;

const FileExplorer_DefaultWidth = 260;
const FileExplorer_MinWidth = 180;
const FileExplorer_MaxWidthRatio = 0.5;

function clampVTabWidth(w: number): number {
    return Math.max(VTabBar_MinWidth, Math.min(w, VTabBar_MaxWidth));
}

function clampFileExplorerWidth(w: number, windowWidth: number): number {
    const maxWidth = Math.floor(windowWidth * FileExplorer_MaxWidthRatio);
    if (FileExplorer_MinWidth > maxWidth) return FileExplorer_MinWidth;
    return Math.max(FileExplorer_MinWidth, Math.min(w, maxWidth));
}

class WorkspaceLayoutModel {
    private static instance: WorkspaceLayoutModel | null = null;

    vtabPanelRef: ImperativePanelHandle | null;
    fileExplorerPanelRef: ImperativePanelHandle | null;
    outerPanelGroupRef: ImperativePanelGroupHandle | null;
    panelContainerRef: HTMLDivElement | null;
    vtabPanelWrapperRef: HTMLDivElement | null;
    fileExplorerWrapperRef: HTMLDivElement | null;

    vtabVisibleAtom: jotai.PrimitiveAtom<boolean>;
    fileExplorerVisibleAtom: jotai.PrimitiveAtom<boolean>;
    codeReviewVisibleAtom: jotai.PrimitiveAtom<boolean>;
    codeReviewWideAtom: jotai.PrimitiveAtom<boolean>;
    // Kept for backward-compat with code that still imports the AI panel atom.
    // We no longer render the AI panel, so this stays permanently false.
    panelVisibleAtom: jotai.PrimitiveAtom<boolean>;

    private inResize: boolean;
    private vtabWidth: number;
    private vtabVisible: boolean;
    private fileExplorerVisible: boolean;
    private fileExplorerWidth: number | null;
    private transitionTimeoutRef: NodeJS.Timeout | null = null;
    private debouncedPersistVTabWidth: () => void;
    private debouncedPersistFileExplorerWidth: () => void;
    widgetsSidebarVisibleAtom: jotai.Atom<boolean>;

    private constructor() {
        this.vtabPanelRef = null;
        this.fileExplorerPanelRef = null;
        this.outerPanelGroupRef = null;
        this.panelContainerRef = null;
        this.vtabPanelWrapperRef = null;
        this.fileExplorerWrapperRef = null;
        this.inResize = false;
        this.vtabWidth = VTabBar_DefaultWidth;
        this.vtabVisible = false;
        this.fileExplorerVisible = true;
        this.fileExplorerWidth = null;
        this.vtabVisibleAtom = jotai.atom(false);
        this.fileExplorerVisibleAtom = jotai.atom(true);
        this.codeReviewVisibleAtom = jotai.atom(false);
        this.codeReviewWideAtom = jotai.atom(false);
        this.panelVisibleAtom = jotai.atom(false);
        this.widgetsSidebarVisibleAtom = jotai.atom(
            (get) =>
                get(getOrefMetaKeyAtom(WOS.makeORef("workspace", this.getWorkspaceId()), "layout:widgetsvisible")) ??
                true
        );
        this.initializeFromMeta();

        this.handleWindowResize = this.handleWindowResize.bind(this);
        this.handleOuterPanelLayout = this.handleOuterPanelLayout.bind(this);

        this.debouncedPersistVTabWidth = debounce(() => {
            if (!this.vtabVisible) return;
            const width = this.vtabPanelWrapperRef?.offsetWidth;
            if (width == null || width <= 0) return;
            try {
                RpcApi.SetMetaCommand(TabRpcClient, {
                    oref: WOS.makeORef("workspace", this.getWorkspaceId()),
                    meta: { "layout:vtabbarwidth": width },
                });
            } catch (e) {
                console.warn("Failed to persist vtabbar width:", e);
            }
        }, 300);

        this.debouncedPersistFileExplorerWidth = debounce(() => {
            if (!this.fileExplorerVisible) return;
            const width = this.fileExplorerWrapperRef?.offsetWidth;
            if (width == null || width <= 0) return;
            try {
                RpcApi.SetMetaCommand(TabRpcClient, {
                    oref: WOS.makeORef("workspace", this.getWorkspaceId()),
                    meta: { "layout:fileexplorerwidth": width },
                });
            } catch (e) {
                console.warn("Failed to persist file explorer width:", e);
            }
        }, 300);
    }

    static getInstance(): WorkspaceLayoutModel {
        if (!WorkspaceLayoutModel.instance) {
            WorkspaceLayoutModel.instance = new WorkspaceLayoutModel();
        }
        return WorkspaceLayoutModel.instance;
    }

    // ---- Meta / persistence helpers ----

    private getWorkspaceId(): string {
        return globalStore.get(atoms.workspace)?.oid ?? "";
    }

    private getVTabBarWidthAtom(): jotai.Atom<number> {
        return getOrefMetaKeyAtom(WOS.makeORef("workspace", this.getWorkspaceId()), "layout:vtabbarwidth");
    }

    private getFileExplorerVisibleAtom(): jotai.Atom<boolean> {
        return getOrefMetaKeyAtom(WOS.makeORef("workspace", this.getWorkspaceId()), "layout:fileexplorervisible");
    }

    private getFileExplorerWidthAtom(): jotai.Atom<number> {
        return getOrefMetaKeyAtom(WOS.makeORef("workspace", this.getWorkspaceId()), "layout:fileexplorerwidth");
    }

    private initializeFromMeta(): void {
        try {
            const savedVTabWidth = globalStore.get(this.getVTabBarWidthAtom());
            const savedFileExplorerVisible = globalStore.get(this.getFileExplorerVisibleAtom());
            const savedFileExplorerWidth = globalStore.get(this.getFileExplorerWidthAtom());
            if (savedVTabWidth != null && savedVTabWidth > 0) {
                this.vtabWidth = savedVTabWidth;
            }
            if (savedFileExplorerVisible != null) {
                this.fileExplorerVisible = savedFileExplorerVisible;
                globalStore.set(this.fileExplorerVisibleAtom, savedFileExplorerVisible);
            }
            if (savedFileExplorerWidth != null && savedFileExplorerWidth > 0) {
                this.fileExplorerWidth = savedFileExplorerWidth;
            }
            const tabBarPosition = globalStore.get(getSettingsKeyAtom("app:tabbar")) ?? "top";
            const showLeftTabBar = tabBarPosition === "left" && !isBuilderWindow();
            this.vtabVisible = showLeftTabBar;
            globalStore.set(this.vtabVisibleAtom, showLeftTabBar);
        } catch (e) {
            console.warn("Failed to initialize from tab meta:", e);
        }
    }

    // ---- Resolved widths ----

    private getResolvedVTabWidth(): number {
        return clampVTabWidth(this.vtabWidth);
    }

    private getResolvedFileExplorerWidth(windowWidth: number): number {
        let w = this.fileExplorerWidth;
        if (w == null) {
            w = FileExplorer_DefaultWidth;
            this.fileExplorerWidth = w;
        }
        return clampFileExplorerWidth(w, windowWidth);
    }

    // ---- Layout ----

    private computeLayout(windowWidth: number): number[] {
        const vtabW = this.vtabVisible ? this.getResolvedVTabWidth() : 0;
        const feW = this.fileExplorerVisible ? this.getResolvedFileExplorerWidth(windowWidth) : 0;
        const vtabPct = windowWidth > 0 ? (vtabW / windowWidth) * 100 : 0;
        const fePct = windowWidth > 0 ? (feW / windowWidth) * 100 : 0;
        const contentPct = Math.max(0, 100 - vtabPct - fePct);
        return [vtabPct, fePct, contentPct];
    }

    private commitLayouts(windowWidth: number): void {
        if (!this.outerPanelGroupRef) return;
        const layout = this.computeLayout(windowWidth);
        this.inResize = true;
        try {
            this.outerPanelGroupRef.setLayout(layout);
        } catch (e) {
            // ignore transient layout mismatch (HMR / panel count changes)
        }
        this.inResize = false;
    }

    handleOuterPanelLayout(sizes: number[]): void {
        if (this.inResize) return;
        if (sizes.length < 3) return;
        const windowWidth = window.innerWidth;
        const newVTabPx = (sizes[0] / 100) * windowWidth;
        const newFePx = (sizes[1] / 100) * windowWidth;

        if (this.vtabVisible) {
            const clamped = clampVTabWidth(newVTabPx);
            if (clamped !== this.vtabWidth) {
                this.vtabWidth = clamped;
                this.debouncedPersistVTabWidth();
            }
        }

        if (this.fileExplorerVisible) {
            const clamped = clampFileExplorerWidth(newFePx, windowWidth);
            if (clamped !== this.fileExplorerWidth) {
                this.fileExplorerWidth = clamped;
                this.debouncedPersistFileExplorerWidth();
            }
        }

        this.commitLayouts(windowWidth);
    }

    handleWindowResize(): void {
        this.commitLayouts(window.innerWidth);
    }

    syncVTabWidthFromMeta(): void {
        const savedVTabWidth = globalStore.get(this.getVTabBarWidthAtom());
        if (savedVTabWidth != null && savedVTabWidth > 0 && savedVTabWidth !== this.vtabWidth) {
            this.vtabWidth = savedVTabWidth;
            this.commitLayouts(window.innerWidth);
        }
    }

    registerRefs(
        outerPanelGroupRef: ImperativePanelGroupHandle,
        panelContainerRef: HTMLDivElement,
        vtabPanelRef?: ImperativePanelHandle,
        vtabPanelWrapperRef?: HTMLDivElement,
        showLeftTabBar?: boolean,
        fileExplorerPanelRef?: ImperativePanelHandle,
        fileExplorerWrapperRef?: HTMLDivElement
    ): void {
        this.outerPanelGroupRef = outerPanelGroupRef;
        this.panelContainerRef = panelContainerRef;
        this.vtabPanelRef = vtabPanelRef ?? null;
        this.vtabPanelWrapperRef = vtabPanelWrapperRef ?? null;
        this.fileExplorerPanelRef = fileExplorerPanelRef ?? null;
        this.fileExplorerWrapperRef = fileExplorerWrapperRef ?? null;
        if (showLeftTabBar != null) {
            this.vtabVisible = showLeftTabBar;
            globalStore.set(this.vtabVisibleAtom, showLeftTabBar);
        }
        this.syncPanelCollapse();
        this.commitLayouts(window.innerWidth);
    }

    private syncPanelCollapse(): void {
        if (this.vtabPanelRef) {
            if (this.vtabVisible) this.vtabPanelRef.expand();
            else this.vtabPanelRef.collapse();
        }
        if (this.fileExplorerPanelRef) {
            if (this.fileExplorerVisible) this.fileExplorerPanelRef.expand();
            else this.fileExplorerPanelRef.collapse();
        }
    }

    enableTransitions(duration: number): void {
        if (!this.panelContainerRef) return;
        const panels = this.panelContainerRef.querySelectorAll("[data-panel]");
        panels.forEach((panel: HTMLElement) => {
            panel.style.transition = "flex 0.2s ease-in-out";
        });
        if (this.transitionTimeoutRef) clearTimeout(this.transitionTimeoutRef);
        this.transitionTimeoutRef = setTimeout(() => {
            if (!this.panelContainerRef) return;
            const panels = this.panelContainerRef.querySelectorAll("[data-panel]");
            panels.forEach((panel: HTMLElement) => {
                panel.style.transition = "none";
            });
        }, duration);
    }

    // ---- Initial percentages + min sizes (used by workspace.tsx) ----

    getVTabInitialPercentage(windowWidth: number, showLeftTabBar: boolean): number {
        if (!showLeftTabBar || isBuilderWindow() || !this.vtabVisible) return 0;
        return (this.getResolvedVTabWidth() / windowWidth) * 100;
    }

    getFileExplorerInitialPercentage(windowWidth: number): number {
        if (!this.fileExplorerVisible) return 0;
        return (this.getResolvedFileExplorerWidth(windowWidth) / windowWidth) * 100;
    }

    getContentInitialPercentage(windowWidth: number, showLeftTabBar: boolean): number {
        return Math.max(
            0,
            100 -
                this.getVTabInitialPercentage(windowWidth, showLeftTabBar) -
                this.getFileExplorerInitialPercentage(windowWidth)
        );
    }

    getVTabMinPct(windowWidth: number): number {
        return windowWidth > 0 ? (VTabBar_MinWidth / windowWidth) * 100 : 0;
    }

    getFileExplorerMinPct(windowWidth: number): number {
        return windowWidth > 0 ? (FileExplorer_MinWidth / windowWidth) * 100 : 0;
    }

    // ---- Public getters ----

    getVTabVisible(): boolean {
        return this.vtabVisible;
    }

    getFileExplorerVisible(): boolean {
        return this.fileExplorerVisible;
    }

    // ---- Toggle / visibility ----

    setVTabVisible(visible: boolean): void {
        const changed = this.vtabVisible !== visible;
        if (changed) {
            this.vtabVisible = visible;
            globalStore.set(this.vtabVisibleAtom, visible);
            this.enableTransitions(200);
        }
        this.syncPanelCollapse();
        this.commitLayouts(window.innerWidth);
    }

    setFileExplorerVisible(visible: boolean): void {
        const changed = this.fileExplorerVisible !== visible;
        if (changed) {
            this.fileExplorerVisible = visible;
            globalStore.set(this.fileExplorerVisibleAtom, visible);
            try {
                RpcApi.SetMetaCommand(TabRpcClient, {
                    oref: WOS.makeORef("workspace", this.getWorkspaceId()),
                    meta: { "layout:fileexplorervisible": visible },
                });
            } catch (e) {
                console.warn("Failed to persist file explorer visibility:", e);
            }
            this.enableTransitions(200);
        }
        // Always re-sync panel collapse + layout so a stray out-of-sync state
        // from HMR or rapid clicks still converges on the desired state.
        this.syncPanelCollapse();
        this.commitLayouts(window.innerWidth);
    }

    setShowLeftTabBar(showLeftTabBar: boolean): void {
        if (this.vtabVisible === showLeftTabBar) return;
        this.vtabVisible = showLeftTabBar;
        globalStore.set(this.vtabVisibleAtom, showLeftTabBar);
        this.enableTransitions(200);
        this.syncPanelCollapse();
        this.commitLayouts(window.innerWidth);
    }

    setCodeReviewVisible(visible: boolean): void {
        globalStore.set(this.codeReviewVisibleAtom, visible);
    }

    getCodeReviewVisible(): boolean {
        return globalStore.get(this.codeReviewVisibleAtom);
    }

    // ---- AI panel stubs (UI removed; keep API for older callers) ----

    getAIPanelVisible(): boolean {
        return false;
    }

    getAIPanelWidth(): number {
        return 0;
    }

    setAIPanelVisible(_visible: boolean, _opts?: { nofocus?: boolean }): void {
        // Wave AI panel removed from UI. Kept as a no-op so lingering callers
        // (termmodel / blockframe / etc.) don't crash. Refocus the current
        // block so the caller's intent (regain focus) still works.
        const layoutModel = getLayoutModelForStaticTab();
        const focusedNode = globalStore.get(layoutModel.focusedNode);
        const blockId = focusedNode?.data?.blockId;
        if (blockId != null) {
            refocusNode(blockId);
        }
    }
}

export { WorkspaceLayoutModel };
