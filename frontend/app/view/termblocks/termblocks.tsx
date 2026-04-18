// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

import * as WOS from "@/app/store/wos";
import { globalStore } from "@/app/store/jotaiStore";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { base64ToString, cn, stringToBase64 } from "@/util/util";
import * as jotai from "jotai";
import { useAtomValue } from "jotai";
import * as React from "react";
import "./termblocks.scss";

const PollIntervalMs = 1500;
const MaxRenderedBytesPerBlock = 256 * 1024;
const AnsiCsiRe = /\x1b\[[0-9;?]*[ -/]*[@-~]/g;
const AnsiOscRe = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g;

function stripAnsi(s: string): string {
    return s.replace(AnsiOscRe, "").replace(AnsiCsiRe, "");
}

export class TermBlocksViewModel implements ViewModel {
    viewType: string;
    blockId: string;

    viewIcon = jotai.atom<string>("list");
    viewName = jotai.atom<string>("Blocks");
    noPadding = jotai.atom<boolean>(true);
    viewText: jotai.Atom<HeaderElem[]>;

    blocksAtom: jotai.PrimitiveAtom<CmdBlock[]>;
    outputCacheAtom: jotai.PrimitiveAtom<Record<string, string>>;
    loadingAtom: jotai.PrimitiveAtom<boolean>;
    errorAtom: jotai.PrimitiveAtom<string>;

    disposed = false;
    pollTimer: ReturnType<typeof setInterval> | null = null;

    constructor({ blockId }: ViewModelInitType) {
        this.viewType = "termblocks";
        this.blockId = blockId;
        this.blocksAtom = jotai.atom<CmdBlock[]>([]) as jotai.PrimitiveAtom<CmdBlock[]>;
        this.outputCacheAtom = jotai.atom<Record<string, string>>({}) as jotai.PrimitiveAtom<Record<string, string>>;
        this.loadingAtom = jotai.atom<boolean>(true);
        this.errorAtom = jotai.atom<string>("") as jotai.PrimitiveAtom<string>;

        this.viewText = jotai.atom<HeaderElem[]>([
            {
                elemtype: "textbutton",
                text: "Back to Terminal",
                className: "grey !py-[2px] !px-[10px] text-[11px] font-[500]",
                title: "Switch this block's view back to the standard terminal",
                onClick: () => this.switchToTerminal(),
            },
        ]);

        this.fetchBlocks();
        this.pollTimer = setInterval(() => {
            if (!this.disposed) {
                this.fetchBlocks();
            }
        }, PollIntervalMs);
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

    async submitInput(raw: string) {
        const line = raw + "\r";
        await RpcApi.ControllerInputCommand(TabRpcClient, {
            blockid: this.blockId,
            inputdata64: stringToBase64(line),
        });
        this.fetchBlocks();
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
            let text = base64ToString(resp.data64);
            if (rawSize > MaxRenderedBytesPerBlock) {
                text += `\n\n[… truncated, ${rawSize - size} more bytes]`;
            }
            const cache = { ...globalStore.get(this.outputCacheAtom), [block.oid]: text };
            globalStore.set(this.outputCacheAtom, cache);
        } catch (e) {
            console.warn("termblocks: fetchOutputFor failed", block.oid, e);
        }
    }

    dispose() {
        this.disposed = true;
        if (this.pollTimer != null) {
            clearInterval(this.pollTimer);
            this.pollTimer = null;
        }
    }
}

const TermBlockRow: React.FC<{ block: CmdBlock; output: string | undefined }> = ({ block, output }) => {
    const isDone = block.state === "done";
    const isError = isDone && block.exitcode != null && block.exitcode !== 0;
    const cleanedOutput = output != null ? stripAnsi(output).replace(/\r\n/g, "\n").replace(/\r/g, "\n") : null;
    const hasOutput = cleanedOutput != null && cleanedOutput.length > 0;

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
            {hasOutput && <pre className="termblocks-row-output">{cleanedOutput}</pre>}
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
    const inputRef = React.useRef<HTMLInputElement>(null);
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
                placeholder="Type a command and press Enter…"
                onChange={(e) => setValue(e.target.value)}
                onKeyDown={onKeyDown}
            />
        </div>
    );
};
TermBlocksInput.displayName = "TermBlocksInput";

export const TermBlocksView: React.FC<ViewComponentProps<TermBlocksViewModel>> = ({ model }) => {
    const blocks = useAtomValue(model.blocksAtom);
    const outputs = useAtomValue(model.outputCacheAtom);
    const loading = useAtomValue(model.loadingAtom);
    const error = useAtomValue(model.errorAtom);
    const scrollRef = React.useRef<HTMLDivElement>(null);

    // Only render rows that actually represent a command — a bare "prompt"
    // state is the transient anchor the next OSC C will attach to, with no
    // user-meaningful content yet.
    const visibleBlocks = React.useMemo(() => blocks.filter((b) => b.state !== "prompt"), [blocks]);
    const lastKey =
        visibleBlocks.length > 0 ? `${visibleBlocks[visibleBlocks.length - 1].oid}:${visibleBlocks.length}` : "";

    React.useEffect(() => {
        const el = scrollRef.current;
        if (el == null) return;
        el.scrollTop = el.scrollHeight;
    }, [lastKey]);

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
                            <TermBlockRow key={cb.oid} block={cb} output={outputs[cb.oid]} />
                        ))}
                    </div>
                )}
            </div>
            <TermBlocksInput model={model} />
        </div>
    );
};
TermBlocksView.displayName = "TermBlocksView";
