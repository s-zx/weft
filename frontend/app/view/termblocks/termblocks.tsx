// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

import { ContextMenuModel } from "@/app/store/contextmenu";
import { getApi, getBlockMetaKeyAtom } from "@/app/store/global";
import { globalStore } from "@/app/store/jotaiStore";
import { waveEventSubscribeSingle } from "@/app/store/wps";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { atoms } from "@/store/global";
import { base64ToArray, cn, stringToBase64 } from "@/util/util";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import * as jotai from "jotai";
import { useAtomValue } from "jotai";
import * as React from "react";
import "@xterm/xterm/css/xterm.css";
import "./termblocks.scss";

const PollIntervalMs = 10_000; // safety net; live updates arrive via wps events
const MaxRenderedBytesPerBlock = 256 * 1024;
const MaxXtermRows = 40;
const MinXtermRows = 1;

export class TermBlocksViewModel implements ViewModel {
    viewType: string;
    blockId: string;

    viewIcon = jotai.atom<string>("list");
    viewName = jotai.atom<string>("Blocks");
    noPadding = jotai.atom<boolean>(true);
    noHeader = jotai.atom<boolean>(true);

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
    inputRef: React.RefObject<HTMLInputElement | null> = React.createRef();

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
            } else if (existing instanceof Uint8Array && existing.length > expected) {
                // Streaming cache accumulated bytes beyond outputendoffset (prompt
                // text arrived in the same pty chunk as the last output bytes).
                // The cache is NOT byte-aligned to outputstartoffset (the chunk
                // that contained OSC C included pre-C bytes too), so slice(0,N)
                // would cut the wrong range.  Force a re-fetch from the server
                // which returns the authoritative [outputstartoffset,outputendoffset]
                // range and discards the extra bytes safely.
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
            const cache = { ...globalStore.get(this.outputCacheAtom), [block.oid]: "" };
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
}> = ({ bytes, onData, onResize }) => {
    const containerRef = React.useRef<HTMLDivElement>(null);
    const termRef = React.useRef<Terminal | null>(null);
    const writtenRef = React.useRef<number>(0);
    const onDataRef = React.useRef(onData);
    const onResizeRef = React.useRef(onResize);
    React.useEffect(() => {
        onDataRef.current = onData;
        onResizeRef.current = onResize;
    });

    React.useEffect(() => {
        const host = containerRef.current;
        if (host == null) return;
        const term = new Terminal({
            convertEol: false,
            cursorBlink: true,
            fontFamily: "ui-monospace, Menlo, Consolas, monospace",
            fontSize: 13,
            theme: {
                background: "#000000",
                foreground: "#e0e0e0",
            },
        });
        const fit = new FitAddon();
        term.loadAddon(fit);
        term.open(host);

        const pushSize = () => {
            try {
                fit.fit();
            } catch {
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
        const written = writtenRef.current;
        if (bytes.length === written) {
            return;
        }
        if (bytes.length < written) {
            term.reset();
            term.write(bytes);
        } else if (written === 0) {
            term.write(bytes);
        } else {
            term.write(bytes.subarray(written));
        }
        writtenRef.current = bytes.length;
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
}> = ({ bytes, interactive = false, onData, onResize }) => {
    const containerRef = React.useRef<HTMLDivElement>(null);
    const termRef = React.useRef<Terminal | null>(null);
    const fitRef = React.useRef<FitAddon | null>(null);
    const writtenRef = React.useRef<number>(0);
    const onDataRef = React.useRef(onData);
    const onResizeRef = React.useRef(onResize);
    React.useEffect(() => {
        onDataRef.current = onData;
        onResizeRef.current = onResize;
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
        const term = new Terminal({
            cols: 120,
            rows: MinXtermRows,
            disableStdin: !interactive,
            convertEol: true,
            cursorBlink: interactive,
            scrollback: 0,
            fontFamily: "ui-monospace, Menlo, Consolas, monospace",
            fontSize: 12,
            theme: interactive
                ? {
                      background: "rgba(0,0,0,0)",
                      foreground: "#e0e0e0",
                  }
                : {
                      background: "rgba(0,0,0,0)",
                      foreground: "#e0e0e0",
                      cursor: "transparent",
                      cursorAccent: "transparent",
                  },
        });
        const fit = new FitAddon();
        term.loadAddon(fit);
        term.open(host);
        try {
            fit.fit();
        } catch {
            // container has zero width on first paint — defaults are fine
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
        const visible = countVisibleLines(bytes);
        const wantRows = Math.min(MaxXtermRows, Math.max(MinXtermRows, visible));
        if (term.rows !== wantRows) {
            try {
                term.resize(term.cols, wantRows);
            } catch {
                // ignore
            }
        }
        const written = writtenRef.current;
        if (bytes.length === written) {
            return;
        }
        if (bytes.length < written) {
            term.reset();
            if (!interactive) {
                term.write(HideCursorSeq);
            }
            term.write(bytes);
        } else if (written === 0) {
            term.write(bytes);
        } else {
            term.write(bytes.subarray(written));
        }
        writtenRef.current = bytes.length;
    }, [bytes, interactive]);

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
}> = ({ block, output, model, fallbackCwd, home, gitInfo, selected }) => {
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

    // Ghost suggestion: latest history entry that starts with the typed prefix
    // but is strictly longer.  Shown dimmed after the caret; Tab/Right-arrow
    // accepts it.  Matches the "inline autosuggest" behaviour zsh-autosuggestions
    // and fish pioneered and Warp echoes.
    const ghost = React.useMemo(() => {
        if (!value) return "";
        for (let i = history.length - 1; i >= 0; i--) {
            const h = history[i];
            if (h.length > value.length && h.startsWith(value)) {
                return h.slice(value.length);
            }
        }
        return "";
    }, [value, history]);

    const submit = () => {
        const line = value;
        if (line.length === 0) {
            return;
        }
        model.recordHistory(line);
        historyIdxRef.current = -1;
        draftRef.current = "";
        setValue("");
        model.submitInput(line);
    };

    const acceptGhost = () => {
        if (!ghost) return false;
        setValue(value + ghost);
        return true;
    };

    const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.ctrlKey && e.key.toLowerCase() === "c" && !e.metaKey) {
            e.preventDefault();
            setValue("");
            historyIdxRef.current = -1;
            draftRef.current = "";
            model.sendInterrupt();
            return;
        }
        if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            submit();
            return;
        }
        if (e.key === "Tab" && !e.shiftKey) {
            e.preventDefault();
            acceptGhost();
            return;
        }
        if (e.key === "ArrowRight" && inputRef.current != null) {
            const el = inputRef.current;
            if (el.selectionStart === value.length && el.selectionEnd === value.length && ghost) {
                e.preventDefault();
                acceptGhost();
                return;
            }
        }
        if (e.key === "ArrowUp") {
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

    const tokens = React.useMemo(() => tokenizeShell(value), [value]);

    return (
        <div className="termblocks-input-row">
            <span className="termblocks-input-prompt">›</span>
            <div className="termblocks-input-wrap">
                <div className="termblocks-input-highlight" aria-hidden>
                    {tokens.map((t, idx) => (
                        <span key={idx} className={`tok-${t.kind}`}>
                            {t.text}
                        </span>
                    ))}
                    {ghost && <span className="tok-ghost">{ghost}</span>}
                </div>
                <input
                    ref={inputRef}
                    className="termblocks-input"
                    type="text"
                    value={value}
                    autoFocus
                    spellCheck={false}
                    autoComplete="off"
                    placeholder="Type a command and press Enter. Ctrl-C to interrupt."
                    onChange={(e) => {
                        setValue(e.target.value);
                        historyIdxRef.current = -1;
                    }}
                    onKeyDown={onKeyDown}
                />
            </div>
            <button
                type="button"
                className="termblocks-input-interrupt"
                title="Send SIGINT (Ctrl-C)"
                onClick={() => model.sendInterrupt()}
            >
                <i className="fa-solid fa-ban" aria-hidden />
            </button>
        </div>
    );
};
TermBlocksInput.displayName = "TermBlocksInput";

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
    const scrollRef = React.useRef<HTMLDivElement>(null);

    React.useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key === "Escape" && selectedOid) {
                model.clearSelection();
            }
        };
        window.addEventListener("keydown", onKey);
        return () => window.removeEventListener("keydown", onKey);
    }, [model, selectedOid]);

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
    const lastOid = visibleBlocks.length > 0 ? visibleBlocks[visibleBlocks.length - 1].oid : "";
    const lastOutputLen = lastOid && outputs[lastOid] != null ? outputs[lastOid].length : 0;
    const inAltScreen = altOID !== "";
    const runningBlock = React.useMemo(() => blocks.find((b) => b.state === "running") ?? null, [blocks]);

    // Scroll to the bottom whenever the last visible block changes OR its
    // output bytes arrive.  xterm itself lays out across a couple of frames,
    // so we trigger a deferred scroll in addition to the immediate one.
    React.useEffect(() => {
        if (inAltScreen) {
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
    }, [lastOid, lastOutputLen, visibleBlocks.length, inAltScreen]);

    // Alt-screen mode: a TUI (less/vim/top/…) took over the PTY, so we show
    // the running block's buffer in a single full-viewport xterm with stdin
    // enabled.  Keystrokes go straight to the PTY via onData.
    if (inAltScreen) {
        const running = blocks.find((b) => b.state === "running") ?? blocks[blocks.length - 1];
        const bytes = running != null ? outputs[running.oid] ?? new Uint8Array() : new Uint8Array();
        return (
            <div className="termblocks-root">
                <div className="termblocks-altscreen-wrap">
                    <AltScreenXterm
                        bytes={bytes}
                        onData={(s) => model.sendBytes(s)}
                        onResize={(r, c) => model.sendResize(r, c)}
                    />
                </div>
            </div>
        );
    }

    return (
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
                {visibleBlocks.length > 0 && (
                    <div
                        className="termblocks-container"
                        onClick={(e) => {
                            if (e.target === e.currentTarget) {
                                model.clearSelection();
                            }
                        }}
                    >
                        {visibleBlocks.map((cb) => (
                            <TermBlockRow
                                key={cb.oid}
                                block={cb}
                                output={outputs[cb.oid]}
                                model={model}
                                fallbackCwd={blockMetaCwd}
                                home={home}
                                gitInfo={gitInfo}
                                selected={cb.oid === selectedOid}
                            />
                        ))}
                    </div>
                )}
            </div>
            <TermBlocksStatusBar cwd={blockMetaCwd} home={home} gitInfo={gitInfo} />
            {runningBlock == null && <TermBlocksInput model={model} />}
        </div>
    );
};
TermBlocksView.displayName = "TermBlocksView";

const TermBlocksStatusBar: React.FC<{
    cwd: string;
    home: string;
    gitInfo: GitInfoResponse | null;
}> = ({ cwd, home, gitInfo }) => {
    const shortCwd = shortenCwd(cwd, home);
    if (!shortCwd && !gitInfo?.isrepo) {
        return null;
    }
    return (
        <div className="termblocks-statusbar">
            {shortCwd && (
                <span className="termblocks-chip" title={cwd}>
                    <i className="fa-regular fa-folder termblocks-chip-icon" aria-hidden />
                    {shortCwd}
                </span>
            )}
            {gitInfo?.isrepo && gitInfo.branch && (
                <span className="termblocks-chip" title="git branch">
                    <i className="fa-solid fa-code-branch termblocks-chip-icon" aria-hidden />
                    {gitInfo.branch}
                    {gitInfo.ahead ? <span className="termblocks-chip-sub">↑{gitInfo.ahead}</span> : null}
                    {gitInfo.behind ? <span className="termblocks-chip-sub">↓{gitInfo.behind}</span> : null}
                </span>
            )}
            {gitInfo?.isrepo && (gitInfo.changedfiles ?? 0) > 0 && (
                <span className="termblocks-chip" title="uncommitted changes">
                    <i className="fa-regular fa-file-lines termblocks-chip-icon" aria-hidden />
                    {gitInfo.changedfiles} file{(gitInfo.changedfiles ?? 0) === 1 ? "" : "s"}
                    {gitInfo.additions ? (
                        <span className="termblocks-chip-add"> +{gitInfo.additions}</span>
                    ) : null}
                    {gitInfo.deletions ? (
                        <span className="termblocks-chip-del"> -{gitInfo.deletions}</span>
                    ) : null}
                </span>
            )}
        </div>
    );
};
TermBlocksStatusBar.displayName = "TermBlocksStatusBar";

type ShellToken = { text: string; kind: "cmd" | "flag" | "string" | "var" | "comment" | "op" | "path" | "text" };

// Minimal bash-ish tokenizer for the input line.  Good enough to color
// the command, flags, quoted strings, variables, comments, and basic
// shell operators (| && || ; > < >>).  Everything else stays plain.
function tokenizeShell(input: string): ShellToken[] {
    const tokens: ShellToken[] = [];
    let i = 0;
    let seenFirstWord = false;
    const isWs = (c: string) => c === " " || c === "\t";
    const isWordChar = (c: string) =>
        c !== " " && c !== "\t" && c !== "\"" && c !== "'" && c !== "$" && c !== "#" &&
        c !== "|" && c !== "&" && c !== ";" && c !== "<" && c !== ">" && c !== "(" && c !== ")";
    while (i < input.length) {
        const c = input[i];
        if (isWs(c)) {
            let j = i;
            while (j < input.length && isWs(input[j])) j++;
            tokens.push({ text: input.slice(i, j), kind: "text" });
            i = j;
            continue;
        }
        if (c === "#") {
            tokens.push({ text: input.slice(i), kind: "comment" });
            break;
        }
        if (c === "\"") {
            let j = i + 1;
            while (j < input.length && input[j] !== "\"") {
                if (input[j] === "\\" && j + 1 < input.length) j++;
                j++;
            }
            if (j < input.length) j++;
            tokens.push({ text: input.slice(i, j), kind: "string" });
            i = j;
            seenFirstWord = true;
            continue;
        }
        if (c === "'") {
            let j = i + 1;
            while (j < input.length && input[j] !== "'") j++;
            if (j < input.length) j++;
            tokens.push({ text: input.slice(i, j), kind: "string" });
            i = j;
            seenFirstWord = true;
            continue;
        }
        if (c === "$") {
            let j = i + 1;
            if (input[j] === "{") {
                while (j < input.length && input[j] !== "}") j++;
                if (j < input.length) j++;
            } else {
                while (j < input.length && /[A-Za-z0-9_]/.test(input[j])) j++;
            }
            tokens.push({ text: input.slice(i, j), kind: "var" });
            i = j;
            continue;
        }
        if (c === "|" || c === "&" || c === ";" || c === "<" || c === ">" || c === "(" || c === ")") {
            let j = i + 1;
            if ((c === "|" || c === "&" || c === ">") && input[j] === c) j++;
            tokens.push({ text: input.slice(i, j), kind: "op" });
            i = j;
            seenFirstWord = false;
            continue;
        }
        let j = i;
        while (j < input.length && isWordChar(input[j])) j++;
        const word = input.slice(i, j);
        if (!seenFirstWord) {
            tokens.push({ text: word, kind: "cmd" });
            seenFirstWord = true;
        } else if (word.startsWith("-")) {
            tokens.push({ text: word, kind: "flag" });
        } else if (word.includes("/") || word.startsWith("~") || word.startsWith(".")) {
            tokens.push({ text: word, kind: "path" });
        } else {
            tokens.push({ text: word, kind: "text" });
        }
        i = j;
    }
    return tokens;
}
