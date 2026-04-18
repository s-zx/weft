// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

import * as WOS from "@/app/store/wos";
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

const TermBlockRow: React.FC<{
    block: CmdBlock;
    output: Uint8Array | undefined;
    model: TermBlocksViewModel;
}> = ({ block, output, model }) => {
    const isDone = block.state === "done";
    const isRunning = block.state === "running";
    const isError = isDone && block.exitcode != null && block.exitcode !== 0;
    const hasOutput = output != null && output.length > 0;
    const showXterm = hasOutput || isRunning; // always render for running so stdin is live

    return (
        <div className={cn("termblocks-row", `termblocks-row-${block.state}`, isError && "termblocks-row-error")}>
            <div className="termblocks-row-head">
                <span className="termblocks-seq">#{block.seq}</span>
                <span className="termblocks-state">{block.state}</span>
                {block.shelltype && <span className="termblocks-shell">{block.shelltype}</span>}
                {isDone && (
                    <span className={cn("termblocks-exit", isError && "is-error")}>exit {block.exitcode ?? "?"}</span>
                )}
                {block.durationms != null && <span className="termblocks-duration">{block.durationms}ms</span>}
            </div>
            <div className="termblocks-row-cmd">
                {block.cmd ? (
                    block.cmd
                ) : (
                    <em className="termblocks-placeholder">(waiting for command)</em>
                )}
            </div>
            {showXterm && (
                <XtermOutput
                    bytes={output ?? new Uint8Array()}
                    interactive={isRunning}
                    onData={isRunning ? (d) => model.sendBytes(d) : undefined}
                    onResize={isRunning ? (r, c) => model.sendResize(r, c) : undefined}
                />
            )}
            <div className="termblocks-row-offsets">
                prompt@{block.promptoffset}
                {block.cmdoffset != null && ` • cmd@${block.cmdoffset}`}
                {block.outputstartoffset != null &&
                    ` • out[${block.outputstartoffset}..${block.outputendoffset ?? "…"}]`}
            </div>
        </div>
    );
};
TermBlockRow.displayName = "TermBlockRow";

const TermBlocksInput: React.FC<{ model: TermBlocksViewModel }> = ({ model }) => {
    const inputRef = model.inputRef;
    const historyRef = React.useRef<string[]>([]);
    const historyIdxRef = React.useRef<number>(-1);
    const [value, setValue] = React.useState("");

    const submit = () => {
        const line = value;
        if (line.length === 0) {
            return;
        }
        historyRef.current.push(line);
        historyIdxRef.current = historyRef.current.length;
        setValue("");
        model.submitInput(line);
    };

    const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.ctrlKey && e.key.toLowerCase() === "c" && !e.metaKey) {
            e.preventDefault();
            setValue("");
            model.sendInterrupt();
            return;
        }
        if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            submit();
            return;
        }
        if (e.key === "ArrowUp") {
            if (historyRef.current.length === 0) return;
            e.preventDefault();
            historyIdxRef.current = Math.max(0, historyIdxRef.current - 1);
            setValue(historyRef.current[historyIdxRef.current] ?? "");
            return;
        }
        if (e.key === "ArrowDown") {
            if (historyRef.current.length === 0) return;
            e.preventDefault();
            const next = historyIdxRef.current + 1;
            if (next >= historyRef.current.length) {
                historyIdxRef.current = historyRef.current.length;
                setValue("");
            } else {
                historyIdxRef.current = next;
                setValue(historyRef.current[next]);
            }
        }
    };

    return (
        <div className="termblocks-input-row">
            <span className="termblocks-input-prompt">›</span>
            <input
                ref={inputRef}
                className="termblocks-input"
                type="text"
                value={value}
                autoFocus
                spellCheck={false}
                autoComplete="off"
                placeholder="Type a command and press Enter. Ctrl-C to interrupt."
                onChange={(e) => setValue(e.target.value)}
                onKeyDown={onKeyDown}
            />
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
    const scrollRef = React.useRef<HTMLDivElement>(null);

    // Only render rows that actually represent a command — a bare "prompt"
    // state is the transient anchor the next OSC C will attach to, with no
    // user-meaningful content yet.
    const visibleBlocks = React.useMemo(() => blocks.filter((b) => b.state !== "prompt"), [blocks]);
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
                        <div className="termblocks-header">
                            {visibleBlocks.length} command{visibleBlocks.length === 1 ? "" : "s"} · block{" "}
                            {model.blockId.slice(0, 8)}
                        </div>
                        {visibleBlocks.map((cb) => (
                            <TermBlockRow
                                key={cb.oid}
                                block={cb}
                                output={outputs[cb.oid]}
                                model={model}
                            />
                        ))}
                    </div>
                )}
            </div>
            {runningBlock == null && <TermBlocksInput model={model} />}
        </div>
    );
};
TermBlocksView.displayName = "TermBlocksView";
