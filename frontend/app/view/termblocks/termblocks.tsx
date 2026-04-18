// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

import * as WOS from "@/app/store/wos";
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
const MinXtermRows = 3;

export class TermBlocksViewModel implements ViewModel {
    viewType: string;
    blockId: string;

    viewIcon = jotai.atom<string>("list");
    viewName = jotai.atom<string>("Blocks");
    noPadding = jotai.atom<boolean>(true);
    viewText: jotai.Atom<HeaderElem[]>;

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
    historyAtom: jotai.PrimitiveAtom<string[]>;

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
        this.historyAtom = jotai.atom<string[]>([]) as jotai.PrimitiveAtom<string[]>;
        this.loadShellHistory();

        this.viewText = jotai.atom<HeaderElem[]>([
            {
                elemtype: "textbutton",
                text: "Back to Terminal",
                className: "grey !py-[2px] !px-[10px] text-[11px] font-[500]",
                title: "Switch this block's view back to the standard terminal",
                onClick: () => this.switchToTerminal(),
            },
        ]);

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

    async switchToTerminal() {
        await RpcApi.SetMetaCommand(TabRpcClient, {
            oref: WOS.makeORef("block", this.blockId),
            meta: { view: "term" },
        });
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

function countNewlines(bytes: Uint8Array): number {
    let n = 0;
    for (let i = 0; i < bytes.length; i++) {
        if (bytes[i] === 0x0a) {
            n++;
        }
    }
    return n;
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
                      background: "#1a1a1a",
                      foreground: "#e0e0e0",
                  }
                : {
                      background: "#1a1a1a",
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
        const wantRows = Math.min(MaxXtermRows, Math.max(MinXtermRows, countNewlines(bytes) + 1));
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

const TermBlockRow: React.FC<{
    block: CmdBlock;
    output: Uint8Array | undefined;
    model: TermBlocksViewModel;
    fallbackCwd: string;
    home: string;
    gitInfo: GitInfoResponse | null;
}> = ({ block, output, model, fallbackCwd, home, gitInfo }) => {
    const isDone = block.state === "done";
    const isRunning = block.state === "running";
    const isError = isDone && block.exitcode != null && block.exitcode !== 0;
    const hasOutput = output != null && output.length > 0;
    const showXterm = hasOutput || isRunning;
    const cwd = shortenCwd(block.cwd ?? fallbackCwd, home);
    const duration = formatDuration(block.durationms);
    const branch = gitInfo?.isrepo ? gitInfo.branch : "";
    const hasDiff = gitInfo?.isrepo && ((gitInfo.changedfiles ?? 0) > 0);

    return (
        <div className={cn("termblocks-row", `termblocks-row-${block.state}`, isError && "termblocks-row-error")}>
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

    return (
        <div className="termblocks-input-row">
            <span className="termblocks-input-prompt">›</span>
            <div className="termblocks-input-wrap">
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
                {ghost && (
                    <span className="termblocks-input-ghost" aria-hidden>
                        <span className="termblocks-input-ghost-prefix">{value}</span>
                        <span className="termblocks-input-ghost-suffix">{ghost}</span>
                    </span>
                )}
            </div>
            <button
                type="button"
                className="termblocks-input-interrupt"
                title="Send SIGINT (Ctrl-C)"
                onClick={() => model.sendInterrupt()}
            >
                ⊗
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
    const scrollRef = React.useRef<HTMLDivElement>(null);

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
    const visibleBlocks = React.useMemo(
        () => blocks.filter((b) => b.state !== "prompt" && b.seq > minVisibleSeq),
        [blocks, minVisibleSeq]
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
            <div className="termblocks-scroll" ref={scrollRef}>
                {error && <div className="termblocks-empty termblocks-error">Error: {error}</div>}
                {!error && loading && visibleBlocks.length === 0 && (
                    <div className="termblocks-empty">Loading…</div>
                )}
                {!error && !loading && visibleBlocks.length === 0 && (
                    <div className="termblocks-empty">
                        No commands yet on this block. Type below to run one.
                    </div>
                )}
                {visibleBlocks.length > 0 && (
                    <div className="termblocks-container">
                        {visibleBlocks.map((cb) => (
                            <TermBlockRow
                                key={cb.oid}
                                block={cb}
                                output={outputs[cb.oid]}
                                model={model}
                                fallbackCwd={blockMetaCwd}
                                home={home}
                                gitInfo={gitInfo}
                            />
                        ))}
                    </div>
                )}
            </div>
            {runningBlock == null && <TermBlocksInput model={model} />}
            <TermBlocksStatusBar cwd={blockMetaCwd} home={home} gitInfo={gitInfo} />
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
                    <span className="termblocks-chip-icon">📁</span>
                    {shortCwd}
                </span>
            )}
            {gitInfo?.isrepo && gitInfo.branch && (
                <span className="termblocks-chip" title="git branch">
                    <span className="termblocks-chip-icon"> </span>
                    {gitInfo.branch}
                    {gitInfo.ahead ? <span className="termblocks-chip-sub">↑{gitInfo.ahead}</span> : null}
                    {gitInfo.behind ? <span className="termblocks-chip-sub">↓{gitInfo.behind}</span> : null}
                </span>
            )}
            {gitInfo?.isrepo && (gitInfo.changedfiles ?? 0) > 0 && (
                <span className="termblocks-chip" title="uncommitted changes">
                    <span className="termblocks-chip-icon">±</span>
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
