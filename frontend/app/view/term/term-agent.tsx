// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { ApprovalDestination, SuggestedRule, WaveUIMessage, WaveUIMessagePart } from "@/store/aitypes";
import * as Diff from "diff";
import { WaveStreamdown } from "@/app/element/streamdown";
import { globalStore } from "@/app/store/jotaiStore";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { getWebServerEndpoint } from "@/util/endpoints";
import { useChat } from "@ai-sdk/react";
import { DefaultChatTransport } from "ai";
import * as jotai from "jotai";
import { createContext, memo, useContext, useEffect, useMemo, useRef, useState } from "react";

const TermAgentCodeBlockMaxWidth = jotai.atom(720);
const TermAgentInlineDiffMaxBytes = 200_000;

export type TermAgentModel = {
    blockId: string;
    tabModel: { tabId: string };
    termAgentVisible: jotai.PrimitiveAtom<boolean>;
    termAgentComposerOpen: jotai.PrimitiveAtom<boolean>;
    termAgentInput: jotai.PrimitiveAtom<string>;
    termAgentError: jotai.PrimitiveAtom<string | null>;
    termAgentChatId: jotai.PrimitiveAtom<string>;
    termAgentAgentMode: jotai.Atom<"ask" | "plan" | "do">;
    getAndClearTermAgentMessage(): any;
    getTermAgentMode(): string;
    getTermAgentModelOverride(): string;
    getAndClearTermAgentPendingMode(): string;
    getAndClearTermAgentPendingContext(): any;
    getAndClearTermAgentPlanPath(): string;
    // getTermAgentCwd returns the current cwd for permission persistence
    // ("save to this project" needs cwd). Stable accessor unlike the
    // pending-context getter which clears state.
    getTermAgentCwd(): string | undefined;
    termAgentLastPlanPath: string | null;
    executePlan(): void;
    registerTermAgentChat(
        sendMessage: any,
        setMessages: any,
        status: string,
        stop: () => void
    ): void;
    clearTermAgentSession(): void;
    hideTermAgentOverlay(): void;
    setTermAgentError(message: string | null): void;
    submitTermAgentPrompt(): Promise<void>;
    closeTermAgentComposer(): void;
};

type TermAgentOverlayProps = {
    model: TermAgentModel;
};

const TermAgentTokenCounter = memo(({ messages }: { messages: WaveUIMessage[] }) => {
    let inputTokens = 0;
    let outputTokens = 0;
    for (const msg of messages) {
        for (const part of msg.parts ?? []) {
            if (part.type === "data-usage" && part.data) {
                inputTokens = part.data.inputtokens ?? 0;
                outputTokens = part.data.outputtokens ?? 0;
            }
        }
    }
    if (inputTokens === 0 && outputTokens === 0) {
        return null;
    }
    const total = inputTokens + outputTokens;
    const formatted = total >= 1000 ? `${(total / 1000).toFixed(1)}k` : String(total);
    return <span className="text-zinc-500">{formatted} tokens</span>;
});

TermAgentTokenCounter.displayName = "TermAgentTokenCounter";

const TermAgentInlineDiff = memo(
    ({ original, modified, filename }: { original: string; modified: string; filename?: string }) => {
        const isNewFile = original === "" || original == null;
        const noChanges = original === modified;
        const label = filename ? filename.replace(/^.*\//, "") : "Diff preview";
        const tooLarge =
            (original?.length ?? 0) > TermAgentInlineDiffMaxBytes ||
            (modified?.length ?? 0) > TermAgentInlineDiffMaxBytes;

        const hunks = useMemo(() => {
            if (noChanges || isNewFile || tooLarge) return null;
            const patch = Diff.structuredPatch("a", "b", original ?? "", modified ?? "", "", "", { context: 3 });
            return patch.hunks;
        }, [original, modified, noChanges, isNewFile, tooLarge]);

        if (tooLarge) {
            return (
                <div className="mt-2 rounded border border-zinc-800 px-3 py-1.5 text-xs text-zinc-500">
                    {label}: file too large to diff inline ({Math.max(original?.length ?? 0, modified?.length ?? 0)} bytes)
                </div>
            );
        }

        if (noChanges) {
            return <div className="mt-2 rounded border border-zinc-800 px-3 py-1.5 text-xs text-zinc-500">No changes</div>;
        }
        if (isNewFile) {
            return (
                <details open className="mt-2">
                    <summary className="cursor-pointer select-none text-xs text-zinc-400">
                        {label} <span className="text-zinc-500">New file</span>
                    </summary>
                    <pre className="mt-1 max-h-[200px] overflow-y-auto rounded border border-zinc-800 bg-[#12261e] p-2 font-mono text-[11px] leading-relaxed text-emerald-300">
                        {modified}
                    </pre>
                </details>
            );
        }

        return (
            <details open className="mt-2">
                <summary className="cursor-pointer select-none text-xs text-zinc-400">{label}</summary>
                <div className="mt-1 max-h-[240px] overflow-y-auto rounded border border-zinc-800 bg-[#0d1117] font-mono text-[11px] leading-[1.6]">
                    {hunks?.map((hunk, hi) => {
                        let newLineNo = hunk.newStart;
                        let oldLineNo = hunk.oldStart;
                        return (
                            <div key={hi}>
                                {hi > 0 && <div className="border-t border-zinc-800/50 py-0.5 text-center text-[10px] text-zinc-600">···</div>}
                                {hunk.lines.map((line, li) => {
                                    const prefix = line[0];
                                    const text = line.slice(1);
                                    let lineNo = "";
                                    let bg = "";
                                    let textColor = "text-zinc-400";
                                    let lineNoColor = "text-zinc-600";
                                    if (prefix === "+") {
                                        lineNo = String(newLineNo++);
                                        bg = "bg-[#12261e]";
                                        textColor = "text-emerald-300";
                                        lineNoColor = "text-emerald-700";
                                    } else if (prefix === "-") {
                                        oldLineNo++;
                                        bg = "bg-[#2d1215]";
                                        textColor = "text-red-300";
                                        lineNoColor = "text-red-700";
                                    } else {
                                        lineNo = String(newLineNo++);
                                        oldLineNo++;
                                    }
                                    return (
                                        <div key={`${hi}-${li}`} className={`flex ${bg}`}>
                                            <span className={`w-8 shrink-0 select-none pr-2 text-right ${lineNoColor}`}>{lineNo}</span>
                                            <span className={`w-4 shrink-0 select-none text-center ${textColor}`}>{prefix}</span>
                                            <span className={`flex-1 whitespace-pre-wrap break-all pr-2 ${textColor}`}>{text}</span>
                                        </div>
                                    );
                                })}
                            </div>
                        );
                    })}
                </div>
            </details>
        );
    }
);

TermAgentInlineDiff.displayName = "TermAgentInlineDiff";

// TermAgentApprovalContext threads chatId + cwd from the chat-level
// model down to the per-tool-call approval button without prop-drilling
// through three intermediate components. Both fields are required when
// the user picks "Approve and Remember" with a non-session destination
// (project rules need cwd, all destinations need chatId for session-
// scope writes).
type TermAgentApprovalContextValue = {
    chatId: string;
    getCwd: () => string | undefined;
};

const TermAgentApprovalContext = createContext<TermAgentApprovalContextValue | null>(null);

const TermAgentApprovalButtons = memo(
    ({ toolCallId, suggestions }: { toolCallId: string; suggestions?: SuggestedRule[] }) => {
        const [approvalOverride, setApprovalOverride] = useState<string | null>(null);
        // selectedIdx === -1 means "approve once, don't remember" — the
        // default. Picking a suggestion sets the index; clicking the
        // radio again toggles back to -1.
        const [selectedIdx, setSelectedIdx] = useState<number>(-1);
        const [destination, setDestination] = useState<ApprovalDestination>("session");
        const ctx = useContext(TermAgentApprovalContext);
        const hasSuggestions = (suggestions?.length ?? 0) > 0;

        const sendApproval = (approval: "user-approved" | "user-denied") => {
            setApprovalOverride(approval);
            const payload: Parameters<typeof RpcApi.WaveAIToolApproveCommand>[1] = {
                toolcallid: toolCallId,
                approval,
            };
            // Persist a "remember this" rule alongside the approval. Only
            // sent on user-approved + a chosen suggestion — denials and
            // approve-once never persist.
            if (approval === "user-approved" && hasSuggestions && selectedIdx >= 0 && ctx) {
                const chosen = suggestions![selectedIdx];
                payload.chatid = ctx.chatId;
                payload.acceptedtoolname = chosen.toolname;
                payload.acceptedcontent = chosen.content;
                payload.accepteddestination = destination;
                if (destination === "localProject" || destination === "sharedProject") {
                    payload.cwd = ctx.getCwd();
                }
            }
            RpcApi.WaveAIToolApproveCommand(TabRpcClient, payload);
        };

        return (
            <div className="mt-2 flex flex-col gap-2">
                <div className="flex gap-2">
                    <button
                        className="rounded border border-emerald-500/60 px-2 py-1 text-xs text-emerald-200 transition-colors hover:bg-emerald-500/10 cursor-pointer"
                        onClick={() => sendApproval("user-approved")}
                    >
                        {approvalOverride === "user-approved"
                            ? selectedIdx >= 0
                                ? "Approved + saved"
                                : "Approved"
                            : selectedIdx >= 0
                              ? "Approve and remember"
                              : "Approve"}
                    </button>
                    <button
                        className="rounded border border-red-500/60 px-2 py-1 text-xs text-red-200 transition-colors hover:bg-red-500/10 cursor-pointer"
                        onClick={() => sendApproval("user-denied")}
                    >
                        {approvalOverride === "user-denied" ? "Denied" : "Deny"}
                    </button>
                </div>
                {hasSuggestions && approvalOverride == null && (
                    <div className="rounded border border-zinc-700/60 bg-black/20 p-2 text-xs text-zinc-300">
                        <div className="mb-1 text-zinc-400">Or remember:</div>
                        <div className="flex flex-col gap-1">
                            {suggestions!.map((s, i) => (
                                <label key={i} className="flex cursor-pointer items-center gap-2">
                                    <input
                                        type="radio"
                                        name={`suggest-${toolCallId}`}
                                        checked={selectedIdx === i}
                                        onChange={() => setSelectedIdx(i)}
                                    />
                                    <span>{s.display}</span>
                                </label>
                            ))}
                        </div>
                        {selectedIdx >= 0 && (
                            <div className="mt-2 flex items-center gap-2 text-zinc-400">
                                <span>Save to:</span>
                                <select
                                    className="rounded border border-zinc-600 bg-black/30 px-1 py-0.5 text-zinc-200 cursor-pointer"
                                    value={destination}
                                    onChange={(e) => setDestination(e.target.value as ApprovalDestination)}
                                >
                                    <option value="session">This chat only</option>
                                    {/* Project options need cwd to write the right
                                        .crest/permissions{,.local}.json file. When
                                        cwd is unknown (e.g. focused block has none
                                        yet), disable them rather than letting the
                                        backend silently fail with no UI feedback. */}
                                    <option value="localProject" disabled={!ctx?.getCwd()}>
                                        This project (local){!ctx?.getCwd() ? " — cwd unknown" : ""}
                                    </option>
                                    <option value="sharedProject" disabled={!ctx?.getCwd()}>
                                        This project (shared){!ctx?.getCwd() ? " — cwd unknown" : ""}
                                    </option>
                                    <option value="user">Always (all projects)</option>
                                </select>
                            </div>
                        )}
                    </div>
                )}
            </div>
        );
    }
);

TermAgentApprovalButtons.displayName = "TermAgentApprovalButtons";

const TermAgentToolUse = memo(({ part, isStreaming }: { part: WaveUIMessagePart; isStreaming: boolean }) => {
    if (part.type !== "data-tooluse") {
        return null;
    }

    const toolData = part.data;
    const status = toolData?.status ?? "pending";
    const approval = toolData?.approval ?? "";
    const isError = status === "error";
    const isDone = status === "completed";
    const toolDesc = toolData?.tooldesc || toolData?.toolname || "Tool call";
    const errorText =
        toolData?.errormessage || (!isStreaming && approval === "needs-approval" ? "Approval timed out." : "");

    return (
        <div className="rounded-lg border border-zinc-700/80 bg-black/20 px-3 py-2 text-sm">
            <div className="flex items-center gap-2">
                <span
                    className={
                        isDone ? "text-emerald-300" : isError ? "text-red-300" : "text-[var(--color-accent)]"
                    }
                >
                    {isDone ? "✓" : isError ? "!" : "•"}
                </span>
                <div className="min-w-0 flex-1">
                    <div className="truncate text-zinc-100">{toolDesc}</div>
                    <div className="text-xs text-zinc-400">{toolData?.toolname ?? "tool"}</div>
                </div>
            </div>
            {toolData?.modifiedcontent != null && (
                <TermAgentInlineDiff
                    original={toolData?.originalcontent ?? ""}
                    modified={toolData.modifiedcontent}
                    filename={toolData?.inputfilename}
                />
            )}
            {errorText && <div className="mt-2 text-xs text-red-300">{errorText}</div>}
            {approval === "needs-approval" && isStreaming && toolData?.toolcallid && (
                <TermAgentApprovalButtons
                    toolCallId={toolData.toolcallid}
                    suggestions={toolData.suggestions}
                />
            )}
        </div>
    );
});

TermAgentToolUse.displayName = "TermAgentToolUse";

const TermAgentToolProgress = memo(({ part }: { part: WaveUIMessagePart }) => {
    if (part.type !== "data-toolprogress") {
        return null;
    }

    const lines = Array.isArray(part.data?.statuslines) ? part.data.statuslines : [];
    if (lines.length === 0) {
        return null;
    }

    return (
        <div className="rounded-lg border border-zinc-800 bg-black/15 px-3 py-2 text-xs text-zinc-300">
            {lines.map((line, idx) => (
                <div key={`${idx}:${line}`}>{line}</div>
            ))}
        </div>
    );
});

TermAgentToolProgress.displayName = "TermAgentToolProgress";

export const TermAgentMessagePartView = memo(({ part, isStreaming }: { part: WaveUIMessagePart; isStreaming: boolean }) => {
    if (part.type === "text") {
        return (
            <WaveStreamdown
                text={part.text ?? ""}
                parseIncompleteMarkdown={isStreaming}
                className="text-zinc-100"
                codeBlockMaxWidthAtom={TermAgentCodeBlockMaxWidth}
            />
        );
    }

    if (part.type === "reasoning") {
        return (
            <details className="rounded-lg border border-zinc-800 bg-black/15 px-3 py-2 text-xs text-zinc-400">
                <summary className="cursor-pointer select-none text-zinc-300">Reasoning</summary>
                <div className="mt-2 whitespace-pre-wrap">{part.text ?? ""}</div>
            </details>
        );
    }

    if (part.type === "data-tooluse") {
        return <TermAgentToolUse part={part} isStreaming={isStreaming} />;
    }

    if (part.type === "data-toolprogress") {
        return <TermAgentToolProgress part={part} />;
    }

    return null;
});

TermAgentMessagePartView.displayName = "TermAgentMessagePartView";

const TermAgentMessage = memo(({ message, isStreaming }: { message: WaveUIMessage; isStreaming: boolean }) => {
    const visibleParts = (message.parts ?? []).filter((part) => {
        return part.type === "text" || part.type === "reasoning" || part.type === "data-tooluse" || part.type === "data-toolprogress";
    });

    if (message.role === "user") {
        const text = visibleParts
            .filter((part) => part.type === "text")
            .map((part) => part.text ?? "")
            .join("\n\n");
        return (
            <div className="flex justify-end">
                <div className="max-w-[min(100%,44rem)] rounded-lg bg-zinc-700/70 px-3 py-2 text-sm text-white">
                    <div className="whitespace-pre-wrap break-words">{text}</div>
                </div>
            </div>
        );
    }

    if (visibleParts.length === 0 && isStreaming) {
        return (
            <div className="flex justify-start">
                <div className="rounded-lg px-3 py-2 text-sm text-zinc-400">Thinking...</div>
            </div>
        );
    }

    return (
        <div className="flex justify-start">
            <div className="min-w-[min(100%,44rem)] space-y-2 rounded-lg px-1 py-1">
                {visibleParts.map((part, idx) => (
                    <TermAgentMessagePartView key={`${part.type}:${idx}`} part={part} isStreaming={isStreaming} />
                ))}
            </div>
        </div>
    );
});

TermAgentMessage.displayName = "TermAgentMessage";

export const TermAgentChatProvider = memo(({ model }: { model: TermAgentModel }) => {
    const transport = useMemo(
        () =>
            new DefaultChatTransport({
                api: `${getWebServerEndpoint()}/api/post-agent-message`,
                prepareSendMessagesRequest: () => {
                    return {
                        body: {
                            msg: model.getAndClearTermAgentMessage(),
                            chatid: globalStore.get(model.termAgentChatId),
                            aimode: model.getTermAgentMode(),
                            modeloverride: model.getTermAgentModelOverride(),
                            planpath: model.getAndClearTermAgentPlanPath(),
                            tabid: model.tabModel.tabId,
                            blockid: model.blockId,
                            mode: model.getAndClearTermAgentPendingMode(),
                            context: model.getAndClearTermAgentPendingContext(),
                        },
                    };
                },
            }),
        [model]
    );

    const { messages, sendMessage, status, setMessages, stop } = useChat<WaveUIMessage>({
        transport,
        onError: (error) => {
            console.error("terminal agent error", error);
            model.setTermAgentError(error.message || "Terminal agent request failed.");
        },
    });

    useEffect(() => {
        model.registerTermAgentChat(sendMessage, setMessages, status, stop);
    }, [model, sendMessage, setMessages, status, stop]);

    useEffect(() => {
        if ("syncAgentMessages" in model) {
            (model as any).syncAgentMessages(messages, status);
        }
    }, [messages, status, model]);

    return null;
});

TermAgentChatProvider.displayName = "TermAgentChatProvider";

export const TermAgentOverlay = memo(({ model }: TermAgentOverlayProps) => {
    const visible = jotai.useAtomValue(model.termAgentVisible);
    const composerOpen = jotai.useAtomValue(model.termAgentComposerOpen);
    const inputValue = jotai.useAtomValue(model.termAgentInput);
    const errorText = jotai.useAtomValue(model.termAgentError);
    const agentMode = jotai.useAtomValue(model.termAgentAgentMode);
    const chatId = jotai.useAtomValue(model.termAgentChatId);
    const approvalCtx = useMemo<TermAgentApprovalContextValue>(
        () => ({ chatId, getCwd: () => model.getTermAgentCwd() }),
        [chatId, model]
    );
    const scrollRef = useRef<HTMLDivElement>(null);
    const composerInputRef = useRef<HTMLInputElement>(null);

    useEffect(() => {
        if (!visible) {
            return;
        }
        const scroller = scrollRef.current;
        if (!scroller) {
            return;
        }
        scroller.scrollTop = scroller.scrollHeight;
    }, [messages, status, visible]);

    const lastSeenPlanCallIdRef = useRef<string | null>(null);
    useEffect(() => {
        for (const msg of messages) {
            for (const part of msg.parts ?? []) {
                if (part.type !== "data-tooluse") continue;
                const data = part.data;
                if (data?.toolname !== "write_plan" || data?.status !== "completed") continue;
                const callId = data.toolcallid ?? `${msg.id}:${data.inputfilename ?? ""}`;
                if (callId === lastSeenPlanCallIdRef.current) continue;
                lastSeenPlanCallIdRef.current = callId;
                model.termAgentLastPlanPath = data.inputfilename ?? "plan";
            }
        }
    }, [messages, model]);

    const shouldRender = visible && (composerOpen || messages.length > 0 || status === "streaming" || status === "submitted" || errorText);
    if (!shouldRender) {
        return null;
    }

    const mode = agentMode;

    return (
        <TermAgentApprovalContext.Provider value={approvalCtx}>
        <div className="pointer-events-none absolute inset-x-3 bottom-3 z-[var(--zindex-block-mask-inner)] flex justify-center">
            <div className="pointer-events-auto w-full max-w-[860px] overflow-hidden rounded-2xl border border-zinc-700/80 bg-[rgb(16_16_18_/_0.94)] shadow-[0_24px_80px_rgba(0,0,0,0.45)] backdrop-blur-xl">
                <div className="flex items-center justify-between border-b border-zinc-800 px-3 py-2 text-xs text-zinc-400">
                    <div className="flex items-center gap-2">
                        <span className="font-medium text-zinc-100">Terminal Agent</span>
                        <span className="rounded-full border border-zinc-700 px-2 py-0.5">{mode}</span>
                        {status === "streaming" || status === "submitted" ? (
                            <span className="text-[var(--color-accent)]">Running</span>
                        ) : null}
                        <TermAgentTokenCounter messages={messages} />
                    </div>
                    <div className="flex items-center gap-2">
                        {model.termAgentLastPlanPath && status !== "streaming" && status !== "submitted" && (
                            <button
                                className="rounded border border-emerald-600/60 bg-emerald-500/10 px-2 py-1 text-emerald-200 transition-colors hover:bg-emerald-500/20 cursor-pointer"
                                onClick={() => model.executePlan()}
                            >
                                Execute Plan
                            </button>
                        )}
                        {messages.length > 0 && (
                            <button
                                className="rounded border border-zinc-700 px-2 py-1 text-zinc-300 transition-colors hover:border-zinc-500 hover:text-white cursor-pointer"
                                onClick={() => model.clearTermAgentSession()}
                            >
                                New
                            </button>
                        )}
                        {(status === "streaming" || status === "submitted") && (
                            <button
                                className="rounded border border-zinc-700 px-2 py-1 text-zinc-300 transition-colors hover:border-zinc-500 hover:text-white"
                                onClick={() => stop()}
                            >
                                Stop
                            </button>
                        )}
                        <button
                            className="rounded border border-zinc-700 px-2 py-1 text-zinc-300 transition-colors hover:border-zinc-500 hover:text-white"
                            onClick={() => model.hideTermAgentOverlay()}
                        >
                            Close
                        </button>
                    </div>
                </div>

                <div ref={scrollRef} className="max-h-[40vh] overflow-y-auto px-3 py-3">
                    {messages.length === 0 ? (
                        <div className="space-y-2 text-sm text-zinc-400">
                            <div className="text-zinc-100">Ask this terminal directly.</div>
                            <div>Examples: `:help me build this feature`, `:explain this code`, `:review this command output`</div>
                            <div>Type `new` to reset the session.</div>
                        </div>
                    ) : (
                        <div className="space-y-4">
                            {messages.map((message, index) => {
                                const isLastMessage = index === messages.length - 1;
                                const isStreaming = status === "streaming" && isLastMessage && message.role === "assistant";
                                return <TermAgentMessage key={message.id} message={message} isStreaming={isStreaming} />;
                            })}
                        </div>
                    )}
                </div>

                <div className="border-t border-zinc-800 px-3 py-3">
                    {composerOpen ? (
                        <div className="flex items-center gap-2 rounded-xl border border-zinc-700/80 bg-black/20 px-3 py-2 font-mono text-sm">
                            <span className="text-[var(--color-accent)]">:</span>
                            <input
                                ref={composerInputRef}
                                type="text"
                                className="flex-1 bg-transparent text-zinc-100 outline-none placeholder:text-zinc-500"
                                value={inputValue}
                                placeholder="ask hello / plan a feature / do run tests"
                                autoFocus
                                onChange={(e) => globalStore.set(model.termAgentInput, e.target.value)}
                                onKeyDown={(e) => {
                                    if (e.key === "Enter" && !e.shiftKey) {
                                        e.preventDefault();
                                        model.submitTermAgentPrompt();
                                    }
                                    if (e.key === "Escape") {
                                        e.preventDefault();
                                        model.closeTermAgentComposer();
                                    }
                                }}
                            />
                        </div>
                    ) : (
                        <div className="text-xs text-zinc-500">Press `:` at an empty prompt to continue this session.</div>
                    )}
                    <div className="mt-2 flex items-center justify-between text-[11px] text-zinc-500">
                        <div>{composerOpen ? "Enter to send, Esc to cancel." : "Esc closes the overlay."}</div>
                        <div>Tools still require approval.</div>
                    </div>
                    {errorText && <div className="mt-2 text-xs text-red-300">{errorText}</div>}
                </div>
            </div>
        </div>
        </TermAgentApprovalContext.Provider>
    );
});

TermAgentOverlay.displayName = "TermAgentOverlay";
