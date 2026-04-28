// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { UseChatSendMessageType, UseChatSetMessagesType } from "@/store/aitypes";
import { BlockNodeModel } from "@/app/block/blocktypes";
import { appHandleKeyDown } from "@/app/store/keymodel";
import { modalsModel } from "@/app/store/modalmodel";
import type { TabModel } from "@/app/store/tab-model";
import { waveEventSubscribeSingle } from "@/app/store/wps";
import { RpcApi } from "@/app/store/wshclientapi";
import { makeFeBlockRouteId } from "@/app/store/wshrouter";
import { DefaultRouter, TabRpcClient } from "@/app/store/wshrpcutil";
import { TermClaudeIcon, TerminalView } from "@/app/view/term/term";
import { buildTermSettingsMenuItems } from "@/app/view/term/term-settings-menu";
import { TermWshClient } from "@/app/view/term/term-wsh";
import { VDomModel } from "@/app/view/vdom/vdom-model";
import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import {
    atoms,
    createBlock,
    createBlockSplitHorizontally,
    createBlockSplitVertically,
    getAllBlockComponentModels,
    getApi,
    getBlockComponentModel,
    getBlockMetaKeyAtom,
    getBlockTermDurableAtom,
    getConnStatusAtom,
    getOverrideConfigAtom,
    getSettingsKeyAtom,
    globalStore,
    readAtom,
    recordTEvent,
    useBlockAtom,
    WOS,
} from "@/store/global";
import * as services from "@/store/services";
import * as keyutil from "@/util/keyutil";
import { isMacOS, isWindows } from "@/util/platformutil";
import { boundNumber, fireAndForget, stringToBase64 } from "@/util/util";
import * as jotai from "jotai";
import * as React from "react";
import { getBlockingCommand } from "./shellblocking";
import { computeTheme, DefaultTermTheme, isLikelyOnSameHost, trimTerminalSelection } from "./termutil";
import { TermWrap, WebGLSupported } from "./termwrap";

export class TermViewModel implements ViewModel {
    viewType: string;
    nodeModel: BlockNodeModel;
    tabModel: TabModel;
    connected: boolean;
    termRef: React.RefObject<TermWrap> = { current: null };
    blockAtom: jotai.Atom<Block>;
    termMode: jotai.Atom<string>;
    blockId: string;
    viewIcon: jotai.Atom<IconButtonDecl>;
    viewName: jotai.Atom<string>;
    viewText: jotai.Atom<HeaderElem[]>;
    blockBg: jotai.Atom<MetaType>;
    manageConnection: jotai.Atom<boolean>;
    filterOutNowsh?: jotai.Atom<boolean>;
    connStatus: jotai.Atom<ConnStatus>;
    useTermHeader: jotai.Atom<boolean>;
    termWshClient: TermWshClient;
    vdomBlockId: jotai.Atom<string>;
    vdomToolbarBlockId: jotai.Atom<string>;
    vdomToolbarTarget: jotai.PrimitiveAtom<VDomTargetToolbar>;
    fontSizeAtom: jotai.Atom<number>;
    termThemeNameAtom: jotai.Atom<string>;
    termTransparencyAtom: jotai.Atom<number>;
    termBPMAtom: jotai.Atom<boolean>;
    noPadding: jotai.PrimitiveAtom<boolean>;
    endIconButtons: jotai.Atom<IconButtonDecl[]>;
    shellProcFullStatus: jotai.PrimitiveAtom<BlockControllerRuntimeStatus>;
    shellProcStatus: jotai.Atom<string>;
    shellProcStatusUnsubFn: () => void;
    blockJobStatusAtom: jotai.PrimitiveAtom<BlockJobStatusData>;
    blockJobStatusVersionTs: number;
    blockJobStatusUnsubFn: () => void;
    termBPMUnsubFn: () => void;
    termCursorUnsubFn: () => void;
    termCursorBlinkUnsubFn: () => void;
    isCmdController: jotai.Atom<boolean>;
    isRestarting: jotai.PrimitiveAtom<boolean>;
    termDurableStatus: jotai.Atom<BlockJobStatusData | null>;
    termConfigedDurable: jotai.Atom<null | boolean>;
    termAgentVisible: jotai.PrimitiveAtom<boolean>;
    termAgentComposerOpen: jotai.PrimitiveAtom<boolean>;
    termAgentInput: jotai.PrimitiveAtom<string>;
    termAgentNotice: jotai.PrimitiveAtom<string | null>;
    termAgentChatId: jotai.PrimitiveAtom<string>;
    // Permissions posture controls per-call approval strictness.
    // "default" (every mutating call asks), "acceptEdits" (file edits in
    // cwd auto-allowed; the bundled default), "bypass" (all auto-allowed
    // — bypass-immune safety still fires). Cycled by Shift+Tab in the
    // overlay; stored per-chat.
    termAgentPosture: jotai.PrimitiveAtom<string>;
    termAgentSendMessage: UseChatSendMessageType | null;
    termAgentSetMessages: UseChatSetMessagesType | null;
    termAgentStop: (() => void) | null;
    termAgentChatStatus: string;
    termAgentRealMessage: any | null;
    termAgentPendingContext: { cwd?: string; connection?: string; last_command?: string };
    searchAtoms?: SearchAtoms;

    constructor({ blockId, nodeModel, tabModel }: ViewModelInitType) {
        this.viewType = "term";
        this.blockId = blockId;
        this.tabModel = tabModel;
        this.termWshClient = new TermWshClient(blockId, this);
        DefaultRouter.registerRoute(makeFeBlockRouteId(blockId), this.termWshClient);
        this.nodeModel = nodeModel;
        this.blockAtom = WOS.getWaveObjectAtom<Block>(`block:${blockId}`);
        this.vdomBlockId = jotai.atom((get) => {
            const blockData = get(this.blockAtom);
            return blockData?.meta?.["term:vdomblockid"];
        });
        this.vdomToolbarBlockId = jotai.atom((get) => {
            const blockData = get(this.blockAtom);
            return blockData?.meta?.["term:vdomtoolbarblockid"];
        });
        this.vdomToolbarTarget = jotai.atom<VDomTargetToolbar>(null) as jotai.PrimitiveAtom<VDomTargetToolbar>;
        this.termMode = jotai.atom((get) => {
            const blockData = get(this.blockAtom);
            return blockData?.meta?.["term:mode"] ?? "term";
        });
        this.isRestarting = jotai.atom(false);
        this.termAgentVisible = jotai.atom(false) as jotai.PrimitiveAtom<boolean>;
        this.termAgentComposerOpen = jotai.atom(false) as jotai.PrimitiveAtom<boolean>;
        this.termAgentInput = jotai.atom("") as jotai.PrimitiveAtom<string>;
        this.termAgentNotice = jotai.atom(null) as jotai.PrimitiveAtom<string | null>;
        this.termAgentChatId = jotai.atom(crypto.randomUUID()) as jotai.PrimitiveAtom<string>;
        this.termAgentPosture = jotai.atom("acceptEdits") as jotai.PrimitiveAtom<string>;
        this.termAgentSendMessage = null;
        this.termAgentSetMessages = null;
        this.termAgentStop = null;
        this.termAgentChatStatus = "ready";
        this.termAgentRealMessage = null;
        this.termAgentPendingContext = {};
        this.viewIcon = jotai.atom((get) => {
            const termMode = get(this.termMode);
            if (termMode == "vdom") {
                return { elemtype: "iconbutton", icon: "bolt" };
            }
            return { elemtype: "iconbutton", icon: "terminal" };
        });
        this.viewName = jotai.atom((get) => {
            const blockData = get(this.blockAtom);
            const termMode = get(this.termMode);
            if (termMode == "vdom") {
                return "Wave App";
            }
            if (blockData?.meta?.controller == "cmd") {
                return "";
            }
            return "";
        });
        this.viewText = jotai.atom((get) => {
            const termMode = get(this.termMode);
            if (termMode == "vdom") {
                return [
                    {
                        elemtype: "iconbutton",
                        icon: "square-terminal",
                        title: "Switch back to Terminal",
                        click: () => {
                            this.setTermMode("term");
                        },
                    },
                ];
            }
            const vdomBlockId = get(this.vdomBlockId);
            const rtn: HeaderElem[] = [];
            if (vdomBlockId) {
                rtn.push({
                    elemtype: "iconbutton",
                    icon: "bolt",
                    title: "Switch to Wave App",
                    click: () => {
                        this.setTermMode("vdom");
                    },
                });
            }
            const isCmd = get(this.isCmdController);
            if (isCmd) {
                const blockMeta = get(this.blockAtom)?.meta;
                let cmdText = blockMeta?.["cmd"];
                const cmdArgs = blockMeta?.["cmd:args"];
                if (cmdArgs != null && Array.isArray(cmdArgs) && cmdArgs.length > 0) {
                    cmdText += " " + cmdArgs.join(" ");
                }
                rtn.push({
                    elemtype: "text",
                    text: cmdText,
                    noGrow: true,
                });
                const isRestarting = get(this.isRestarting);
                if (isRestarting) {
                    rtn.push({
                        elemtype: "iconbutton",
                        icon: "refresh",
                        iconColor: "var(--success-color)",
                        iconSpin: true,
                        title: "Restarting Command",
                        noAction: true,
                    });
                } else {
                    const fullShellProcStatus = get(this.shellProcFullStatus);
                    if (fullShellProcStatus?.shellprocstatus == "done") {
                        if (fullShellProcStatus?.shellprocexitcode == 0) {
                            rtn.push({
                                elemtype: "iconbutton",
                                icon: "check",
                                iconColor: "var(--success-color)",
                                title: "Command Exited Successfully",
                                noAction: true,
                            });
                        } else {
                            rtn.push({
                                elemtype: "iconbutton",
                                icon: "xmark-large",
                                iconColor: "var(--error-color)",
                                title: "Exit Code: " + fullShellProcStatus?.shellprocexitcode,
                                noAction: true,
                            });
                        }
                    }
                }
            }
            const isMI = get(this.tabModel.isTermMultiInput);
            if (isMI && this.isBasicTerm(get)) {
                rtn.push({
                    elemtype: "textbutton",
                    text: "Multi Input ON",
                    className: "yellow !py-[2px] !px-[10px] text-[11px] font-[500]",
                    title: "Input will be sent to all connected terminals (click to disable)",
                    onClick: () => {
                        globalStore.set(this.tabModel.isTermMultiInput, false);
                    },
                });
            }
            return rtn;
        });
        this.manageConnection = jotai.atom((get) => {
            const termMode = get(this.termMode);
            if (termMode == "vdom") {
                return false;
            }
            const isCmd = get(this.isCmdController);
            if (isCmd) {
                return false;
            }
            return true;
        });
        this.useTermHeader = jotai.atom((get) => {
            const termMode = get(this.termMode);
            if (termMode == "vdom") {
                return false;
            }
            const isCmd = get(this.isCmdController);
            if (isCmd) {
                return false;
            }
            return true;
        });
        this.filterOutNowsh = jotai.atom(false);
        this.termBPMAtom = getOverrideConfigAtom(blockId, "term:allowbracketedpaste");
        this.termThemeNameAtom = useBlockAtom(blockId, "termthemeatom", () => {
            return jotai.atom<string>((get) => {
                return get(getOverrideConfigAtom(this.blockId, "term:theme")) ?? DefaultTermTheme;
            });
        });
        this.termTransparencyAtom = useBlockAtom(blockId, "termtransparencyatom", () => {
            return jotai.atom<number>((get) => {
                const value = get(getOverrideConfigAtom(this.blockId, "term:transparency")) ?? 0.5;
                return boundNumber(value, 0, 1);
            });
        });
        this.blockBg = jotai.atom((get) => {
            const fullConfig = get(atoms.fullConfigAtom);
            const themeName = get(this.termThemeNameAtom);
            const termTransparency = get(this.termTransparencyAtom);
            const [_, bgcolor] = computeTheme(fullConfig, themeName, termTransparency);
            if (bgcolor != null) {
                return { bg: bgcolor };
            }
            return null;
        });
        this.connStatus = jotai.atom((get) => {
            const blockData = get(this.blockAtom);
            const connName = blockData?.meta?.connection;
            const connAtom = getConnStatusAtom(connName);
            return get(connAtom);
        });
        this.fontSizeAtom = useBlockAtom(blockId, "fontsizeatom", () => {
            return jotai.atom<number>((get) => {
                const blockData = get(this.blockAtom);
                const fsSettingsAtom = getSettingsKeyAtom("term:fontsize");
                const settingsFontSize = get(fsSettingsAtom);
                const connName = blockData?.meta?.connection;
                const fullConfig = get(atoms.fullConfigAtom);
                const connFontSize = fullConfig?.connections?.[connName]?.["term:fontsize"];
                const rtnFontSize = blockData?.meta?.["term:fontsize"] ?? connFontSize ?? settingsFontSize ?? 12;
                if (typeof rtnFontSize != "number" || isNaN(rtnFontSize) || rtnFontSize < 4 || rtnFontSize > 64) {
                    return 12;
                }
                return rtnFontSize;
            });
        });
        this.noPadding = jotai.atom(true);
        this.endIconButtons = jotai.atom((get) => {
            const blockData = get(this.blockAtom);
            const shellProcStatus = get(this.shellProcStatus);
            const connStatus = get(this.connStatus);
            const isCmd = get(this.isCmdController);
            const rtn: IconButtonDecl[] = [];

            const isAIPanelOpen = get(WorkspaceLayoutModel.getInstance().panelVisibleAtom);
            if (isAIPanelOpen) {
                const shellIntegrationButton = this.getShellIntegrationIconButton(get);
                if (shellIntegrationButton) {
                    rtn.push(shellIntegrationButton);
                }
            }

            if (get(getSettingsKeyAtom("debug:webglstatus"))) {
                const webglButton = this.getWebGlIconButton(get);
                if (webglButton) {
                    rtn.push(webglButton);
                }
            }

            if (blockData?.meta?.["controller"] != "cmd" && shellProcStatus != "done") {
                return rtn;
            }
            if (connStatus?.status != "connected") {
                return rtn;
            }
            let iconName: string = null;
            let title: string = null;
            const noun = isCmd ? "Command" : "Shell";
            if (shellProcStatus == "init") {
                iconName = "play";
                title = "Click to Start " + noun;
            } else if (shellProcStatus == "running") {
                iconName = "refresh";
                title = noun + " Running. Click to Restart";
            } else if (shellProcStatus == "done") {
                iconName = "refresh";
                title = noun + " Exited. Click to Restart";
            }
            if (iconName != null) {
                const buttonDecl: IconButtonDecl = {
                    elemtype: "iconbutton",
                    icon: iconName,
                    click: () => fireAndForget(() => this.forceRestartController()),
                    title: title,
                };
                rtn.push(buttonDecl);
            }
            return rtn;
        });
        this.isCmdController = jotai.atom((get) => {
            const controllerMetaAtom = getBlockMetaKeyAtom(this.blockId, "controller");
            return get(controllerMetaAtom) == "cmd";
        });
        this.shellProcFullStatus = jotai.atom(null) as jotai.PrimitiveAtom<BlockControllerRuntimeStatus>;
        const initialShellProcStatus = services.BlockService.GetControllerStatus(blockId);
        initialShellProcStatus.then((rts) => {
            this.updateShellProcStatus(rts);
        });
        this.shellProcStatusUnsubFn = waveEventSubscribeSingle({
            eventType: "controllerstatus",
            scope: WOS.makeORef("block", blockId),
            handler: (event) => {
                this.updateShellProcStatus(event.data);
            },
        });
        this.shellProcStatus = jotai.atom((get) => {
            const fullStatus = get(this.shellProcFullStatus);
            return fullStatus?.shellprocstatus ?? "init";
        });
        this.termDurableStatus = jotai.atom((get) => {
            const isDurable = get(getBlockTermDurableAtom(this.blockId));
            if (!isDurable) {
                return null;
            }
            const blockJobStatus = get(this.blockJobStatusAtom);
            if (blockJobStatus?.jobid == null || blockJobStatus?.status == null) {
                return null;
            }
            return blockJobStatus;
        });
        this.termConfigedDurable = getBlockTermDurableAtom(this.blockId);
        this.blockJobStatusAtom = jotai.atom(null) as jotai.PrimitiveAtom<BlockJobStatusData>;
        this.blockJobStatusVersionTs = 0;
        const initialBlockJobStatus = RpcApi.BlockJobStatusCommand(TabRpcClient, blockId);
        initialBlockJobStatus
            .then((status) => {
                this.handleBlockJobStatusUpdate(status);
            })
            .catch((error) => {
                console.log("error getting initial block job status", error);
            });
        this.blockJobStatusUnsubFn = waveEventSubscribeSingle({
            eventType: "block:jobstatus",
            scope: `block:${blockId}`,
            handler: (event) => {
                this.handleBlockJobStatusUpdate(event.data);
            },
        });
        this.termBPMUnsubFn = globalStore.sub(this.termBPMAtom, () => {
            if (this.termRef.current?.terminal) {
                const allowBPM = globalStore.get(this.termBPMAtom) ?? true;
                this.termRef.current.terminal.options.ignoreBracketedPasteMode = !allowBPM;
            }
        });
        const termCursorAtom = getOverrideConfigAtom(blockId, "term:cursor");
        this.termCursorUnsubFn = globalStore.sub(termCursorAtom, () => {
            if (this.termRef.current?.terminal) {
                this.termRef.current.setCursorStyle(globalStore.get(termCursorAtom));
            }
        });
        const termCursorBlinkAtom = getOverrideConfigAtom(blockId, "term:cursorblink");
        this.termCursorBlinkUnsubFn = globalStore.sub(termCursorBlinkAtom, () => {
            if (this.termRef.current?.terminal) {
                this.termRef.current.setCursorBlink(globalStore.get(termCursorBlinkAtom) ?? false);
            }
        });
    }

    getShellIntegrationIconButton(get: jotai.Getter): IconButtonDecl | null {
        if (!this.termRef.current?.shellIntegrationStatusAtom) {
            return null;
        }
        const shellIntegrationStatus = get(this.termRef.current.shellIntegrationStatusAtom);
        const claudeCodeActive = get(this.termRef.current.claudeCodeActiveAtom);
        const icon = claudeCodeActive ? React.createElement(TermClaudeIcon) : "sparkles";
        if (shellIntegrationStatus == null) {
            return {
                elemtype: "iconbutton",
                icon,
                className: "text-muted",
                title: "No shell integration — Wave AI unable to run commands.",
                noAction: true,
            };
        }
        if (shellIntegrationStatus === "ready") {
            return {
                elemtype: "iconbutton",
                icon,
                className: "text-accent",
                title: "Shell ready — Wave AI can run commands in this terminal.",
                noAction: true,
            };
        }
        if (shellIntegrationStatus === "running-command") {
            let title = claudeCodeActive
                ? "Claude Code Detected"
                : "Shell busy — Wave AI unable to run commands while another command is running.";

            if (this.termRef.current) {
                const inAltBuffer = this.termRef.current.terminal?.buffer?.active?.type === "alternate";
                const lastCommand = get(this.termRef.current.lastCommandAtom);
                const blockingCmd = getBlockingCommand(lastCommand, inAltBuffer);
                if (blockingCmd) {
                    title = `Wave AI integration disabled while you're inside ${blockingCmd}.`;
                }
            }

            return {
                elemtype: "iconbutton",
                icon,
                className: "text-warning",
                title: title,
                noAction: true,
            };
        }
        return null;
    }

    getWebGlIconButton(get: jotai.Getter): IconButtonDecl | null {
        if (!WebGLSupported) {
            return {
                elemtype: "iconbutton",
                icon: "microchip",
                iconColor: "var(--error-color)",
                title: "WebGL not supported",
                noAction: true,
            };
        }
        if (!this.termRef.current?.webglEnabledAtom) {
            return null;
        }
        const webglEnabled = get(this.termRef.current.webglEnabledAtom);
        if (webglEnabled) {
            return {
                elemtype: "iconbutton",
                icon: "microchip",
                iconColor: "var(--success-color)",
                title: "WebGL enabled (click to disable)",
                click: () => this.toggleWebGl(),
            };
        }
        return {
            elemtype: "iconbutton",
            icon: "microchip",
            iconColor: "var(--secondary-text-color)",
            title: "WebGL disabled (click to enable)",
            click: () => this.toggleWebGl(),
        };
    }

    get viewComponent(): ViewComponent {
        return TerminalView as ViewComponent;
    }

    isBasicTerm(getFn: jotai.Getter): boolean {
        const termMode = getFn(this.termMode);
        if (termMode == "vdom") {
            return false;
        }
        const blockData = getFn(this.blockAtom);
        if (blockData?.meta?.controller == "cmd") {
            return false;
        }
        return true;
    }

    multiInputHandler(data: string) {
        const tvms = getAllBasicTermModels();
        for (const tvm of tvms) {
            if (tvm != this) {
                tvm.sendDataToController(data);
            }
        }
    }

    sendDataToController(data: string) {
        const b64data = stringToBase64(data);
        RpcApi.ControllerInputCommand(TabRpcClient, { blockid: this.blockId, inputdata64: b64data });
    }

    registerTermAgentChat(
        sendMessage: UseChatSendMessageType,
        setMessages: UseChatSetMessagesType,
        status: string,
        stop: () => void
    ) {
        this.termAgentSendMessage = sendMessage;
        this.termAgentSetMessages = setMessages;
        this.termAgentChatStatus = status;
        this.termAgentStop = stop;
    }

    getAndClearTermAgentMessage(): any {
        return this.termAgentRealMessage ?? { messageid: crypto.randomUUID(), parts: [{ type: "text", text: "continue" }] };
    }

    getAndClearTermAgentPendingContext(): { cwd?: string; connection?: string; last_command?: string } {
        const ctx = this.termAgentPendingContext;
        this.termAgentPendingContext = {};
        return ctx;
    }

    getTermAgentCwd(): string | undefined {
        return this.buildTermAgentContext().cwd;
    }

    getTermAgentModelOverride(): string {
        return this.termAgentModelOverride ?? "";
    }

    termAgentModelOverride: string | null = null;

    buildTermAgentContext(): { cwd?: string; connection?: string; last_command?: string } {
        const cwd = globalStore.get(getBlockMetaKeyAtom(this.blockId, "cmd:cwd"));
        const connName = globalStore.get(getBlockMetaKeyAtom(this.blockId, "connection"));
        const lastCommand = this.termRef.current?.lastCommandAtom
            ? globalStore.get(this.termRef.current.lastCommandAtom)
            : null;
        const out: { cwd?: string; connection?: string; last_command?: string } = {};
        if (typeof cwd === "string" && cwd) out.cwd = cwd;
        if (typeof connName === "string" && connName) out.connection = connName;
        if (typeof lastCommand === "string" && lastCommand) out.last_command = lastCommand;
        return out;
    }

    setTermAgentNotice(message: string | null) {
        globalStore.set(this.termAgentNotice, message);
    }

    openTermAgentComposer() {
        globalStore.set(this.termAgentVisible, true);
        globalStore.set(this.termAgentComposerOpen, true);
        globalStore.set(this.termAgentInput, "");
        globalStore.set(this.termAgentNotice, null);
    }

    closeTermAgentComposer() {
        globalStore.set(this.termAgentComposerOpen, false);
        globalStore.set(this.termAgentInput, "");
    }

    hideTermAgentOverlay() {
        this.closeTermAgentComposer();
        globalStore.set(this.termAgentVisible, false);
    }

    termAgentLastPlanPath: string | null = null;
    termAgentPendingPlanPath: string | null = null;

    clearTermAgentSession() {
        this.termAgentStop?.();
        globalStore.set(this.termAgentChatId, crypto.randomUUID());
        globalStore.set(this.termAgentNotice, null);
        this.termAgentSetMessages?.([]);
        this.termAgentLastPlanPath = null;
    }

    getAndClearTermAgentPlanPath(): string {
        const path = this.termAgentPendingPlanPath;
        this.termAgentPendingPlanPath = null;
        return path ?? "";
    }

    executePlan() {
        if (!this.termAgentLastPlanPath || !this.termAgentSendMessage) {
            return;
        }
        const planPath = this.termAgentLastPlanPath;
        const msg = `Execute the plan at ${planPath}`;
        this.termAgentPendingPlanPath = planPath;
        this.termAgentRealMessage = {
            messageid: crypto.randomUUID(),
            parts: [{ type: "text", text: msg }],
        };
        this.termAgentPendingContext = this.buildTermAgentContext();
        globalStore.set(this.termAgentVisible, true);
        globalStore.set(this.termAgentNotice, null);
        this.closeTermAgentComposer();
        this.termAgentSendMessage({ parts: [{ type: "text", text: msg }] });
    }

    canOpenTermAgent(): boolean {
        const blockData = globalStore.get(this.blockAtom);
        if (blockData?.meta?.controller === "cmd") {
            return false;
        }
        if (blockData?.meta?.["term:mode"] === "vdom") {
            return false;
        }
        const shellState = this.termRef.current?.shellIntegrationStatusAtom
            ? globalStore.get(this.termRef.current.shellIntegrationStatusAtom)
            : null;
        const inputEmpty = this.termRef.current?.shellInputEmptyAtom
            ? globalStore.get(this.termRef.current.shellInputEmptyAtom)
            : null;
        // when shell integration is not available, allow opening anyway
        if (shellState == null) {
            return true;
        }
        return shellState === "ready" && inputEmpty === true;
    }

    async submitTermAgentPrompt(): Promise<void> {
        const userInput = globalStore.get(this.termAgentInput).trim();
        if (userInput === "") {
            this.closeTermAgentComposer();
            return;
        }
        if (this.termAgentChatStatus === "streaming" || this.termAgentChatStatus === "submitted") {
            return;
        }
        if (!this.termAgentSendMessage) {
            this.setTermAgentNotice("Terminal agent is still initializing.");
            return;
        }
        if (userInput === "clear" || userInput === "new") {
            this.clearTermAgentSession();
            this.closeTermAgentComposer();
            globalStore.set(this.termAgentVisible, true);
            return;
        }
        if (userInput.startsWith("model ")) {
            const modelName = userInput.slice("model ".length).trim();
            if (modelName) {
                this.termAgentModelOverride = modelName;
                globalStore.set(this.termAgentChatId, crypto.randomUUID());
                globalStore.set(this.termAgentNotice, `Model switched to: ${modelName}`);
                this.closeTermAgentComposer();
                globalStore.set(this.termAgentVisible, true);
            }
            return;
        }

        // Permissions v2: no more mode prefix routing. The prompt goes
        // straight to the agent in "do" mode (full toolset). Posture
        // — set on the overlay via Shift+Tab — is the only strictness
        // axis the user controls per-chat.
        this.termAgentRealMessage = {
            messageid: crypto.randomUUID(),
            parts: [{ type: "text", text: userInput }],
        };
        this.termAgentPendingContext = this.buildTermAgentContext();
        globalStore.set(this.termAgentVisible, true);
        globalStore.set(this.termAgentNotice, null);
        this.closeTermAgentComposer();

        try {
            await this.termAgentSendMessage({
                parts: [{ type: "text", text: userInput }] as any,
            });
        } catch (error) {
            console.error("failed to submit terminal agent prompt", error);
            const message = error instanceof Error ? error.message : String(error);
            this.setTermAgentNotice(message);
        }
    }

    setTermMode(mode: "term" | "vdom") {
        if (mode == "term") {
            mode = null;
        }
        RpcApi.SetMetaCommand(TabRpcClient, {
            oref: WOS.makeORef("block", this.blockId),
            meta: { "term:mode": mode },
        });
    }

    getTermRenderer(): "webgl" | "dom" {
        return this.termRef.current?.getTermRenderer() ?? "dom";
    }

    isWebGlEnabled(): boolean {
        return this.termRef.current?.isWebGlEnabled() ?? false;
    }

    toggleWebGl() {
        if (!this.termRef.current) {
            return;
        }
        const renderer = this.termRef.current.getTermRenderer() === "webgl" ? "dom" : "webgl";
        this.termRef.current.setTermRenderer(renderer);
    }

    triggerRestartAtom() {
        globalStore.set(this.isRestarting, true);
        setTimeout(() => {
            globalStore.set(this.isRestarting, false);
        }, 300);
    }

    handleBlockJobStatusUpdate(status: BlockJobStatusData) {
        if (status?.versionts == null) {
            return;
        }
        if (status.versionts <= this.blockJobStatusVersionTs) {
            return;
        }
        this.blockJobStatusVersionTs = status.versionts;
        globalStore.set(this.blockJobStatusAtom, status);
    }

    updateShellProcStatus(fullStatus: BlockControllerRuntimeStatus) {
        if (fullStatus == null) {
            return;
        }
        const curStatus = globalStore.get(this.shellProcFullStatus);
        if (curStatus == null || curStatus.version < fullStatus.version) {
            globalStore.set(this.shellProcFullStatus, fullStatus);
        }
    }

    getVDomModel(): VDomModel {
        const vdomBlockId = globalStore.get(this.vdomBlockId);
        if (!vdomBlockId) {
            return null;
        }
        const bcm = getBlockComponentModel(vdomBlockId);
        if (!bcm) {
            return null;
        }
        return bcm.viewModel as VDomModel;
    }

    getVDomToolbarModel(): VDomModel {
        const vdomToolbarBlockId = globalStore.get(this.vdomToolbarBlockId);
        if (!vdomToolbarBlockId) {
            return null;
        }
        const bcm = getBlockComponentModel(vdomToolbarBlockId);
        if (!bcm) {
            return null;
        }
        return bcm.viewModel as VDomModel;
    }

    dispose() {
        DefaultRouter.unregisterRoute(makeFeBlockRouteId(this.blockId));
        this.shellProcStatusUnsubFn?.();
        this.blockJobStatusUnsubFn?.();
        this.termBPMUnsubFn?.();
        this.termCursorUnsubFn?.();
        this.termCursorBlinkUnsubFn?.();
    }

    giveFocus(): boolean {
        if (this.searchAtoms && globalStore.get(this.searchAtoms.isOpen)) {
            console.log("search is open, not giving focus");
            return true;
        }
        const termMode = globalStore.get(this.termMode);
        if (termMode == "term") {
            if (this.termRef?.current?.terminal) {
                this.termRef.current.terminal.focus();
                return true;
            }
        }
        return false;
    }

    keyDownHandler(waveEvent: WaveKeyboardEvent): boolean {
        if (keyutil.checkKeyPressed(waveEvent, "Ctrl:r")) {
            const shellIntegrationStatus = readAtom(this.termRef?.current?.shellIntegrationStatusAtom);
            if (shellIntegrationStatus === "ready") {
                recordTEvent("action:term", { "action:type": "term:ctrlr" });
            }
            // just for telemetry, we allow this keybinding through, back to the terminal
            return false;
        }
        if (keyutil.checkKeyPressed(waveEvent, "Cmd:Escape")) {
            const blockAtom = WOS.getWaveObjectAtom<Block>(`block:${this.blockId}`);
            const blockData = globalStore.get(blockAtom);
            const newTermMode = blockData?.meta?.["term:mode"] == "vdom" ? null : "vdom";
            const vdomBlockId = globalStore.get(this.vdomBlockId);
            if (newTermMode == "vdom" && !vdomBlockId) {
                return;
            }
            this.setTermMode(newTermMode);
            return true;
        }
        if (keyutil.checkKeyPressed(waveEvent, "Shift:End")) {
            if (this.termRef?.current?.terminal) {
                this.termRef.current.terminal.scrollToBottom();
            }
            return true;
        }
        if (keyutil.checkKeyPressed(waveEvent, "Shift:Home")) {
            if (this.termRef?.current?.terminal) {
                this.termRef.current.terminal.scrollToLine(0);
            }
            return true;
        }
        if (isMacOS() && keyutil.checkKeyPressed(waveEvent, "Cmd:End")) {
            if (this.termRef?.current?.terminal) {
                this.termRef.current.terminal.scrollToBottom();
            }
            return true;
        }
        if (isMacOS() && keyutil.checkKeyPressed(waveEvent, "Cmd:Home")) {
            if (this.termRef?.current?.terminal) {
                this.termRef.current.terminal.scrollToLine(0);
            }
            return true;
        }
        if (keyutil.checkKeyPressed(waveEvent, "Shift:PageDown")) {
            if (this.termRef?.current?.terminal) {
                this.termRef.current.terminal.scrollPages(1);
            }
            return true;
        }
        if (keyutil.checkKeyPressed(waveEvent, "Shift:PageUp")) {
            if (this.termRef?.current?.terminal) {
                this.termRef.current.terminal.scrollPages(-1);
            }
            return true;
        }
        const blockData = globalStore.get(this.blockAtom);
        if (blockData.meta?.["term:mode"] == "vdom") {
            const vdomModel = this.getVDomModel();
            return vdomModel?.keyDownHandler(waveEvent);
        }
        return false;
    }

    shouldHandleCtrlVPaste(): boolean {
        // macOS never uses Ctrl-V for paste (uses Cmd-V)
        if (isMacOS()) {
            return false;
        }

        // Get the app:ctrlvpaste setting
        const ctrlVPasteAtom = getSettingsKeyAtom("app:ctrlvpaste");
        const ctrlVPasteSetting = globalStore.get(ctrlVPasteAtom);

        // If setting is explicitly set, use it
        if (ctrlVPasteSetting != null) {
            return ctrlVPasteSetting;
        }

        // Default behavior: Windows=true, Linux/other=false
        return isWindows();
    }

    handleTermAgentKeydown(event: KeyboardEvent): boolean {
        const waveEvent = keyutil.adaptFromReactOrNativeKeyEvent(event);
        const overlayVisible = globalStore.get(this.termAgentVisible);
        const composerOpen = globalStore.get(this.termAgentComposerOpen);

        if (composerOpen) {
            if (keyutil.checkKeyPressed(waveEvent, "Escape")) {
                this.closeTermAgentComposer();
                event.preventDefault();
                event.stopPropagation();
                return true;
            }
            if (keyutil.checkKeyPressed(waveEvent, "Enter")) {
                event.preventDefault();
                event.stopPropagation();
                fireAndForget(() => this.submitTermAgentPrompt());
                return true;
            }
            if (keyutil.checkKeyPressed(waveEvent, "Backspace")) {
                const currentInput = globalStore.get(this.termAgentInput);
                globalStore.set(this.termAgentInput, currentInput.slice(0, -1));
                event.preventDefault();
                event.stopPropagation();
                return true;
            }
            if (keyutil.isCharacterKeyEvent(waveEvent)) {
                const currentInput = globalStore.get(this.termAgentInput);
                globalStore.set(this.termAgentInput, currentInput + waveEvent.key);
                event.preventDefault();
                event.stopPropagation();
                return true;
            }
            if (
                keyutil.checkKeyPressed(waveEvent, "Ctrl:Shift:v") ||
                keyutil.checkKeyPressed(waveEvent, "Cmd:v") ||
                (this.shouldHandleCtrlVPaste() && keyutil.checkKeyPressed(waveEvent, "Ctrl:v"))
            ) {
                event.preventDefault();
                event.stopPropagation();
                navigator.clipboard
                    .readText()
                    .then((text) => {
                        if (!text) {
                            return;
                        }
                        const currentInput = globalStore.get(this.termAgentInput);
                        globalStore.set(this.termAgentInput, currentInput + text);
                    })
                    .catch((err) => {
                        console.error("failed to paste into terminal agent", err);
                    });
                return true;
            }
            return false;
        }

        if (overlayVisible && keyutil.checkKeyPressed(waveEvent, "Escape")) {
            this.hideTermAgentOverlay();
            event.preventDefault();
            event.stopPropagation();
            return true;
        }

        if (keyutil.isCharacterKeyEvent(waveEvent) && waveEvent.key === ":" && this.canOpenTermAgent()) {
            this.openTermAgentComposer();
            event.preventDefault();
            event.stopPropagation();
            return true;
        }

        return false;
    }

    handleTerminalKeydown(event: KeyboardEvent): boolean {
        const waveEvent = keyutil.adaptFromReactOrNativeKeyEvent(event);
        if (waveEvent.type != "keydown") {
            return true;
        }

        if (this.handleTermAgentKeydown(event)) {
            return false;
        }

        if (this.keyDownHandler(waveEvent)) {
            event.preventDefault();
            event.stopPropagation();
            return false;
        }

        if (isMacOS()) {
            if (keyutil.checkKeyPressed(waveEvent, "Cmd:ArrowLeft")) {
                this.sendDataToController("\x01"); // Ctrl-A (beginning of line)
                event.preventDefault();
                event.stopPropagation();
                return false;
            }
            if (keyutil.checkKeyPressed(waveEvent, "Cmd:ArrowRight")) {
                this.sendDataToController("\x05"); // Ctrl-E (end of line)
                event.preventDefault();
                event.stopPropagation();
                return false;
            }
        }
        if (keyutil.checkKeyPressed(waveEvent, "Shift:Enter")) {
            const shiftEnterNewlineAtom = getOverrideConfigAtom(this.blockId, "term:shiftenternewline");
            const shiftEnterNewlineEnabled = globalStore.get(shiftEnterNewlineAtom) ?? true;
            if (shiftEnterNewlineEnabled) {
                this.sendDataToController("\n");
                event.preventDefault();
                event.stopPropagation();
                return false;
            }
        }

        // Check for Ctrl-V paste (platform-dependent)
        if (this.shouldHandleCtrlVPaste() && keyutil.checkKeyPressed(waveEvent, "Ctrl:v")) {
            event.preventDefault();
            event.stopPropagation();
            getApi().nativePaste();
            return false;
        }

        if (keyutil.checkKeyPressed(waveEvent, "Ctrl:Shift:v")) {
            event.preventDefault();
            event.stopPropagation();
            getApi().nativePaste();
            // this.termRef.current?.pasteHandler();
            return false;
        } else if (keyutil.checkKeyPressed(waveEvent, "Ctrl:Shift:c")) {
            event.preventDefault();
            event.stopPropagation();
            let sel = this.termRef.current?.terminal.getSelection();
            if (!sel) {
                return false;
            }
            if (globalStore.get(getSettingsKeyAtom("term:trimtrailingwhitespace")) !== false) {
                sel = trimTerminalSelection(sel);
            }
            navigator.clipboard.writeText(sel);
            return false;
        } else if (keyutil.checkKeyPressed(waveEvent, "Cmd:k")) {
            event.preventDefault();
            event.stopPropagation();
            this.termRef.current?.terminal?.clear();
            return false;
        }
        const shellProcStatus = globalStore.get(this.shellProcStatus);
        if ((shellProcStatus == "done" || shellProcStatus == "init") && keyutil.checkKeyPressed(waveEvent, "Enter")) {
            fireAndForget(() => this.forceRestartController());
            return false;
        }
        const appHandled = appHandleKeyDown(waveEvent);
        if (appHandled) {
            event.preventDefault();
            event.stopPropagation();
            return false;
        }
        return true;
    }

    setTerminalTheme(themeName: string) {
        RpcApi.SetMetaCommand(TabRpcClient, {
            oref: WOS.makeORef("block", this.blockId),
            meta: { "term:theme": themeName },
        });
    }

    async forceRestartController() {
        if (globalStore.get(this.isRestarting)) {
            return;
        }
        this.triggerRestartAtom();
        await RpcApi.ControllerDestroyCommand(TabRpcClient, this.blockId);
        const termsize = {
            rows: this.termRef.current?.terminal?.rows,
            cols: this.termRef.current?.terminal?.cols,
        };
        await RpcApi.ControllerResyncCommand(TabRpcClient, {
            tabid: globalStore.get(atoms.staticTabId),
            blockid: this.blockId,
            forcerestart: true,
            rtopts: { termsize: termsize },
        });
    }

    async restartSessionWithDurability(isDurable: boolean) {
        await RpcApi.SetMetaCommand(TabRpcClient, {
            oref: WOS.makeORef("block", this.blockId),
            meta: { "term:durable": isDurable },
        });
        await RpcApi.ControllerDestroyCommand(TabRpcClient, this.blockId);
        const termsize = {
            rows: this.termRef.current?.terminal?.rows,
            cols: this.termRef.current?.terminal?.cols,
        };
        await RpcApi.ControllerResyncCommand(TabRpcClient, {
            tabid: globalStore.get(atoms.staticTabId),
            blockid: this.blockId,
            forcerestart: true,
            rtopts: { termsize: termsize },
        });
    }

    getContextMenuItems(): ContextMenuItem[] {
        const menu: ContextMenuItem[] = [];
        const hasSelection = this.termRef.current?.terminal?.hasSelection();
        const selection = hasSelection ? this.termRef.current?.terminal.getSelection() : null;

        if (hasSelection) {
            menu.push({
                label: "Copy",
                click: () => {
                    if (selection) {
                        const text =
                            globalStore.get(getSettingsKeyAtom("term:trimtrailingwhitespace")) !== false
                                ? trimTerminalSelection(selection)
                                : selection;
                        navigator.clipboard.writeText(text);
                    }
                },
            });
            menu.push({ type: "separator" });
        }

        const hoveredLinkUri = this.termRef.current?.hoveredLinkUri;
        if (hoveredLinkUri) {
            let hoveredURL: URL = null;
            try {
                hoveredURL = new URL(hoveredLinkUri);
            } catch (e) {
                // not a valid URL
            }
            if (hoveredURL) {
                menu.push({
                    label: hoveredURL.hostname ? "Open URL (" + hoveredURL.hostname + ")" : "Open URL",
                    click: () => {
                        createBlock({
                            meta: {
                                view: "web",
                                url: hoveredURL.toString(),
                            },
                        });
                    },
                });
                menu.push({
                    label: "Open URL in External Browser",
                    click: () => {
                        getApi().openExternal(hoveredURL.toString());
                    },
                });
                menu.push({ type: "separator" });
            }
        }

        menu.push({
            label: "Paste",
            click: () => {
                getApi().nativePaste();
            },
        });

        menu.push({ type: "separator" });

        const magnified = globalStore.get(this.nodeModel.isMagnified);
        menu.push({
            label: magnified ? "Un-Magnify Block" : "Magnify Block",
            click: () => {
                this.nodeModel.toggleMagnify();
            },
        });

        menu.push({ type: "separator" });

        const settingsItems = this.getSettingsMenuItems();
        menu.push(...settingsItems);

        return menu;
    }

    getSettingsMenuItems(): ContextMenuItem[] {
        return buildTermSettingsMenuItems({
            blockId: this.blockId,
            getScrollbackContent: () => this.termRef.current?.getScrollbackContent() ?? null,
            forceRestartController: () => this.forceRestartController(),
            restartSessionWithDurability: (isDurable) => this.restartSessionWithDurability(isDurable),
            lastCommand: globalStore.get(this.termRef?.current?.lastCommandAtom),
        });
    }
}

export function getAllBasicTermModels(): TermViewModel[] {
    const termModels: TermViewModel[] = [];
    const bcms = getAllBlockComponentModels();
    for (const bcm of bcms) {
        if (bcm?.viewModel?.viewType == "term") {
            const tvm = bcm.viewModel as TermViewModel;
            if (tvm.isBasicTerm((atom) => globalStore.get(atom))) {
                termModels.push(tvm);
            }
        }
    }
    return termModels;
}
