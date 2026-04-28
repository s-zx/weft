// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

import { UseChatSendMessageType, UseChatSetMessagesType, WaveUIMessage } from "@/store/aitypes";
import { ContextMenuModel } from "@/app/store/contextmenu";
import { getApi, getBlockMetaKeyAtom, getSettingsKeyAtom } from "@/app/store/global";
import { globalStore } from "@/app/store/jotaiStore";
import { waveEventSubscribeSingle } from "@/app/store/wps";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { TermAgentApprovalContext, TermAgentChatProvider, TermAgentMessagePartView } from "@/app/view/term/term-agent";
import { buildTermSettingsMenuItems } from "@/app/view/term/term-settings-menu";
import { computeTheme } from "@/app/view/term/termutil";
import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import { FolderIcon } from "@/app/fileexplorer/file-icons";
import { atoms } from "@/store/global";
import { getWebServerEndpoint } from "@/util/endpoints";
import { base64ToArray, cn, stringToBase64 } from "@/util/util";
import { formatRemoteUri } from "@/util/waveutil";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { quote as shellQuote } from "shell-quote";
import * as jotai from "jotai";
import { useAtomValue } from "jotai";
import * as React from "react";
import * as ReactDOM from "react-dom";
import "@xterm/xterm/css/xterm.css";
import "./termblocks.scss";

const PollIntervalMs = 10_000; // safety net; live updates arrive via wps events
const MaxRenderedBytesPerBlock = 256 * 1024;
// Total xterm buffer capacity per block (viewport + scrollback).  Bytes that
// scroll off during write land in scrollback and are pulled back when we
// resize the viewport to match actual content.  Sized well above any realistic
// block output bounded by MaxRenderedBytesPerBlock.
const MaxXtermRows = 2000;
const MinXtermRows = 1;

export type TimelineEntry =
    | { type: "cmd"; block: CmdBlock; ts: number }
    | { type: "agent-user"; id: string; text: string; mode: string; ts: number }
    | { type: "agent-response"; message: WaveUIMessage; streaming: boolean; ts: number }
    | { type: "agent-error"; id: string; text: string; ts: number };

export class TermBlocksViewModel implements ViewModel {
    viewType: string;
    blockId: string;

    viewIcon = jotai.atom<string>("");
    viewName = jotai.atom<string>("");
    noPadding = jotai.atom<boolean>(true);
    noHeader = jotai.atom<boolean>(false);
    viewText!: jotai.Atom<string>;
    termThemeNameAtom!: jotai.Atom<string>;
    termTransparencyAtom!: jotai.Atom<number>;
    termFontSizeAtom!: jotai.Atom<number>;

    blocksAtom: jotai.PrimitiveAtom<CmdBlock[]>;
    outputCacheAtom: jotai.PrimitiveAtom<Record<string, Uint8Array>>;
    loadingAtom: jotai.PrimitiveAtom<boolean>;
    errorAtom: jotai.PrimitiveAtom<string>;
    altScreenOIDAtom: jotai.PrimitiveAtom<string>;
    blockCwdAtom!: jotai.Atom<string>;
    homeAtom: jotai.PrimitiveAtom<string>;
    gitInfoAtom: jotai.PrimitiveAtom<GitInfoResponse | null>;
    gitPollTimer: ReturnType<typeof setInterval> | null = null;
    lastGitCwd = "";
    // minVisibleSeq hides every block with seq < this value; bumped on clear.
    minVisibleSeqAtom: jotai.PrimitiveAtom<number>;
    // Per-oid hide set for one-off "Delete block" actions.
    hiddenOidsAtom: jotai.PrimitiveAtom<Set<string>>;
    historyAtom: jotai.PrimitiveAtom<string[]>;
    selectedOidAtom: jotai.PrimitiveAtom<string>;

    disposed = false;
    pollTimer: ReturnType<typeof setInterval> | null = null;
    unsubs: (() => void)[] = [];
    inputRef: React.RefObject<HTMLTextAreaElement | null> = React.createRef();

    termAgentVisible = jotai.atom(false) as jotai.PrimitiveAtom<boolean>;
    termAgentComposerOpen = jotai.atom(false) as jotai.PrimitiveAtom<boolean>;
    termAgentInput = jotai.atom("") as jotai.PrimitiveAtom<string>;
    termAgentNotice = jotai.atom(null) as jotai.PrimitiveAtom<string | null>;
    termAgentChatId = jotai.atom(crypto.randomUUID()) as jotai.PrimitiveAtom<string>;
    // Permissions posture — see term-model.ts for the contract. Defaults
    // to acceptEdits per design §5.
    termAgentPosture = jotai.atom("acceptEdits") as jotai.PrimitiveAtom<string>;
    agentEntriesAtom = jotai.atom<TimelineEntry[]>([]) as jotai.PrimitiveAtom<TimelineEntry[]>;
    // Conversation-area errors. Mid-run failures (rate limit, network,
    // provider 5xx) belong in the chat flow next to the user's
    // triggering message, not in the input-row error bar (which is
    // for immediate user-action feedback like "Model switched to X").
    // syncAgentMessages folds these into the timeline.
    agentErrorsAtom = jotai.atom<{ id: string; text: string; ts: number }[]>([]) as jotai.PrimitiveAtom<
        { id: string; text: string; ts: number }[]
    >;
    // Stable timestamps for ai-sdk messages — useChat doesn't always
    // populate createdAt, and re-running syncAgentMessages every status
    // tick would otherwise stamp every message with `now` and push the
    // whole chat past any concurrent shell command. We snapshot the
    // first-seen ts per message id and reuse it on later syncs.
    agentMsgTsMap: Map<string, number> = new Map();
    timelineAtom!: jotai.Atom<TimelineEntry[]>;
    termAgentSendMessage: UseChatSendMessageType | null = null;
    termAgentSetMessages: UseChatSetMessagesType | null = null;
    termAgentStop: (() => void) | null = null;
    // Mirrors useChat's status ("ready" | "submitted" | "streaming"). An
    // atom so the timeline can render a "thinking" placeholder while we
    // wait for the first model chunk — without this, the gap between
    // submit and stream-start looks like the agent is unresponsive.
    termAgentChatStatusAtom = jotai.atom<string>("ready") as jotai.PrimitiveAtom<string>;
    termAgentChatStatus: string = "ready";
    termAgentRealMessage: any | null = null;
    termAgentPendingContext: { cwd?: string; connection?: string; last_command?: string } = {};

    constructor({ blockId }: ViewModelInitType) {
        this.viewType = "termblocks";
        this.blockId = blockId;
        this.blocksAtom = jotai.atom<CmdBlock[]>([]) as jotai.PrimitiveAtom<CmdBlock[]>;
        this.outputCacheAtom = jotai.atom<Record<string, Uint8Array>>({}) as jotai.PrimitiveAtom<
            Record<string, Uint8Array>
        >;
        this.loadingAtom = jotai.atom<boolean>(true);
        this.errorAtom = jotai.atom<string>("") as jotai.PrimitiveAtom<string>;
        this.altScreenOIDAtom = jotai.atom<string>("") as jotai.PrimitiveAtom<string>;
        this.homeAtom = jotai.atom<string>("") as jotai.PrimitiveAtom<string>;
        try {
            globalStore.set(this.homeAtom, getApi().getHomeDir() ?? "");
        } catch {
            // preview/test envs without the electron preload: leave home empty
        }
        const cwdMetaAtom = getBlockMetaKeyAtom(blockId, "cmd:cwd");
        this.blockCwdAtom = jotai.atom((get) => (get(cwdMetaAtom) as string) ?? "");
        const themeMetaAtom = getBlockMetaKeyAtom(blockId, "term:theme");
        this.termThemeNameAtom = jotai.atom((get) => (get(themeMetaAtom) as string) ?? "");
        const transparencyMetaAtom = getBlockMetaKeyAtom(blockId, "term:transparency");
        this.termTransparencyAtom = jotai.atom((get) => {
            const v = get(transparencyMetaAtom);
            return typeof v === "number" ? v : 0;
        });
        const fontSizeMetaAtom = getBlockMetaKeyAtom(blockId, "term:fontsize");
        const fontSizeSettingAtom = getSettingsKeyAtom("term:fontsize");
        this.termFontSizeAtom = jotai.atom((get) => {
            const override = get(fontSizeMetaAtom);
            if (typeof override === "number") return override;
            const fallback = get(fontSizeSettingAtom);
            return typeof fallback === "number" ? fallback : 12;
        });
        this.viewText = jotai.atom((get) => {
            const cwd = get(this.blockCwdAtom);
            const home = get(this.homeAtom);
            if (!cwd) return "";
            if (home && (cwd === home || cwd.startsWith(home + "/"))) {
                return "~" + cwd.slice(home.length);
            }
            return cwd;
        });
        this.gitInfoAtom = jotai.atom<GitInfoResponse | null>(null) as jotai.PrimitiveAtom<GitInfoResponse | null>;
        this.gitPollTimer = setInterval(() => {
            if (!this.disposed) {
                this.refreshGitInfo();
            }
        }, 4000);
        this.minVisibleSeqAtom = jotai.atom<number>(0) as jotai.PrimitiveAtom<number>;
        this.hiddenOidsAtom = jotai.atom<Set<string>>(new Set<string>()) as jotai.PrimitiveAtom<Set<string>>;
        this.historyAtom = jotai.atom<string[]>([]) as jotai.PrimitiveAtom<string[]>;
        this.selectedOidAtom = jotai.atom<string>("") as jotai.PrimitiveAtom<string>;
        this.timelineAtom = jotai.atom((get): TimelineEntry[] => {
            const blocks = get(this.blocksAtom);
            const minSeq = get(this.minVisibleSeqAtom);
            const hidden = get(this.hiddenOidsAtom);
            const agentEntries = get(this.agentEntriesAtom);
            const cmdEntries: TimelineEntry[] = blocks
                .filter((b) => b.state !== "prompt" && b.seq > minSeq && !hidden.has(b.oid))
                .map((b) => ({ type: "cmd" as const, block: b, ts: b.tspromptns / 1e6 }));
            if (agentEntries.length === 0) return cmdEntries;
            return [...cmdEntries, ...agentEntries].sort((a, b) => a.ts - b.ts);
        });
        // Initial RPC work is deferred because TabRpcClient is a live binding
        // that can still be undefined during module-evaluation / first-render
        // right after an HMR reload. queueMicrotask runs after module eval is
        // complete and the binding has been assigned.
        queueMicrotask(() => {
            if (this.disposed || TabRpcClient == null) {
                return;
            }
            this.loadShellHistory();
            // Ask wavesrv to (re)start the shell controller bound to this block.
            // The term view does the same thing via termwrap.resyncController; we
            // don't have a termwrap so we fire it from the model directly.  Without
            // this call, controller=shell meta alone is not enough — the shell
            // never actually spawns and input has nowhere to go.
            RpcApi.ControllerResyncCommand(TabRpcClient, {
                tabid: globalStore.get(atoms.staticTabId),
                blockid: blockId,
            }).catch((e) => {
                console.warn("termblocks: ControllerResync failed", blockId, e);
            });
            this.fetchBlocks();
        });

        this.pollTimer = setInterval(() => {
            if (!this.disposed) {
                this.fetchBlocks();
            }
        }, PollIntervalMs);

        const scope = `block:${blockId}`;
        this.unsubs.push(
            waveEventSubscribeSingle({
                eventType: "cmdblock:row",
                scope,
                handler: (ev) => {
                    const row = ev.data as CmdBlock | undefined;
                    if (row != null) {
                        this.applyRow(row);
                    }
                },
            })
        );
        this.unsubs.push(
            waveEventSubscribeSingle({
                eventType: "cmdblock:chunk",
                scope,
                handler: (ev) => {
                    const chunk = ev.data as CmdBlockChunkEvent | undefined;
                    if (chunk != null) {
                        this.applyChunk(chunk);
                    }
                },
            })
        );
        this.unsubs.push(
            waveEventSubscribeSingle({
                eventType: "cmdblock:altscreen",
                scope,
                handler: (ev) => {
                    const data = ev.data as CmdBlockAltScreenEvent | undefined;
                    if (data == null) return;
                    console.log("[cd-bug] cmdblock:altscreen event", { enter: data.enter, oid: data.oid, blockId });
                    globalStore.set(this.altScreenOIDAtom, data.enter ? data.oid || "ALT" : "");
                },
            })
        );
        this.unsubs.push(
            waveEventSubscribeSingle({
                eventType: "cmdblock:clear",
                scope,
                handler: () => this.applyClear(),
            })
        );
    }

    applyClear() {
        if (this.disposed) return;
        const current = globalStore.get(this.blocksAtom);
        if (current.length === 0) return;
        // Hide everything up to (but not including) the newest row; subsequent
        // commands from this point on remain visible.
        const maxSeq = current[current.length - 1].seq;
        globalStore.set(this.minVisibleSeqAtom, maxSeq);
    }

    hideBlock(oid: string) {
        const current = globalStore.get(this.hiddenOidsAtom);
        const next = new Set(current);
        next.add(oid);
        globalStore.set(this.hiddenOidsAtom, next);
        if (globalStore.get(this.selectedOidAtom) === oid) {
            globalStore.set(this.selectedOidAtom, "");
        }
    }

    selectBlock(oid: string) {
        const current = globalStore.get(this.selectedOidAtom);
        globalStore.set(this.selectedOidAtom, current === oid ? "" : oid);
    }

    clearSelection() {
        if (globalStore.get(this.selectedOidAtom) !== "") {
            globalStore.set(this.selectedOidAtom, "");
        }
    }

    rerunBlock(cmd: string) {
        if (!cmd) return;
        this.submitInput(cmd);
    }

    async loadShellHistory() {
        try {
            const resp = await RpcApi.GetShellHistoryCommand(TabRpcClient, {});
            if (this.disposed) return;
            globalStore.set(this.historyAtom, resp?.lines ?? []);
        } catch (e) {
            console.warn("termblocks: history load failed", e);
        }
    }

    recordHistory(line: string) {
        if (!line) return;
        const current = globalStore.get(this.historyAtom);
        if (current.length > 0 && current[current.length - 1] === line) return;
        globalStore.set(this.historyAtom, [...current, line]);
    }

    applyRow(row: CmdBlock) {
        if (this.disposed) {
            return;
        }
        const current = globalStore.get(this.blocksAtom);
        const idx = current.findIndex((b) => b.oid === row.oid);
        let next: CmdBlock[];
        if (idx >= 0) {
            next = current.slice();
            next[idx] = row;
        } else {
            next = [...current, row].sort((a, b) => a.seq - b.seq);
        }
        globalStore.set(this.blocksAtom, next);
        globalStore.set(this.loadingAtom, false);

        // When a block transitions to "done", fetch the final output range so
        // xterm renders the frozen result. During "running" we've been
        // appending via chunk events, so the cached buffer should already
        // cover the output.
        if (
            row.state === "done" &&
            row.outputstartoffset != null &&
            row.outputendoffset != null
        ) {
            const cache = globalStore.get(this.outputCacheAtom);
            const existing = cache[row.oid];
            const expected = row.outputendoffset - row.outputstartoffset;
            if (existing == null || existing.length < expected) {
                this.fetchOutputFor(row);
            }
        }
    }

    applyChunk(chunk: CmdBlockChunkEvent) {
        if (this.disposed) {
            return;
        }
        const bytes = base64ToArray(chunk.data64);
        const cache = globalStore.get(this.outputCacheAtom);
        const current = cache[chunk.oid];
        let merged: Uint8Array;
        if (current == null) {
            merged = bytes;
        } else {
            merged = new Uint8Array(current.length + bytes.length);
            merged.set(current, 0);
            merged.set(bytes, current.length);
        }
        globalStore.set(this.outputCacheAtom, { ...cache, [chunk.oid]: merged });
    }

    get viewComponent(): ViewComponent {
        return TermBlocksView;
    }

    async sendBytes(bytes: string) {
        await RpcApi.ControllerInputCommand(TabRpcClient, {
            blockid: this.blockId,
            inputdata64: stringToBase64(bytes),
        });
    }

    async refreshGitInfo() {
        const cwd = globalStore.get(this.blockCwdAtom);
        if (!cwd) {
            globalStore.set(this.gitInfoAtom, null);
            this.lastGitCwd = "";
            return;
        }
        try {
            const info = await RpcApi.GetGitInfoCommand(TabRpcClient, cwd);
            if (this.disposed) return;
            globalStore.set(this.gitInfoAtom, info ?? null);
            this.lastGitCwd = cwd;
        } catch {
            // non-repo / missing git binary / timeout — just drop the pill
            if (!this.disposed) {
                globalStore.set(this.gitInfoAtom, null);
            }
        }
    }

    async sendResize(rows: number, cols: number) {
        if (rows <= 0 || cols <= 0) {
            return;
        }
        await RpcApi.ControllerInputCommand(TabRpcClient, {
            blockid: this.blockId,
            termsize: { rows, cols },
        });
    }

    async submitInput(raw: string) {
        await this.sendBytes(raw + "\r");
        this.fetchBlocks();
    }

    sendInterrupt() {
        void this.sendBytes("\x03");
    }

    async fetchBlocks() {
        try {
            const rows = await RpcApi.GetCmdBlocksCommand(TabRpcClient, {
                blockid: this.blockId,
            });
            if (this.disposed) {
                return;
            }
            const list = rows ?? [];
            globalStore.set(this.blocksAtom, list);
            globalStore.set(this.errorAtom, "");
            globalStore.set(this.loadingAtom, false);
            const cache = globalStore.get(this.outputCacheAtom);
            for (const b of list) {
                if (
                    b.state === "done" &&
                    b.outputstartoffset != null &&
                    b.outputendoffset != null &&
                    cache[b.oid] == null
                ) {
                    this.fetchOutputFor(b);
                }
            }
        } catch (e) {
            if (this.disposed) {
                return;
            }
            globalStore.set(this.errorAtom, String(e));
            globalStore.set(this.loadingAtom, false);
        }
    }

    async fetchOutputFor(block: CmdBlock) {
        if (block.outputstartoffset == null || block.outputendoffset == null) {
            return;
        }
        const rawSize = block.outputendoffset - block.outputstartoffset;
        if (rawSize <= 0) {
            const cache = { ...globalStore.get(this.outputCacheAtom), [block.oid]: new Uint8Array(0) };
            globalStore.set(this.outputCacheAtom, cache);
            return;
        }
        const size = Math.min(rawSize, MaxRenderedBytesPerBlock);
        try {
            const resp = await RpcApi.ReadBlockFileRangeCommand(TabRpcClient, {
                blockid: this.blockId,
                name: "term",
                offset: block.outputstartoffset,
                size,
            });
            if (this.disposed) {
                return;
            }
            const bytes = base64ToArray(resp.data64);
            const cache = { ...globalStore.get(this.outputCacheAtom), [block.oid]: bytes };
            globalStore.set(this.outputCacheAtom, cache);
        } catch (e) {
            console.warn("termblocks: fetchOutputFor failed", block.oid, e);
        }
    }

    giveFocus(): boolean {
        const el = this.inputRef.current;
        if (el == null) {
            return false;
        }
        el.focus();
        return document.activeElement === el;
    }

    dispose() {
        this.disposed = true;
        if (this.pollTimer != null) {
            clearInterval(this.pollTimer);
            this.pollTimer = null;
        }
        if (this.gitPollTimer != null) {
            clearInterval(this.gitPollTimer);
            this.gitPollTimer = null;
        }
        for (const unsub of this.unsubs) {
            try {
                unsub();
            } catch {
                // ignore
            }
        }
        this.unsubs = [];
    }

    getSettingsMenuItems(): ContextMenuItem[] {
        return buildTermSettingsMenuItems({ blockId: this.blockId });
    }

    get tabModel(): { tabId: string } {
        return { tabId: globalStore.get(atoms.staticTabId) };
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
        globalStore.set(this.termAgentVisible, false);
        globalStore.set(this.termAgentComposerOpen, false);
        globalStore.set(this.termAgentInput, "");
    }

    setTermAgentNotice(message: string | null) {
        globalStore.set(this.termAgentNotice, message);
    }

    buildTermAgentContext(): { cwd?: string; connection?: string; last_command?: string } {
        const cwd = this.worktreePath ?? globalStore.get(this.blockCwdAtom);
        const blocks = globalStore.get(this.blocksAtom);
        const lastDone = [...blocks].reverse().find((b) => b.state === "done");
        return {
            cwd: cwd || undefined,
            last_command: lastDone?.cmd ?? undefined,
        };
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
        if (userInput.toLowerCase() === "new") {
            this.clearTermAgentSession();
            this.closeTermAgentComposer();
            return;
        }
        if (userInput.toLowerCase() === "rewind") {
            this.rewindFiles();
            this.closeTermAgentComposer();
            return;
        }
        if (userInput.startsWith("worktree")) {
            this.handleWorktreeCommand(userInput);
            this.closeTermAgentComposer();
            return;
        }
        if (userInput.startsWith("model ")) {
            const modelName = userInput.slice("model ".length).trim();
            if (modelName) {
                globalStore.set(this.termAgentModelOverrideAtom, modelName);
                globalStore.set(this.termAgentChatId, crypto.randomUUID());
                globalStore.set(this.termAgentNotice, `Model switched to: ${modelName}`);
                this.closeTermAgentComposer();
            }
            return;
        }
        // Permissions v2: no mode prefix routing. Raw user input goes
        // straight to the agent in "do" mode; posture (Shift+Tab) is
        // the only per-chat strictness control.
        globalStore.set(this.termAgentNotice, null);
        this.termAgentRealMessage = { messageid: crypto.randomUUID(), parts: [{ type: "text", text: userInput }] };
        this.termAgentPendingContext = this.buildTermAgentContext();
        globalStore.set(this.termAgentComposerOpen, false);
        globalStore.set(this.termAgentInput, "");
        if (this.termAgentSendMessage) {
            await this.termAgentSendMessage({ parts: [{ type: "text", text: userInput }] });
        }
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
        globalStore.set(this.termAgentChatStatusAtom, status);
        this.termAgentStop = stop;
    }

    // Atom (not plain field) so the footer + model picker re-render
    // when the override changes — both via the `model xxx` shell
    // command and the new picker UI.
    termAgentModelOverrideAtom = jotai.atom<string | null>(null) as jotai.PrimitiveAtom<string | null>;

    getAndClearTermAgentMessage(): any {
        return this.termAgentRealMessage ?? { messageid: crypto.randomUUID(), parts: [{ type: "text", text: "continue" }] };
    }

    getTermAgentModelOverride(): string {
        return globalStore.get(this.termAgentModelOverrideAtom) ?? "";
    }

    getAndClearTermAgentPendingContext(): any {
        const ctx = this.termAgentPendingContext;
        this.termAgentPendingContext = {};
        return ctx;
    }

    // Stable cwd accessor for the permission approval prompt — needs
    // current cwd when the user picks "save to this project".
    // Mirrors term-model's implementation but the focused block id may
    // shift across calls in the multi-block view, so always query live.
    getTermAgentCwd(): string | undefined {
        const cwd = globalStore.get(this.blockCwdAtom);
        return cwd || undefined;
    }

    syncAgentMessages(messages: WaveUIMessage[], status: string) {
        this.termAgentChatStatus = status;
        globalStore.set(this.termAgentChatStatusAtom, status);
        const entries: TimelineEntry[] = [];
        for (const msg of messages) {
            // Use the message's createdAt if useChat populated it;
            // otherwise pin to first-seen wall-clock time. Either way
            // each message id keeps a stable ts across re-syncs so
            // interleaving with cmd blocks (which carry tspromptns)
            // is order-stable.
            const supplied = (msg as any).createdAt?.getTime?.();
            let ts: number;
            if (typeof supplied === "number") {
                ts = supplied;
            } else {
                ts = this.agentMsgTsMap.get(msg.id) ?? Date.now();
                if (!this.agentMsgTsMap.has(msg.id)) {
                    this.agentMsgTsMap.set(msg.id, ts);
                }
            }
            if (msg.role === "user") {
                const text = (msg.parts ?? [])
                    .filter((p) => p.type === "text")
                    .map((p) => (p as any).text ?? "")
                    .join("\n");
                if (text) {
                    // Permissions v2: timeline entries no longer carry a
                    // mode tag — the agent always runs in "do". Posture
                    // is per-chat, not per-message; doesn't belong on
                    // timeline entries.
                    entries.push({ type: "agent-user", id: msg.id, text, mode: "do", ts });
                }
            } else if (msg.role === "assistant") {
                const isLast = msg === messages[messages.length - 1];
                const streaming = (status === "streaming" || status === "submitted") && isLast;
                entries.push({ type: "agent-response", message: msg, streaming, ts });
            }
        }
        // Fold conversation-area errors in by timestamp so they sit
        // next to the message that triggered them (typically right
        // after the latest user message / partial assistant reply).
        for (const err of globalStore.get(this.agentErrorsAtom)) {
            entries.push({ type: "agent-error", id: err.id, text: err.text, ts: err.ts });
        }
        entries.sort((a, b) => a.ts - b.ts);
        globalStore.set(this.agentEntriesAtom, entries);
    }

    // addTermAgentChatError appends a conversation-area error. Used by
    // useTermAgentChat's onError for mid-run model/network failures
    // — the error renders inline in the timeline next to the user's
    // triggering message, looking just like an assistant response
    // (only red-tinted) because it conceptually IS the agent's
    // response for this turn. Pushes to the errors atom and re-merges
    // the timeline so the new error shows up at the right ts position.
    addTermAgentChatError(text: string) {
        const ts = Date.now();
        const newErr = { id: crypto.randomUUID(), text, ts };
        globalStore.set(this.agentErrorsAtom, [...globalStore.get(this.agentErrorsAtom), newErr]);
        // Splice into the live timeline. Errors are tail-of-turn so
        // appending then sorting is sufficient — sorting is needed
        // only because future model-message arrivals may have ts
        // values older than the error's (clock skew or batched
        // syncAgentMessages updates).
        const entries = globalStore.get(this.agentEntriesAtom);
        const merged: TimelineEntry[] = [
            ...entries,
            { type: "agent-error", id: newErr.id, text: newErr.text, ts },
        ];
        merged.sort((a, b) => a.ts - b.ts);
        globalStore.set(this.agentEntriesAtom, merged);
    }

    termAgentLastPlanPath: string | null = null;
    termAgentPendingPlanPath: string | null = null;

    clearTermAgentSession() {
        if (this.termAgentSetMessages) {
            this.termAgentSetMessages([]);
        }
        globalStore.set(this.termAgentChatId, crypto.randomUUID());
        globalStore.set(this.termAgentNotice, null);
        globalStore.set(this.agentErrorsAtom, []);
        globalStore.set(this.agentEntriesAtom, []);
        this.agentMsgTsMap.clear();
        this.termAgentLastPlanPath = null;
        globalStore.set(this.termAgentModelOverrideAtom, null);
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
        this.termAgentPendingPlanPath = this.termAgentLastPlanPath;
        this.termAgentRealMessage = {
            messageid: crypto.randomUUID(),
            parts: [{ type: "text", text: "go" }],
        };
        this.termAgentPendingContext = this.buildTermAgentContext();
        globalStore.set(this.termAgentNotice, null);
        this.closeTermAgentComposer();
        this.termAgentLastPlanPath = null;
        this.termAgentSendMessage({ parts: [{ type: "text", text: "go" }] });
    }

    worktreePath: string | null = null;
    worktreeName: string | null = null;

    async handleWorktreeCommand(input: string) {
        const parts = input.split(/\s+/);
        const sub = parts[1] ?? "";

        if (sub === "exit" || sub === "remove" || sub === "discard") {
            if (!this.worktreePath) {
                globalStore.set(this.termAgentNotice, "No active worktree");
                return;
            }
            const force = sub === "discard";
            try {
                const resp = await fetch(`${getWebServerEndpoint()}/api/agent-worktree`, {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({
                        action: "remove",
                        cwd: globalStore.get(this.blockCwdAtom),
                        path: this.worktreePath,
                        force,
                    }),
                });
                if (!resp.ok) {
                    const text = await resp.text();
                    if (resp.status === 409) {
                        globalStore.set(
                            this.termAgentNotice,
                            "Worktree has uncommitted changes — commit/push first, or run `:worktree discard` to throw them away."
                        );
                    } else {
                        globalStore.set(this.termAgentNotice, `Failed to remove worktree: ${text}`);
                    }
                    return;
                }
                globalStore.set(this.termAgentNotice, `Worktree ${this.worktreeName} removed`);
                this.worktreePath = null;
                this.worktreeName = null;
            } catch (e) {
                globalStore.set(this.termAgentNotice, `Failed to remove worktree: ${e}`);
            }
            return;
        }

        const name = sub || undefined;
        const cwd = globalStore.get(this.blockCwdAtom);
        try {
            const resp = await fetch(`${getWebServerEndpoint()}/api/agent-worktree`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ action: "create", cwd, name }),
            });
            if (!resp.ok) {
                const text = await resp.text();
                globalStore.set(this.termAgentNotice, `Worktree failed: ${text}`);
                return;
            }
            const data = await resp.json();
            this.worktreePath = data.path;
            this.worktreeName = data.name;
            globalStore.set(this.termAgentNotice, `Worktree created: ${data.name} (branch ${data.branch})`);
        } catch (e) {
            globalStore.set(this.termAgentNotice, `Worktree failed: ${e}`);
        }
    }

    async rewindFiles() {
        const chatId = globalStore.get(this.termAgentChatId);
        try {
            const resp = await fetch(`${getWebServerEndpoint()}/api/agent-rewind`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ chatid: chatId }),
            });
            if (!resp.ok) {
                const text = await resp.text();
                globalStore.set(this.termAgentNotice, `Rewind failed: ${text}`);
                return;
            }
            const data = await resp.json();
            globalStore.set(this.termAgentNotice, `Rewound: ${data.restored} file(s) restored`);
        } catch (e) {
            globalStore.set(this.termAgentNotice, `Rewind failed: ${e}`);
        }
    }

}

// countVisibleLines strips ANSI escapes (CSI + OSC) from bytes, splits on
// \n or \r, and returns the number of lines that contain at least one
// non-whitespace character after stripping.  This is what we use both to
// decide whether to render an xterm at all (0 visible lines -> hide) and
// to size the xterm rows so the block height hugs the real content.
function countVisibleLines(bytes: Uint8Array): number {
    if (bytes.length === 0) return 0;
    const out: number[] = [];
    let i = 0;
    while (i < bytes.length) {
        const b = bytes[i];
        if (b === 0x1b && i + 1 < bytes.length) {
            const next = bytes[i + 1];
            if (next === 0x5d /* ] */) {
                let j = i + 2;
                while (j < bytes.length) {
                    if (bytes[j] === 0x07) {
                        j++;
                        break;
                    }
                    if (bytes[j] === 0x1b && j + 1 < bytes.length && bytes[j + 1] === 0x5c) {
                        j += 2;
                        break;
                    }
                    j++;
                }
                i = j;
                continue;
            }
            if (next === 0x5b /* [ */) {
                let j = i + 2;
                while (j < bytes.length && (bytes[j] < 0x40 || bytes[j] > 0x7e)) j++;
                i = j + 1;
                continue;
            }
            // other two-byte escapes (ESC =, ESC >, etc.) — skip two bytes.
            i += 2;
            continue;
        }
        out.push(b);
        i++;
    }
    // Split on LF (0x0a); treat CR (0x0d) alone as a line break too.
    let lineHasNonSpace = false;
    let lines = 0;
    for (let k = 0; k < out.length; k++) {
        const b = out[k];
        if (b === 0x0a || b === 0x0d) {
            if (lineHasNonSpace) {
                lines++;
            }
            lineHasNonSpace = false;
            continue;
        }
        if (b !== 0x20 && b !== 0x09) {
            lineHasNonSpace = true;
        }
    }
    if (lineHasNonSpace) lines++;
    return lines;
}

function hasMeaningfulOutput(bytes: Uint8Array): boolean {
    return countVisibleLines(bytes) > 0;
}

const AltScreenXterm: React.FC<{
    bytes: Uint8Array;
    onData: (s: string) => void;
    onResize: (rows: number, cols: number) => void;
    theme: any;
    fontSize: number;
}> = ({ bytes, onData, onResize, theme, fontSize }) => {
    const containerRef = React.useRef<HTMLDivElement>(null);
    const termRef = React.useRef<Terminal | null>(null);
    const writtenRef = React.useRef<number>(0);
    const onDataRef = React.useRef(onData);
    const onResizeRef = React.useRef(onResize);
    // Refs keep the init effect's dep array empty (mount once) while still
    // letting the reactive update effects see the latest values.
    const themeRef = React.useRef(theme);
    const fontSizeRef = React.useRef(fontSize);
    React.useEffect(() => {
        onDataRef.current = onData;
        onResizeRef.current = onResize;
        themeRef.current = theme;
        fontSizeRef.current = fontSize;
    });

    React.useEffect(() => {
        const host = containerRef.current;
        console.log("[cd-bug] AltScreenXterm mount", { hasHost: host != null, hostRect: host?.getBoundingClientRect() });
        if (host == null) return;
        let term: Terminal;
        let fit: FitAddon;
        try {
            term = new Terminal({
                convertEol: false,
                cursorBlink: true,
                fontFamily: "ui-monospace, Menlo, Consolas, monospace",
                fontSize: fontSizeRef.current,
                theme: themeRef.current,
            });
            fit = new FitAddon();
            term.loadAddon(fit);
            term.open(host);
            console.log("[cd-bug] AltScreenXterm opened", { rows: term.rows, cols: term.cols });
        } catch (err) {
            console.error("[cd-bug] AltScreenXterm init failed", err);
            return;
        }

        const pushSize = () => {
            try {
                fit.fit();
            } catch (err) {
                console.warn("[cd-bug] AltScreenXterm fit.fit failed", err);
                return;
            }
            const t = termRef.current;
            if (t == null) return;
            onResizeRef.current(t.rows, t.cols);
        };

        termRef.current = term;
        writtenRef.current = 0;
        pushSize();
        term.focus();
        const onDataSub = term.onData((data) => onDataRef.current(data));
        // xterm itself reports resizes (font load, manual cols set); forward those too.
        const onResizeSub = term.onResize((s) => onResizeRef.current(s.rows, s.cols));

        const ro = new ResizeObserver(() => pushSize());
        ro.observe(host);
        return () => {
            ro.disconnect();
            onDataSub.dispose();
            onResizeSub.dispose();
            term.dispose();
            termRef.current = null;
        };
    }, []);

    React.useEffect(() => {
        const term = termRef.current;
        if (term == null) return;
        term.options.theme = theme;
    }, [theme]);

    React.useEffect(() => {
        const term = termRef.current;
        if (term == null) return;
        term.options.fontSize = fontSize;
    }, [fontSize]);

    React.useEffect(() => {
        const term = termRef.current;
        if (term == null) {
            console.log("[cd-bug] AltScreenXterm write skipped (no term)", { bytesLen: bytes.length });
            return;
        }
        const written = writtenRef.current;
        if (bytes.length === written) {
            return;
        }
        console.log("[cd-bug] AltScreenXterm writing", { written, newLen: bytes.length, delta: bytes.length - written });
        try {
            if (bytes.length < written) {
                term.reset();
                term.write(bytes);
            } else if (written === 0) {
                term.write(bytes);
            } else {
                term.write(bytes.subarray(written));
            }
            writtenRef.current = bytes.length;
        } catch (err) {
            console.error("[cd-bug] AltScreenXterm write failed", err);
        }
    }, [bytes]);

    return <div className="termblocks-altscreen" ref={containerRef} />;
};
AltScreenXterm.displayName = "AltScreenXterm";

const HideCursorSeq = "\x1b[?25l";

const XtermOutput: React.FC<{
    bytes: Uint8Array;
    interactive?: boolean;
    onData?: (s: string) => void;
    onResize?: (rows: number, cols: number) => void;
    theme: any;
    fontSize: number;
}> = ({ bytes, interactive = false, onData, onResize, theme, fontSize }) => {
    const containerRef = React.useRef<HTMLDivElement>(null);
    const termRef = React.useRef<Terminal | null>(null);
    const fitRef = React.useRef<FitAddon | null>(null);
    const writtenRef = React.useRef<number>(0);
    const onDataRef = React.useRef(onData);
    const onResizeRef = React.useRef(onResize);
    const themeRef = React.useRef(theme);
    const fontSizeRef = React.useRef(fontSize);
    React.useEffect(() => {
        onDataRef.current = onData;
        onResizeRef.current = onResize;
        themeRef.current = theme;
        fontSizeRef.current = fontSize;
    });

    // Forward wheel events to the outer scroll container.  xterm attaches a
    // wheel listener on its viewport even when scrollback is 0, which makes
    // scrolling impossible whenever the cursor is hovering a block. We trap
    // wheel on the wrapper in the capture phase, cancel it, and drive the
    // nearest scrollable ancestor ourselves.
    React.useEffect(() => {
        if (interactive) {
            return;
        }
        const host = containerRef.current;
        if (host == null) return;
        const onWheel = (ev: WheelEvent) => {
            let node: HTMLElement | null = host.parentElement;
            while (node != null) {
                const style = window.getComputedStyle(node);
                if (style.overflowY === "auto" || style.overflowY === "scroll") {
                    break;
                }
                node = node.parentElement;
            }
            if (node == null) return;
            ev.preventDefault();
            ev.stopPropagation();
            node.scrollTop += ev.deltaY;
            if (ev.deltaX) {
                node.scrollLeft += ev.deltaX;
            }
        };
        host.addEventListener("wheel", onWheel, { capture: true, passive: false });
        return () => host.removeEventListener("wheel", onWheel, { capture: true });
    }, [interactive]);

    React.useEffect(() => {
        const host = containerRef.current;
        if (host == null) return;
        const baseTheme = { ...themeRef.current };
        // Read-only rows hide the cursor so stale prompts don't show a caret.
        const termTheme = interactive
            ? baseTheme
            : { ...baseTheme, cursor: "transparent", cursorAccent: "transparent" };
        // Design note: xterm is the only authoritative interpreter of terminal
        // bytes (cursor moves, CR/LF, clear-line, etc.).  Any row-count
        // heuristic that scans the byte stream before writing will eventually
        // disagree with what xterm actually renders.
        //
        // We keep the viewport tiny initially but give xterm a large scrollback
        // so bytes that scroll off during write are preserved, not dropped.
        // After the write completes, sizeToContent() grows the viewport to
        // baseY+cursorY (xterm pulls the scrolled-off rows back into view),
        // which is the authoritative "rows actually used".
        const term = new Terminal({
            cols: 120,
            rows: MinXtermRows,
            disableStdin: !interactive,
            convertEol: true,
            cursorBlink: interactive,
            scrollback: MaxXtermRows,
            fontFamily: "ui-monospace, Menlo, Consolas, monospace",
            fontSize: fontSizeRef.current,
            theme: termTheme,
        });
        const fit = new FitAddon();
        term.loadAddon(fit);
        term.open(host);
        // Take cols from the container, never rows: fit.fit() would clamp rows
        // to the measured container height, which is fragile before content
        // has laid out (zero-height host → rows→1 → write-time overflow).
        // sizeToContent() is the authority on rows.
        try {
            const proposed = fit.proposeDimensions();
            if (proposed?.cols && proposed.cols > 0) {
                term.resize(Math.max(20, proposed.cols), term.rows);
            }
        } catch {
            // container not yet measured — default cols=120 is fine
        }
        termRef.current = term;
        fitRef.current = fit;
        writtenRef.current = 0;

        const subs: { dispose(): void }[] = [];
        if (interactive) {
            subs.push(term.onData((data) => onDataRef.current?.(data)));
            subs.push(term.onResize((s) => onResizeRef.current?.(s.rows, s.cols)));
            const t = termRef.current;
            if (t != null) {
                onResizeRef.current?.(t.rows, t.cols);
            }
            term.focus();
        } else {
            // belt-and-suspenders: prepend DECTCEM off so any byte-level
            // shell prompt draw doesn't reveal a stray cursor later.
            term.write(HideCursorSeq);
        }
        return () => {
            for (const s of subs) {
                try {
                    s.dispose();
                } catch {
                    // ignore
                }
            }
            term.dispose();
            termRef.current = null;
            fitRef.current = null;
        };
    }, [interactive]);

    React.useEffect(() => {
        const term = termRef.current;
        if (term == null) return;
        const t = interactive
            ? theme
            : { ...theme, cursor: "transparent", cursorAccent: "transparent" };
        term.options.theme = t;
    }, [theme, interactive]);

    // Resize the viewport to exactly match what xterm has rendered.
    //
    // Non-interactive (done) blocks: size rows to content only — absY +
    // (cursorX==0 ? 0 : 1) drops the phantom row that a trailing \n creates.
    // Then scrollToTop so the viewport starts at line 1 rather than xterm's
    // default bottom-anchored view (which would omit the first lines when
    // content had to scroll off into scrollback during write).
    //
    // Interactive (running) blocks: size rows to the entire buffer length
    // (= content + phantom cursor row).  The phantom is where the next
    // write will land, so showing it is correct for a live shell.  We do
    // NOT scroll to top — xterm's default bottom-anchor keeps newest
    // output visible as it streams in, and scrollToTop would freeze the
    // viewport at the start of the run.
    const sizeToContent = React.useCallback(() => {
        const term = termRef.current;
        if (term == null) return;
        const buf = term.buffer.active;
        let wantRows: number;
        if (interactive) {
            wantRows = buf.length;
        } else {
            const absY = buf.baseY + buf.cursorY;
            wantRows = buf.cursorX === 0 ? absY : absY + 1;
        }
        wantRows = Math.min(MaxXtermRows, Math.max(MinXtermRows, wantRows));
        if (term.rows !== wantRows) {
            try {
                term.resize(term.cols, wantRows);
            } catch {
                // ignore
            }
        }
        if (!interactive) {
            try {
                term.scrollToTop();
            } catch {
                // ignore
            }
        }
    }, [interactive]);

    React.useEffect(() => {
        const term = termRef.current;
        if (term == null) return;
        term.options.fontSize = fontSize;
        // Font size changes change char width → re-derive cols from the
        // container, but keep rows bound to the content (don't let fit.fit()
        // shrink rows to the container height).
        try {
            const proposed = fitRef.current?.proposeDimensions();
            if (proposed?.cols && proposed.cols > 0) {
                const newCols = Math.max(20, proposed.cols);
                if (newCols !== term.cols) {
                    term.resize(newCols, term.rows);
                    sizeToContent();
                }
            }
        } catch {
            // container not yet measured
        }
    }, [fontSize, sizeToContent]);

    React.useEffect(() => {
        const term = termRef.current;
        if (term == null) return;
        const written = writtenRef.current;
        if (bytes.length === written) {
            // No new bytes — still size in case interactivity flipped and we
            // need to re-measure with the current theme/cursor settings.
            sizeToContent();
            return;
        }
        if (bytes.length < written) {
            term.reset();
            if (!interactive) term.write(HideCursorSeq);
            writtenRef.current = 0;
            term.write(bytes, sizeToContent);
        } else if (written === 0) {
            term.write(bytes, sizeToContent);
        } else {
            term.write(bytes.subarray(written), sizeToContent);
        }
        writtenRef.current = bytes.length;
    }, [bytes, interactive, sizeToContent]);

    // Re-derive cols when the container resizes (panel drag, sidebar toggle).
    // xterm reflows wrapped lines at the new width; sizeToContent then picks
    // up the (possibly changed) row count.
    React.useEffect(() => {
        const host = containerRef.current;
        if (host == null) return;
        const ro = new ResizeObserver(() => {
            const term = termRef.current;
            const fit = fitRef.current;
            if (term == null || fit == null) return;
            try {
                const proposed = fit.proposeDimensions();
                if (!proposed?.cols || proposed.cols <= 0) return;
                const newCols = Math.max(20, proposed.cols);
                if (newCols === term.cols) return;
                term.resize(newCols, term.rows);
                sizeToContent();
            } catch {
                // ignore
            }
        });
        ro.observe(host);
        return () => ro.disconnect();
    }, [sizeToContent]);

    return <div className="termblocks-xterm" ref={containerRef} />;
};
XtermOutput.displayName = "XtermOutput";

function formatDuration(ms: number | null | undefined): string {
    if (ms == null) return "";
    if (ms < 1000) return `${ms}ms`;
    const s = ms / 1000;
    if (s < 60) return `${s.toFixed(s < 10 ? 2 : 1)}s`;
    const m = Math.floor(s / 60);
    const rem = Math.round(s - m * 60);
    return `${m}m${rem}s`;
}

function shortenCwd(cwd: string | null | undefined, home: string): string {
    if (!cwd) return "";
    if (home && cwd === home) return "~";
    if (home && cwd.startsWith(home + "/")) return "~" + cwd.slice(home.length);
    return cwd;
}

function stripAnsiForCopy(bytes: Uint8Array): string {
    if (bytes.length === 0) return "";
    const decoder = new TextDecoder("utf-8", { fatal: false });
    const raw = decoder.decode(bytes);
    // Remove OSC, CSI, and basic two-byte escape sequences so the clipboard
    // has plain text instead of ESC-riddled shell bytes.
    return raw
        .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, "")
        .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, "")
        .replace(/\r\n/g, "\n")
        .replace(/\r/g, "\n");
}

async function writeClipboard(text: string): Promise<void> {
    try {
        await navigator.clipboard.writeText(text);
    } catch (e) {
        console.warn("clipboard write failed", e);
    }
}

const TermBlockRow: React.FC<{
    block: CmdBlock;
    output: Uint8Array | undefined;
    model: TermBlocksViewModel;
    fallbackCwd: string;
    home: string;
    gitInfo: GitInfoResponse | null;
    selected: boolean;
    theme: any;
    fontSize: number;
}> = ({ block, output, model, fallbackCwd, home, gitInfo, selected, theme, fontSize }) => {
    const isDone = block.state === "done";
    const isRunning = block.state === "running";
    const isError = isDone && block.exitcode != null && block.exitcode !== 0;
    const hasOutput = output != null && hasMeaningfulOutput(output);
    const showXterm = hasOutput || isRunning;
    const cwd = shortenCwd(block.cwd ?? fallbackCwd, home);
    const duration = formatDuration(block.durationms);
    const branch = gitInfo?.isrepo ? gitInfo.branch : "";
    const hasDiff = gitInfo?.isrepo && ((gitInfo.changedfiles ?? 0) > 0);

    const openMenu = (e: React.MouseEvent) => {
        e.preventDefault();
        const cmd = block.cmd ?? "";
        const outputText = output ? stripAnsiForCopy(output) : "";
        const items: ContextMenuItem[] = [];
        if (cmd) {
            items.push({
                label: "Rerun command",
                click: () => model.rerunBlock(cmd),
            });
            items.push({ type: "separator" });
            items.push({ label: "Copy command", click: () => writeClipboard(cmd) });
        }
        if (outputText) {
            items.push({ label: "Copy output", click: () => writeClipboard(outputText) });
        }
        if (cmd || outputText) {
            items.push({
                label: "Copy block as Markdown",
                click: () => {
                    const md = [];
                    if (block.cwd) md.push(`> \`${shortenCwd(block.cwd, home)}\``);
                    if (cmd) md.push("```sh", "$ " + cmd, "```");
                    if (outputText) md.push("```", outputText.trimEnd(), "```");
                    writeClipboard(md.join("\n"));
                },
            });
        }
        if (items.length > 0) {
            items.push({ type: "separator" });
        }
        if (block.cwd) {
            items.push({
                label: "Copy working directory",
                sublabel: block.cwd,
                click: () => writeClipboard(block.cwd!),
            });
        }
        if (branch) {
            items.push({
                label: "Copy git branch",
                sublabel: branch,
                click: () => writeClipboard(branch),
            });
        }
        if (isDone || isError) {
            items.push({ type: "separator" });
            items.push({
                label: "Delete block from view",
                click: () => model.hideBlock(block.oid),
            });
        }
        if (items.length === 0) return;
        ContextMenuModel.getInstance().showContextMenu(items, e);
    };

    const onClick = (e: React.MouseEvent) => {
        // Ignore clicks that land on text selection so users can still copy
        // output without toggling block selection.
        const sel = window.getSelection();
        if (sel && sel.toString().length > 0) {
            return;
        }
        e.stopPropagation();
        model.selectBlock(block.oid);
    };

    return (
        <div
            className={cn(
                "termblocks-row group/row",
                `termblocks-row-${block.state}`,
                isError && "termblocks-row-error",
                !showXterm && "termblocks-row-compact",
                selected && "termblocks-row-selected"
            )}
            onContextMenu={openMenu}
            onClick={onClick}
        >
            <button
                type="button"
                className="termblocks-row-menu"
                aria-label="Block options"
                title="Block options"
                onClick={(e) => {
                    e.stopPropagation();
                    openMenu(e);
                }}
            >
                <i className="fa fa-solid fa-ellipsis-vertical" aria-hidden />
            </button>
            <div className="termblocks-row-meta">
                {cwd && <span className="termblocks-meta-cwd">{cwd}</span>}
                {branch && (
                    <span className="termblocks-meta-git">
                        git:(<span className="termblocks-meta-branch">{branch}</span>)
                    </span>
                )}
                {hasDiff && (
                    <span className="termblocks-meta-diff">
                        {gitInfo?.changedfiles}
                        {" • "}
                        <span className="termblocks-diff-add">+{gitInfo?.additions ?? 0}</span>{" "}
                        <span className="termblocks-diff-del">-{gitInfo?.deletions ?? 0}</span>
                    </span>
                )}
                {duration && <span className="termblocks-meta-dur">({duration})</span>}
                {isError && (
                    <span className="termblocks-meta-exit" title={`exit ${block.exitcode}`}>
                        ✕ exit {block.exitcode}
                    </span>
                )}
                {isRunning && <span className="termblocks-meta-running">running…</span>}
            </div>
            {block.cmd && <div className="termblocks-row-cmd">{block.cmd}</div>}
            {showXterm && (
                <XtermOutput
                    bytes={output ?? new Uint8Array()}
                    interactive={isRunning}
                    onData={isRunning ? (d) => model.sendBytes(d) : undefined}
                    onResize={isRunning ? (r, c) => model.sendResize(r, c) : undefined}
                    theme={theme}
                    fontSize={fontSize}
                />
            )}
        </div>
    );
};
TermBlockRow.displayName = "TermBlockRow";

const TermBlocksInput: React.FC<{ model: TermBlocksViewModel }> = ({ model }) => {
    const inputRef = model.inputRef;
    const history = useAtomValue(model.historyAtom);
    const historyIdxRef = React.useRef<number>(-1);
    const draftRef = React.useRef<string>("");
    const [value, setValue] = React.useState("");

    // The input is unmounted while a command runs (see TermBlocksView) and
    // remounted when the command finishes.  Auto-focus on mount so the user
    // can immediately type the next command without clicking.
    React.useEffect(() => {
        inputRef.current?.focus();
    }, []);

    // Permissions v2 routing: `:` prefix is gone. Plain Enter runs the
    // line as a shell command; Cmd+Enter (or Ctrl+Enter on non-mac)
    // sends it to the agent. Same input box, same value — only the
    // dispatch keystroke differs. `::` legacy escape removed because
    // there's no longer a `:` prefix to escape.
    const submitShell = () => {
        const line = value;
        if (line.length === 0) return;
        model.recordHistory(line);
        historyIdxRef.current = -1;
        draftRef.current = "";
        setValue("");
        model.submitInput(line);
    };

    const submitAgent = () => {
        const line = value;
        if (line.length === 0) return;
        globalStore.set(model.termAgentInput, line);
        setValue("");
        historyIdxRef.current = -1;
        draftRef.current = "";
        model.submitTermAgentPrompt();
    };

    // Auto-grow the textarea up to a cap. Resize on every value change
    // so multi-line agent prompts get visible vertical room without
    // requiring a manual resize handle. Cap at 8 lines (~160px) before
    // scrolling — beyond that the input column would dominate the view.
    React.useEffect(() => {
        const el = inputRef.current;
        if (!el) return;
        el.style.height = "auto";
        const max = 160;
        el.style.height = Math.min(el.scrollHeight, max) + "px";
    }, [value]);

    const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
        if (e.ctrlKey && e.key.toLowerCase() === "c" && !e.metaKey && !e.shiftKey && !e.altKey) {
            e.preventDefault();
            setValue("");
            historyIdxRef.current = -1;
            draftRef.current = "";
            model.sendInterrupt();
            return;
        }
        if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            // Cmd+Enter (mac) or Ctrl+Enter (cross-platform) → agent.
            // Plain Enter → shell. Shift+Enter falls through to the
            // textarea's native handler so the user can compose
            // multi-line prompts (especially useful for the agent).
            if (e.metaKey || e.ctrlKey) {
                submitAgent();
            } else {
                submitShell();
            }
            return;
        }
        if (e.key === "ArrowUp") {
            // Only navigate history when the caret is on the FIRST
            // line — otherwise let the textarea move the caret up
            // within the current prompt.
            const el = inputRef.current;
            if (el) {
                const before = value.slice(0, el.selectionStart ?? 0);
                if (before.includes("\n")) return;
            }
            if (history.length === 0) return;
            e.preventDefault();
            if (historyIdxRef.current === -1) {
                draftRef.current = value;
                historyIdxRef.current = history.length;
            }
            historyIdxRef.current = Math.max(0, historyIdxRef.current - 1);
            setValue(history[historyIdxRef.current] ?? "");
            return;
        }
        if (e.key === "ArrowDown") {
            // Mirror ArrowUp: only navigate history when the caret is
            // on the LAST line.
            const el = inputRef.current;
            if (el) {
                const after = value.slice(el.selectionStart ?? 0);
                if (after.includes("\n")) return;
            }
            if (history.length === 0 || historyIdxRef.current === -1) return;
            e.preventDefault();
            const next = historyIdxRef.current + 1;
            if (next >= history.length) {
                historyIdxRef.current = -1;
                setValue(draftRef.current);
                draftRef.current = "";
            } else {
                historyIdxRef.current = next;
                setValue(history[next]);
            }
            return;
        }
    };

    // Permissions v2: dropped the per-input ask/plan/do chip. The
    // posture pill above the input is the only mode-like affordance
    // now, and it shows posture (per-chat strictness), not mode.
    return (
        <div className="termblocks-input-row">
            <div className="termblocks-input-wrap">
                <textarea
                    ref={inputRef}
                    className="termblocks-input"
                    rows={1}
                    value={value}
                    spellCheck={false}
                    autoComplete="off"
                    placeholder="Enter to run · ⌘+Enter to ask · ⇧+Enter for newline"
                    onChange={(e) => {
                        setValue(e.target.value);
                        historyIdxRef.current = -1;
                    }}
                    onKeyDown={onKeyDown}
                />
            </div>
        </div>
    );
};
TermBlocksInput.displayName = "TermBlocksInput";

// extractFriendlyError tries to pull the human-readable bit out of a
// raw provider/error string. Many backends throw the full JSON body
// at us — e.g. OpenRouter rate-limit returns
// `API returned status 429: {"error":{"message":"Rate limit ..."}}`.
// Showing the whole blob in the UI is noisy; if we can find an
// `error.message` field we use that instead, falling back to the
// original string when parsing fails.
function extractFriendlyError(raw: string): string {
    const braceStart = raw.indexOf("{");
    if (braceStart < 0) return raw;
    try {
        const parsed = JSON.parse(raw.slice(braceStart));
        const inner = parsed?.error?.message ?? parsed?.message;
        if (typeof inner === "string" && inner.length > 0) {
            // Keep the leading status-prefix ("API returned status 429: ")
            // because it's actionable signal.
            const prefix = raw.slice(0, braceStart).trim();
            return prefix ? `${prefix} ${inner}` : inner;
        }
    } catch {
        // Not JSON — fall through to the raw string.
    }
    return raw;
}

const TermAgentNoticeBar: React.FC<{ model: TermBlocksViewModel }> = ({ model }) => {
    const error = useAtomValue(model.termAgentNotice);
    if (!error) return null;
    const isInfo = error.startsWith("Model switched to:");
    const display = isInfo ? error : extractFriendlyError(error);
    return (
        <div
            className={cn(
                "mx-3 my-1 rounded border px-2 py-1 text-xs break-words max-h-24 overflow-y-auto",
                isInfo
                    ? "border-zinc-700 text-zinc-400"
                    : "border-red-500/40 bg-red-500/5 text-red-300"
            )}
        >
            <div className="flex items-start gap-2">
                <div className="flex-1">{display}</div>
                <button
                    type="button"
                    onClick={() => model.setTermAgentNotice(null)}
                    className="shrink-0 text-[11px] text-zinc-500 hover:text-zinc-300 cursor-pointer"
                    title="Dismiss"
                >
                    ✕
                </button>
            </div>
        </div>
    );
};

// Posture cycle for the agent footer. Mirrors the term-agent.tsx
// overlay version. Order matches Claude Code's Shift+Tab convention
// (default → acceptEdits → bypass → default); "bench" is API-only and
// not in the cycle.
const TERMBLOCKS_POSTURE_CYCLE = ["default", "acceptEdits", "bypass"] as const;
function termblocksPostureLabel(p: string): string {
    switch (p) {
        case "default":
            return "Ask permissions";
        case "acceptEdits":
            return "Accept edits";
        case "bypass":
            return "Bypass permissions";
        default:
            return p;
    }
}
// Pill-style classes for the posture button — border + tinted bg per
// posture so it's visually consistent with the model pill while still
// communicating the "asking / accepting / bypass" gradient via color.
// All three share the same shape so the eye reads a row of pills, not
// a label-then-pill.
function termblocksPostureClass(p: string): string {
    switch (p) {
        case "default":
            return "border-zinc-700 bg-zinc-900/40 text-zinc-300 hover:bg-zinc-800 hover:border-zinc-600";
        case "acceptEdits":
            return "border-sky-500/40 bg-sky-500/10 text-sky-300 hover:bg-sky-500/20 hover:border-sky-500/60";
        case "bypass":
            return "border-amber-500/40 bg-amber-500/10 text-amber-300 hover:bg-amber-500/20 hover:border-amber-500/60";
        default:
            return "border-zinc-700 bg-zinc-900/40 text-zinc-500";
    }
}

// TermAgentModelPicker is the footer-anchored model switcher. Pops up
// above the footer (so it doesn't collide with the input) and lets the
// user search the provider's model list and pick one without leaving
// the chat. Selection writes to termAgentModelOverrideAtom — same
// state path as the `model xxx` shell command.
const TermAgentModelPicker = React.memo(({
    model,
    currentModelId,
    onClose,
}: {
    model: TermBlocksViewModel;
    currentModelId: string;
    onClose: () => void;
}) => {
    const settings = useAtomValue(atoms.settingsAtom);
    const [filter, setFilter] = React.useState("");
    const [models, setModels] = React.useState<ProviderModelInfo[]>([]);
    const [loading, setLoading] = React.useState(false);
    const [error, setError] = React.useState<string | null>(null);
    const [activeIdx, setActiveIdx] = React.useState(-1);
    const wrapRef = React.useRef<HTMLDivElement | null>(null);
    const listRef = React.useRef<HTMLDivElement | null>(null);

    React.useEffect(() => {
        let cancelled = false;
        (async () => {
            setLoading(true);
            setError(null);
            try {
                const apiType = (settings as any)?.["ai:apitype"] ?? "";
                const baseUrl = (settings as any)?.["ai:baseurl"] ?? "";
                const tokenSecretName = (settings as any)?.["ai:apitokensecretname"] ?? "";
                let apiToken = (settings as any)?.["ai:apitoken"] ?? "";
                if (!apiToken && tokenSecretName) {
                    try {
                        const secrets = await RpcApi.GetSecretsCommand(TabRpcClient, [tokenSecretName]);
                        apiToken = secrets?.[tokenSecretName] ?? "";
                    } catch {
                        // keep apiToken empty; some providers (e.g. OpenRouter) accept anonymous /models
                    }
                }
                const result = await RpcApi.ListProviderModelsCommand(TabRpcClient, {
                    apitype: apiType,
                    baseurl: baseUrl,
                    apitoken: apiToken,
                });
                if (!cancelled) setModels(result.models ?? []);
            } catch (e: any) {
                if (!cancelled) {
                    setError(e?.message ?? "failed to fetch models");
                    setModels([]);
                }
            } finally {
                if (!cancelled) setLoading(false);
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [settings]);

    // Same tier ranking as the AI Provider settings combobox so the
    // two pickers feel identical when both open on the same provider.
    const filtered = React.useMemo(() => {
        if (!filter) return models;
        const q = filter.toLowerCase();
        const scored: { m: ProviderModelInfo; tier: number; idx: number }[] = [];
        models.forEach((m, idx) => {
            const id = m.id.toLowerCase();
            const name = (m.name ?? "").toLowerCase();
            const desc = (m.description ?? "").toLowerCase();
            let tier = Infinity;
            if (id === q) tier = 0;
            else if (id.startsWith(q)) tier = 1;
            else if (id.includes(q)) tier = 2;
            else if (name.startsWith(q)) tier = 3;
            else if (name.includes(q)) tier = 4;
            else if (desc.includes(q)) tier = 5;
            if (tier !== Infinity) scored.push({ m, tier, idx });
        });
        scored.sort((a, b) => a.tier - b.tier || a.idx - b.idx);
        return scored.map((s) => s.m);
    }, [filter, models]);

    React.useEffect(() => {
        // Pre-select the current model on first paint so Enter
        // re-affirms the existing choice instead of jumping to top.
        setActiveIdx(filtered.findIndex((m) => m.id === currentModelId));
    }, [models, currentModelId]); // eslint-disable-line react-hooks/exhaustive-deps

    React.useEffect(() => {
        if (activeIdx < 0) return;
        const el = listRef.current?.querySelector<HTMLElement>(`[data-mp-idx="${activeIdx}"]`);
        if (el) el.scrollIntoView({ block: "nearest" });
    }, [activeIdx]);

    React.useEffect(() => {
        const onDocMouseDown = (e: MouseEvent) => {
            const wrap = wrapRef.current;
            if (wrap && !wrap.contains(e.target as Node)) {
                onClose();
            }
        };
        document.addEventListener("mousedown", onDocMouseDown, true);
        return () => document.removeEventListener("mousedown", onDocMouseDown, true);
    }, [onClose]);

    const pick = (id: string) => {
        globalStore.set(model.termAgentModelOverrideAtom, id);
        // New chatId per provider/model switch — same as the legacy
        // `model xxx` shell command. Forces a fresh conversation with
        // the new model so context isn't mid-thought across families.
        globalStore.set(model.termAgentChatId, crypto.randomUUID());
        onClose();
    };

    const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === "ArrowDown") {
            e.preventDefault();
            setActiveIdx((i) => Math.min(filtered.length - 1, i + 1));
        } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActiveIdx((i) => Math.max(0, i - 1));
        } else if (e.key === "Enter") {
            if (activeIdx >= 0 && filtered[activeIdx]) {
                e.preventDefault();
                pick(filtered[activeIdx].id);
            } else {
                onClose();
            }
        } else if (e.key === "Escape") {
            e.preventDefault();
            onClose();
        }
    };

    return (
        <div
            ref={wrapRef}
            className="absolute bottom-full right-0 mb-1 w-[360px] max-w-[calc(100vw-2rem)] max-h-72 z-20 rounded-lg border border-zinc-700 bg-zinc-900 shadow-xl flex flex-col text-[12px]"
        >
            <input
                type="text"
                autoFocus
                placeholder="Search models…"
                value={filter}
                onChange={(e) => {
                    setFilter(e.target.value);
                    setActiveIdx(-1);
                }}
                onKeyDown={onKey}
                className="px-3 py-2 bg-transparent border-b border-zinc-800 text-zinc-200 focus:outline-none"
                spellCheck={false}
                autoComplete="off"
            />
            <div ref={listRef} className="flex-1 overflow-y-auto">
                {loading && (
                    <div className="px-3 py-3 text-zinc-400 flex items-center gap-2">
                        <i className="fa-sharp fa-solid fa-spinner fa-spin text-xs" />
                        Loading models…
                    </div>
                )}
                {error && !loading && <div className="px-3 py-3 text-red-400">{error}</div>}
                {!loading && !error && filtered.length === 0 && (
                    <div className="px-3 py-3 text-zinc-500">
                        {models.length === 0 ? "No models returned." : "No matches."}
                    </div>
                )}
                {!loading &&
                    filtered.map((m, idx) => (
                        <button
                            key={m.id}
                            type="button"
                            data-mp-idx={idx}
                            onMouseDown={(e) => e.preventDefault()}
                            onClick={() => pick(m.id)}
                            onMouseEnter={() => setActiveIdx(idx)}
                            className={cn(
                                "w-full text-left px-3 py-2 cursor-pointer border-b border-zinc-800 last:border-b-0 transition-colors",
                                idx === activeIdx ? "bg-zinc-800" : "hover:bg-zinc-800/60",
                                m.id === currentModelId && "ring-1 ring-inset ring-[var(--color-accent)]/40"
                            )}
                        >
                            <div className="font-mono text-[11px] text-zinc-200 truncate">{m.id}</div>
                            {(m.name || m.context > 0) && (
                                <div className="text-[10px] text-zinc-500 mt-0.5 flex items-center gap-2">
                                    {m.name && <span className="truncate">{m.name}</span>}
                                    {m.context > 0 && (
                                        <span className="ml-auto whitespace-nowrap">
                                            {Math.round(m.context / 1000)}K ctx
                                        </span>
                                    )}
                                </div>
                            )}
                        </button>
                    ))}
            </div>
        </div>
    );
});
TermAgentModelPicker.displayName = "TermAgentModelPicker";

// TermAgentPostureMenu pops up over the posture pill and lists all
// permission modes. Clicking a row sets that posture; number keys 1/2/3
// select directly while the menu is open. Mirrors the reference
// design in the user-facing docs (Mode header + numeric shortcuts).
const TermAgentPostureMenu = React.memo(({
    model,
    currentPosture,
    onClose,
}: {
    model: TermBlocksViewModel;
    currentPosture: string;
    onClose: () => void;
}) => {
    const wrapRef = React.useRef<HTMLDivElement | null>(null);

    const pick = (p: string) => {
        globalStore.set(model.termAgentPosture, p);
        onClose();
    };

    React.useEffect(() => {
        const onDocMouseDown = (e: MouseEvent) => {
            const wrap = wrapRef.current;
            if (wrap && !wrap.contains(e.target as Node)) {
                onClose();
            }
        };
        const onKey = (e: KeyboardEvent) => {
            if (e.key === "Escape") {
                e.preventDefault();
                onClose();
                return;
            }
            // Number keys 1/2/3 select directly. The menu is the only
            // visible affordance while open, so it's safe to claim
            // these keys without colliding with text input.
            const idx = TERMBLOCKS_POSTURE_CYCLE.findIndex((_, i) => String(i + 1) === e.key);
            if (idx >= 0) {
                e.preventDefault();
                pick(TERMBLOCKS_POSTURE_CYCLE[idx]);
            }
        };
        document.addEventListener("mousedown", onDocMouseDown, true);
        document.addEventListener("keydown", onKey);
        return () => {
            document.removeEventListener("mousedown", onDocMouseDown, true);
            document.removeEventListener("keydown", onKey);
        };
    }, [onClose]); // eslint-disable-line react-hooks/exhaustive-deps

    return (
        <div
            ref={wrapRef}
            className="absolute bottom-full left-0 mb-1 w-[240px] z-20 rounded-lg border border-zinc-700 bg-zinc-900 shadow-xl py-1 text-[12px]"
        >
            <div className="px-3 py-1.5 text-[11px] text-zinc-500 border-b border-zinc-800 mb-1">Mode</div>
            {TERMBLOCKS_POSTURE_CYCLE.map((p, idx) => {
                const isCurrent = p === currentPosture;
                return (
                    <button
                        key={p}
                        type="button"
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => pick(p)}
                        className={cn(
                            "w-full flex items-center px-3 py-1.5 text-zinc-200 hover:bg-zinc-800 transition-colors cursor-pointer",
                            isCurrent && "text-zinc-100"
                        )}
                    >
                        <span className="flex-1 text-left">{termblocksPostureLabel(p)}</span>
                        {isCurrent && <i className="fa-sharp fa-solid fa-check text-[10px] text-zinc-400 mr-2" />}
                        <span className="text-[10px] text-zinc-500 tabular-nums">{idx + 1}</span>
                    </button>
                );
            })}
        </div>
    );
});
TermAgentPostureMenu.displayName = "TermAgentPostureMenu";

// TermAgentFooter is the bottom info strip — a place for ambient
// agent state that the user wants to glance at, not interact with
// often. Today it shows posture + active model name; future agent
// info (token usage, step count, active rule preset, etc.) can join
// the same row separated by `·`. Posture and model name are both
// click-interactive (popover menus).
const TermAgentFooter: React.FC<{ model: TermBlocksViewModel }> = ({ model }) => {
    const posture = useAtomValue(model.termAgentPosture);
    const settingsModel = useAtomValue(getSettingsKeyAtom("ai:model"));
    const override = useAtomValue(model.termAgentModelOverrideAtom);
    const chatStatus = useAtomValue(model.termAgentChatStatusAtom);
    const modelName = (override || (settingsModel as string) || "default") as string;
    const [pickerOpen, setPickerOpen] = React.useState(false);
    const [postureMenuOpen, setPostureMenuOpen] = React.useState(false);
    const isActive = chatStatus === "streaming" || chatStatus === "submitted";
    const stopAgent = () => {
        model.termAgentStop?.();
    };
    // Esc anywhere stops a running agent — the natural "get me out"
    // shortcut. Only bind while active so we don't intercept Esc
    // for other modal closers (model picker has its own Esc handling).
    React.useEffect(() => {
        if (!isActive) return;
        const onKey = (e: KeyboardEvent) => {
            if (e.key === "Escape" && !pickerOpen) {
                stopAgent();
            }
        };
        window.addEventListener("keydown", onKey);
        return () => window.removeEventListener("keydown", onKey);
    }, [isActive, pickerOpen]); // eslint-disable-line react-hooks/exhaustive-deps
    return (
        <div className="px-3 py-1.5 text-[11px] text-zinc-600 flex items-center gap-2 relative">
            <div className="relative">
                <button
                    type="button"
                    onClick={() => setPostureMenuOpen((v) => !v)}
                    title="Permission mode — click to choose"
                    className={cn(
                        "px-2.5 py-1 rounded-md border text-[11px] transition-colors cursor-pointer focus:outline-none",
                        termblocksPostureClass(posture)
                    )}
                >
                    {termblocksPostureLabel(posture)}
                </button>
                {postureMenuOpen && (
                    <TermAgentPostureMenu
                        model={model}
                        currentPosture={posture}
                        onClose={() => setPostureMenuOpen(false)}
                    />
                )}
            </div>
            {isActive && (
                <button
                    type="button"
                    onClick={stopAgent}
                    title="Stop the agent (Esc)"
                    className="px-2 py-0.5 rounded text-[11px] bg-red-500/15 hover:bg-red-500/25 text-red-300 border border-red-500/30 transition-colors cursor-pointer flex items-center gap-1 focus:outline-none"
                >
                    <i className="fa-sharp fa-solid fa-stop text-[9px]" />
                    Stop
                </button>
            )}
            {/* Right cluster: context / model / attach — same order as
                the reference design. Posture stays on the left as the
                only non-input ambient state. */}
            <div className="ml-auto flex items-center gap-2">
                {/* Context-remaining indicator placeholder. Wires to
                    data-usage SSE event later — static ring for now. */}
                <div
                    title="Context remaining (coming soon)"
                    className="w-4 h-4 rounded-full border border-zinc-700"
                />
                <div className="relative">
                    <button
                        type="button"
                        onClick={() => setPickerOpen((v) => !v)}
                        title="Click to switch model"
                        className="px-2.5 py-1 rounded-md border border-zinc-700 bg-zinc-900/40 text-zinc-200 hover:bg-zinc-800 hover:border-zinc-600 transition-colors cursor-pointer truncate max-w-[260px] font-mono text-[11px] focus:outline-none"
                    >
                        {modelName}
                    </button>
                    {pickerOpen && (
                        <TermAgentModelPicker
                            model={model}
                            currentModelId={modelName}
                            onClose={() => setPickerOpen(false)}
                        />
                    )}
                </div>
                {/* Attach file placeholder. No file input wired yet. */}
                <button
                    type="button"
                    title="Attach file (coming soon)"
                    disabled
                    className="w-6 h-6 rounded-md border border-zinc-700 bg-zinc-900/40 text-zinc-500 flex items-center justify-center disabled:opacity-50 focus:outline-none"
                >
                    <i className="fa-sharp fa-solid fa-plus text-[10px]" />
                </button>
            </div>
        </div>
    );
};

const InlineAgentUserMsg = React.memo(({ text }: { text: string; mode?: string }) => {
    // Permissions v2: dropped the per-message mode tag — Mode the
    // axis is gone (everything runs in "do"); the user's posture
    // lives on the chat-level pill, not on each message.
    return (
        <div className="mx-3 my-2 flex justify-end">
            <div className="max-w-[min(100%,44rem)] rounded-lg bg-zinc-700/70 px-3 py-2 text-sm text-white">
                <div className="whitespace-pre-wrap break-words">{text}</div>
            </div>
        </div>
    );
});

// InlineAgentError renders a mid-run model/network error as part of
// the conversation timeline. Visually shaped like InlineAgentResponse
// (same width, same lozenge layout, no "Error" header) — the error
// IS the assistant's response in this turn; only the tint signals
// it's a failure mode rather than normal output. Tries to extract
// the human-readable bit from a JSON-wrapped provider response so
// "Rate limit exceeded: ..." shows instead of a 200-line blob.
const InlineAgentError = React.memo(({ text }: { text: string }) => {
    const display = extractFriendlyError(text);
    return (
        <div className="mx-3 my-2 space-y-2 rounded-lg border border-red-500/30 bg-red-500/5 px-3 py-2 text-sm text-red-300">
            <div className="whitespace-pre-wrap break-words">{display}</div>
        </div>
    );
});
InlineAgentError.displayName = "InlineAgentError";
InlineAgentUserMsg.displayName = "InlineAgentUserMsg";

// AgentThinkingBubble shows up between submit and the first model chunk.
// Without this, timeline goes silent for ~1-3s after Cmd+Enter and the
// user can't tell whether the request was even received. Three-dot
// pulse mirrors what Claude Code / chat UIs use; intentionally low-key
// so the eye doesn't stick on it.
const AgentThinkingBubble = React.memo(() => {
    return (
        <div className="mx-3 my-2 flex items-center gap-2 text-zinc-500">
            <span className="inline-flex items-center gap-1">
                <span className="w-1.5 h-1.5 rounded-full bg-zinc-500 animate-pulse" style={{ animationDelay: "0ms" }} />
                <span className="w-1.5 h-1.5 rounded-full bg-zinc-500 animate-pulse" style={{ animationDelay: "150ms" }} />
                <span className="w-1.5 h-1.5 rounded-full bg-zinc-500 animate-pulse" style={{ animationDelay: "300ms" }} />
            </span>
            <span className="text-xs">Thinking…</span>
        </div>
    );
});
AgentThinkingBubble.displayName = "AgentThinkingBubble";

const InlineAgentResponse = React.memo(({ message, streaming }: { message: WaveUIMessage; streaming: boolean }) => {
    const visibleParts = (message.parts ?? []).filter(
        (part) => part.type === "text" || part.type === "reasoning" || part.type === "data-tooluse" || part.type === "data-toolprogress"
    );
    if (visibleParts.length === 0 && streaming) {
        return (
            <div className="mx-3 my-2 rounded-lg px-3 py-2 text-sm text-zinc-400">Thinking...</div>
        );
    }
    return (
        <div className="mx-3 my-2 space-y-2 rounded-lg border border-zinc-800/50 bg-zinc-900/30 px-3 py-2">
            {visibleParts.map((part, idx) => (
                <TermAgentMessagePartView key={`${part.type}:${idx}`} part={part} isStreaming={streaming} />
            ))}
        </div>
    );
});
InlineAgentResponse.displayName = "InlineAgentResponse";

export const TermBlocksView: React.FC<ViewComponentProps<TermBlocksViewModel>> = ({ model }) => {
    const blocks = useAtomValue(model.blocksAtom);
    const outputs = useAtomValue(model.outputCacheAtom);
    const loading = useAtomValue(model.loadingAtom);
    const error = useAtomValue(model.errorAtom);
    const altOID = useAtomValue(model.altScreenOIDAtom);
    const blockMetaCwd = useAtomValue(model.blockCwdAtom);
    const home = useAtomValue(model.homeAtom);
    const gitInfo = useAtomValue(model.gitInfoAtom);
    const selectedOid = useAtomValue(model.selectedOidAtom);
    const fullConfig = useAtomValue(atoms.fullConfigAtom);
    const themeName = useAtomValue(model.termThemeNameAtom);
    const transparency = useAtomValue(model.termTransparencyAtom);
    const fontSize = useAtomValue(model.termFontSizeAtom);
    const [termTheme] = React.useMemo(
        () => computeTheme(fullConfig, themeName, transparency),
        [fullConfig, themeName, transparency]
    );
    const scrollRef = React.useRef<HTMLDivElement>(null);
    // Sticky-scroll: track whether the viewport is currently at the
    // bottom. When the user scrolls up to read history, this flips to
    // false and we stop auto-scrolling. When they scroll back to the
    // bottom, it flips true again and follow-mode resumes. Initial
    // true so first paint lands at the bottom.
    const isAtBottomRef = React.useRef(true);
    const chatStatus = useAtomValue(model.termAgentChatStatusAtom);

    React.useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key === "Escape" && selectedOid) {
                model.clearSelection();
            }
        };
        window.addEventListener("keydown", onKey);
        return () => window.removeEventListener("keydown", onKey);
    }, [model, selectedOid]);

    React.useEffect(() => {
        const el = scrollRef.current;
        if (!el) return;
        const onScroll = () => {
            // 32px slack — small toolbar/padding shouldn't break the
            // "at bottom" detection.
            const threshold = 32;
            isAtBottomRef.current =
                el.scrollHeight - el.scrollTop - el.clientHeight <= threshold;
        };
        el.addEventListener("scroll", onScroll, { passive: true });
        return () => el.removeEventListener("scroll", onScroll);
    }, []);

    // Refresh git info the moment the block's cwd changes (cd into a repo,
    // cd out of it).  The 4s poll still covers branch switch / file edits.
    React.useEffect(() => {
        if (blockMetaCwd !== "" && blockMetaCwd !== model.lastGitCwd) {
            model.refreshGitInfo();
        }
    }, [blockMetaCwd, model]);

    // Only render rows that actually represent a command — a bare "prompt"
    // state is the transient anchor the next OSC C will attach to, with no
    // user-meaningful content yet.
    const minVisibleSeq = useAtomValue(model.minVisibleSeqAtom);
    const hiddenOids = useAtomValue(model.hiddenOidsAtom);
    const visibleBlocks = React.useMemo(
        () => blocks.filter((b) => b.state !== "prompt" && b.seq > minVisibleSeq && !hiddenOids.has(b.oid)),
        [blocks, minVisibleSeq, hiddenOids]
    );
    const timeline = useAtomValue(model.timelineAtom);
    const lastOid = visibleBlocks.length > 0 ? visibleBlocks[visibleBlocks.length - 1].oid : "";
    const lastOutputLen = lastOid && outputs[lastOid] != null ? outputs[lastOid].length : 0;
    const inAltScreen = altOID !== "";
    const runningBlock = React.useMemo(() => blocks.find((b) => b.state === "running") ?? null, [blocks]);

    // Scroll to the bottom whenever the last visible block changes OR
    // its output bytes arrive — but only when the user is already
    // parked at the bottom. If they scrolled up to read earlier
    // history, leave their viewport alone. xterm lays out across a
    // couple of frames, so we trigger deferred scrolls in addition to
    // the immediate one.
    React.useEffect(() => {
        if (inAltScreen) {
            return;
        }
        if (!isAtBottomRef.current) {
            return;
        }
        const el = scrollRef.current;
        if (el == null) return;
        const toBottom = () => {
            el.scrollTop = el.scrollHeight;
        };
        toBottom();
        const raf1 = requestAnimationFrame(toBottom);
        const raf2 = requestAnimationFrame(() => requestAnimationFrame(toBottom));
        const late = setTimeout(toBottom, 120);
        return () => {
            cancelAnimationFrame(raf1);
            cancelAnimationFrame(raf2);
            clearTimeout(late);
        };
        // `timeline` (not just .length) so streaming text-deltas — which
        // mutate parts inside an existing entry without changing array
        // length — still fire the auto-scroll. Re-rendering at every
        // sync is fine: the work below is just two scrollTop sets when
        // already at bottom, otherwise an early return.
    }, [lastOid, lastOutputLen, visibleBlocks.length, inAltScreen, timeline, chatStatus]);

    // Alt-screen mode: a TUI (less/vim/top/…) took over the PTY, so we show
    // the running block's buffer in a single full-viewport xterm with stdin
    // enabled.  Keystrokes go straight to the PTY via onData.
    if (inAltScreen) {
        const running = blocks.find((b) => b.state === "running") ?? blocks[blocks.length - 1];
        const bytes = running != null ? outputs[running.oid] ?? new Uint8Array() : new Uint8Array();
        console.log("[cd-bug] TermBlocksView render alt-screen", {
            blockId: model.blockId,
            altOID,
            runningOID: running?.oid,
            runningCmd: running?.cmd,
            bytesLen: bytes.length,
            blocksCount: blocks.length,
        });
        return (
            <div className="termblocks-root">
                <div className="termblocks-altscreen-wrap">
                    <AltScreenXterm
                        bytes={bytes}
                        onData={(s) => model.sendBytes(s)}
                        onResize={(r, c) => model.sendResize(r, c)}
                        theme={termTheme}
                        fontSize={fontSize}
                    />
                </div>
            </div>
        );
    }

    // Subscribe reactively — termAgentChatId is reset on
    // clearTermAgentSession() and on the start of a new prompt, so a
    // non-reactive read would freeze the context against the previous
    // chat and stamp persisted "remember this" rules with a stale id.
    const approvalChatId = useAtomValue(model.termAgentChatId);
    const approvalCtx = React.useMemo(
        () => ({
            chatId: approvalChatId,
            getCwd: () => blockMetaCwd ?? undefined,
            tabId: model.tabModel.tabId,
            anchorBlockId: model.blockId,
        }),
        [model, blockMetaCwd, approvalChatId]
    );

    return (
        <TermAgentApprovalContext.Provider value={approvalCtx}>
        <div className="termblocks-root">
            <div
                className="termblocks-scroll"
                ref={scrollRef}
                onClick={(e) => {
                    if (e.target === e.currentTarget) {
                        model.clearSelection();
                    }
                }}
            >
                {error && <div className="termblocks-empty termblocks-error">Error: {error}</div>}
                {!error && loading && visibleBlocks.length === 0 && (
                    <div className="termblocks-empty">Loading…</div>
                )}
                {timeline.length > 0 && (
                    <div
                        className="termblocks-container"
                        onClick={(e) => {
                            if (e.target === e.currentTarget) {
                                model.clearSelection();
                            }
                        }}
                    >
                        {timeline.map((entry) => {
                            if (entry.type === "cmd") {
                                return (
                                    <TermBlockRow
                                        key={entry.block.oid}
                                        block={entry.block}
                                        output={outputs[entry.block.oid]}
                                        model={model}
                                        fallbackCwd={blockMetaCwd}
                                        home={home}
                                        gitInfo={gitInfo}
                                        selected={entry.block.oid === selectedOid}
                                        theme={termTheme}
                                        fontSize={fontSize}
                                    />
                                );
                            }
                            if (entry.type === "agent-user") {
                                return (
                                    <InlineAgentUserMsg
                                        key={entry.id}
                                        text={entry.text}
                                        mode={entry.mode}
                                    />
                                );
                            }
                            if (entry.type === "agent-response") {
                                return (
                                    <InlineAgentResponse
                                        key={entry.message.id}
                                        message={entry.message}
                                        streaming={entry.streaming}
                                    />
                                );
                            }
                            if (entry.type === "agent-error") {
                                return <InlineAgentError key={entry.id} text={entry.text} />;
                            }
                            return null;
                        })}
                        {chatStatus === "submitted" &&
                            timeline.length > 0 &&
                            timeline[timeline.length - 1].type === "agent-user" && (
                                <AgentThinkingBubble />
                            )}
                    </div>
                )}
            </div>
            <div className="termblocks-input-card">
                <TermBlocksStatusBar cwd={blockMetaCwd} home={home} gitInfo={gitInfo} blockId={model.blockId} />
                <TermAgentNoticeBar model={model} />
                {runningBlock == null && <TermBlocksInput model={model} />}
                <TermAgentFooter model={model} />
            </div>
            <TermAgentChatProvider model={model} />
        </div>
        </TermAgentApprovalContext.Provider>
    );
};
TermBlocksView.displayName = "TermBlocksView";

// ---- Chip Popover helper ----
// Renders via ReactDOM.createPortal at document.body with position:fixed so
// no parent overflow:hidden or z-index can obscure the dropdown.
// Position is calculated from the trigger's getBoundingClientRect() and the
// dropdown is anchored above the trigger (bottom of dropdown = top of trigger).
const ChipPopover: React.FC<{
    trigger: React.ReactNode;
    children: (close: () => void) => React.ReactNode;
}> = ({ trigger, children }) => {
    const [open, setOpen] = React.useState(false);
    const [rect, setRect] = React.useState<DOMRect | null>(null);
    const triggerRef = React.useRef<HTMLSpanElement>(null);
    const dropdownRef = React.useRef<HTMLDivElement>(null);

    const toggle = () => {
        if (!open && triggerRef.current) {
            setRect(triggerRef.current.getBoundingClientRect());
        }
        setOpen((v) => !v);
    };

    React.useEffect(() => {
        if (!open) return;
        const handler = (e: MouseEvent) => {
            const target = e.target as Node;
            if (
                dropdownRef.current && !dropdownRef.current.contains(target) &&
                triggerRef.current && !triggerRef.current.contains(target)
            ) {
                setOpen(false);
            }
        };
        document.addEventListener("mousedown", handler);
        return () => document.removeEventListener("mousedown", handler);
    }, [open]);

    const dropdownStyle: React.CSSProperties = rect
        ? {
            position: "fixed",
            left: rect.left,
            bottom: window.innerHeight - rect.top + 6,
            zIndex: 99999,
            minWidth: Math.max(rect.width, 220),
        }
        : { display: "none" };

    return (
        <>
            <span ref={triggerRef} onClick={toggle}>{trigger}</span>
            {open && rect && ReactDOM.createPortal(
                <div ref={dropdownRef} style={dropdownStyle}>
                    {children(() => setOpen(false))}
                </div>,
                document.body
            )}
        </>
    );
};
ChipPopover.displayName = "ChipPopover";

// ---- cwd picker dropdown content ----
const CwdPickerContent: React.FC<{ cwd: string; blockId: string; close: () => void }> = ({ cwd, blockId, close }) => {
    const [entries, setEntries] = React.useState<FileInfo[]>([]);
    const [search, setSearch] = React.useState("");
    const searchRef = React.useRef<HTMLInputElement>(null);
    const [focused, setFocused] = React.useState(0);

    React.useEffect(() => {
        searchRef.current?.focus();
        let cancelled = false;
        const load = async () => {
            const list: FileInfo[] = [];
            const stream = RpcApi.FileListStreamCommand(TabRpcClient, { path: formatRemoteUri(cwd, "local") }, null);
            for await (const chunk of stream) {
                if (cancelled) return;
                if (chunk?.fileinfo) list.push(...chunk.fileinfo.filter((f) => f.isdir));
            }
            if (cancelled) return;
            list.sort((a, b) => (a.name ?? "").localeCompare(b.name ?? ""));
            setEntries(list);
        };
        load().catch(() => {});
        return () => { cancelled = true; };
    }, [cwd]);

    const navigate = (path: string) => {
        const cmd = `cd ${shellQuote([path])}\n`;
        RpcApi.ControllerInputCommand(TabRpcClient, { blockid: blockId, inputdata64: stringToBase64(cmd) });
        close();
    };

    const filtered = React.useMemo(() => {
        const s = search.toLowerCase();
        return s ? entries.filter((e) => (e.name ?? "").toLowerCase().includes(s)) : entries;
    }, [entries, search]);

    const rows = React.useMemo(() => [
        ...(search ? [] : [{ path: cwd + "/..", label: ".. (Parent Directory)", icon: "fa-solid fa-arrow-up" }]),
        ...filtered.map((e) => ({ path: e.path, label: e.name ?? e.path, icon: "fa-regular fa-folder" })),
    ], [cwd, search, filtered]);

    const clampFocus = (n: number) => Math.max(0, Math.min(n, rows.length - 1));

    return (
        <div className="tb-chip-dropdown">
            <div className="tb-chip-dropdown-search">
                <i className="fa-solid fa-magnifying-glass" style={{ opacity: 0.4, fontSize: 11 }} />
                <input
                    ref={searchRef}
                    value={search}
                    onChange={(e) => { setSearch(e.target.value); setFocused(0); }}
                    placeholder="Search directories..."
                    spellCheck={false}
                    className="tb-chip-dropdown-input"
                    onKeyDown={(e) => {
                        if (e.key === "ArrowDown") { e.preventDefault(); setFocused((f) => clampFocus(f + 1)); }
                        if (e.key === "ArrowUp") { e.preventDefault(); setFocused((f) => clampFocus(f - 1)); }
                        if (e.key === "Enter" && rows[focused]) navigate(rows[focused].path);
                        if (e.key === "Escape") close();
                    }}
                />
            </div>
            <div className="tb-chip-dropdown-list">
                {rows.map((r, i) => (
                    <div
                        key={r.path}
                        className={cn("tb-chip-dropdown-row", i === focused && "tb-chip-dropdown-row-active")}
                        onMouseEnter={() => setFocused(i)}
                        onClick={() => navigate(r.path)}
                    >
                        <i className={`${r.icon} tb-chip-dropdown-row-icon`} aria-hidden />
                        {r.label}
                    </div>
                ))}
            </div>
        </div>
    );
};
CwdPickerContent.displayName = "CwdPickerContent";

// ---- git branch picker ----
const BranchPickerContent: React.FC<{ cwd: string; currentBranch: string; blockId: string; close: () => void }> = ({
    cwd, currentBranch, blockId, close,
}) => {
    const [branches, setBranches] = React.useState<string[]>([]);
    const [focused, setFocused] = React.useState(0);
    const searchRef = React.useRef<HTMLInputElement>(null);
    const [search, setSearch] = React.useState("");

    React.useEffect(() => {
        searchRef.current?.focus();
        let cancelled = false;
        const load = async () => {
            const res = await RpcApi.RunLocalCmdCommand(TabRpcClient, {
                cmd: "git",
                args: ["branch", "--format=%(refname:short)"],
                cwd,
            });
            if (cancelled) return;
            const list = res.stdout.split("\n").map((s) => s.trim()).filter(Boolean);
            list.sort((a, b) => {
                if (a === currentBranch) return -1;
                if (b === currentBranch) return 1;
                return a.localeCompare(b);
            });
            setBranches(list);
        };
        load().catch(() => {});
        return () => { cancelled = true; };
    }, [cwd, currentBranch]);

    const filtered = search ? branches.filter((b) => b.toLowerCase().includes(search.toLowerCase())) : branches;
    const clamp = (n: number) => Math.max(0, Math.min(n, filtered.length - 1));

    const checkout = (branch: string) => {
        const cmd = `git checkout ${shellQuote([branch])}\n`;
        RpcApi.ControllerInputCommand(TabRpcClient, { blockid: blockId, inputdata64: stringToBase64(cmd) });
        close();
    };

    return (
        <div className="tb-chip-dropdown">
            <div className="tb-chip-dropdown-search">
                <i className="fa-solid fa-magnifying-glass" style={{ opacity: 0.4, fontSize: 11 }} />
                <input
                    ref={searchRef}
                    value={search}
                    onChange={(e) => { setSearch(e.target.value); setFocused(0); }}
                    placeholder="Search branches..."
                    spellCheck={false}
                    className="tb-chip-dropdown-input"
                    onKeyDown={(e) => {
                        if (e.key === "ArrowDown") { e.preventDefault(); setFocused((f) => clamp(f + 1)); }
                        if (e.key === "ArrowUp") { e.preventDefault(); setFocused((f) => clamp(f - 1)); }
                        if (e.key === "Enter" && filtered[focused]) checkout(filtered[focused]);
                        if (e.key === "Escape") close();
                    }}
                />
            </div>
            <div className="tb-chip-dropdown-list">
                {filtered.map((b, i) => (
                    <div
                        key={b}
                        className={cn("tb-chip-dropdown-row", i === focused && "tb-chip-dropdown-row-active")}
                        onMouseEnter={() => setFocused(i)}
                        onClick={() => checkout(b)}
                    >
                        {b === currentBranch
                            ? <i className="fa-solid fa-check tb-chip-dropdown-row-icon" aria-hidden />
                            : <i className="fa-solid fa-code-branch tb-chip-dropdown-row-icon" aria-hidden />
                        }
                        {b}
                    </div>
                ))}
            </div>
        </div>
    );
};
BranchPickerContent.displayName = "BranchPickerContent";

// ---- Updated status bar with clickable chips ----
const TermBlocksStatusBar: React.FC<{
    cwd: string;
    home: string;
    gitInfo: GitInfoResponse | null;
    blockId: string;
}> = ({ cwd, home, gitInfo, blockId }) => {
    const shortCwd = shortenCwd(cwd, home);
    const hasDiff = gitInfo?.isrepo && (gitInfo.changedfiles ?? 0) > 0;
    if (!shortCwd && !gitInfo?.isrepo) return null;

    return (
        <div className="termblocks-statusbar">
            {shortCwd && (
                <ChipPopover trigger={
                    <span className="termblocks-chip termblocks-chip-clickable" title={cwd}>
                        <FolderIcon size={12} className="termblocks-chip-icon" aria-hidden />
                        {shortCwd}
                    </span>
                }>
                    {(close) => <CwdPickerContent cwd={cwd} blockId={blockId} close={close} />}
                </ChipPopover>
            )}
            {gitInfo?.isrepo && gitInfo.branch && (
                <ChipPopover trigger={
                    <span className="termblocks-chip termblocks-chip-clickable" title="git branch — click to switch">
                        <i className="fa-solid fa-code-branch termblocks-chip-icon" aria-hidden />
                        {gitInfo.branch}
                        {gitInfo.ahead ? <span className="termblocks-chip-sub">↑{gitInfo.ahead}</span> : null}
                        {gitInfo.behind ? <span className="termblocks-chip-sub">↓{gitInfo.behind}</span> : null}
                    </span>
                }>
                    {(close) => (
                        <BranchPickerContent
                            cwd={cwd}
                            currentBranch={gitInfo.branch}
                            blockId={blockId}
                            close={close}
                        />
                    )}
                </ChipPopover>
            )}
            {hasDiff && (
                <span
                    className="termblocks-chip termblocks-chip-clickable"
                    title="Open Code Review"
                    onClick={() => {
                        const lm = WorkspaceLayoutModel.getInstance();
                        lm.setCodeReviewVisible(!lm.getCodeReviewVisible());
                    }}
                >
                    <i className="fa-regular fa-file-lines termblocks-chip-icon" aria-hidden />
                    {gitInfo.changedfiles} file{(gitInfo.changedfiles ?? 0) === 1 ? "" : "s"}
                    {gitInfo.additions ? <span className="termblocks-chip-add"> +{gitInfo.additions}</span> : null}
                    {gitInfo.deletions ? <span className="termblocks-chip-del"> -{gitInfo.deletions}</span> : null}
                </span>
            )}
        </div>
    );
};
TermBlocksStatusBar.displayName = "TermBlocksStatusBar";

