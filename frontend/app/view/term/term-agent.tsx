// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { WaveUIMessage, WaveUIMessagePart } from "@/app/aipanel/aitypes";
import { WaveStreamdown } from "@/app/element/streamdown";
import { globalStore } from "@/app/store/jotaiStore";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { getWebServerEndpoint } from "@/util/endpoints";
import { useChat } from "@ai-sdk/react";
import { DefaultChatTransport } from "ai";
import * as jotai from "jotai";
import { memo, useEffect, useMemo, useRef, useState } from "react";

const TermAgentCodeBlockMaxWidth = jotai.atom(720);

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
    getAndClearTermAgentPendingMode(): string;
    getAndClearTermAgentPendingContext(): any;
    registerTermAgentChat(
        sendMessage: any,
        setMessages: any,
        status: string,
        stop: () => void
    ): void;
    clearTermAgentSession(): void;
    hideTermAgentOverlay(): void;
    setTermAgentError(message: string | null): void;
};

type TermAgentOverlayProps = {
    model: TermAgentModel;
};

const TermAgentApprovalButtons = memo(({ toolCallId }: { toolCallId: string }) => {
    const [approvalOverride, setApprovalOverride] = useState<string | null>(null);

    const sendApproval = (approval: "user-approved" | "user-denied") => {
        setApprovalOverride(approval);
        RpcApi.WaveAIToolApproveCommand(TabRpcClient, {
            toolcallid: toolCallId,
            approval,
        });
    };

    return (
        <div className="mt-2 flex gap-2">
            <button
                className="rounded border border-emerald-500/60 px-2 py-1 text-xs text-emerald-200 transition-colors hover:bg-emerald-500/10"
                onClick={() => sendApproval("user-approved")}
            >
                {approvalOverride === "user-approved" ? "Approved" : "Approve"}
            </button>
            <button
                className="rounded border border-red-500/60 px-2 py-1 text-xs text-red-200 transition-colors hover:bg-red-500/10"
                onClick={() => sendApproval("user-denied")}
            >
                {approvalOverride === "user-denied" ? "Denied" : "Deny"}
            </button>
        </div>
    );
});

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
            {errorText && <div className="mt-2 text-xs text-red-300">{errorText}</div>}
            {approval === "needs-approval" && isStreaming && toolData?.toolcallid && (
                <TermAgentApprovalButtons toolCallId={toolData.toolcallid} />
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

const TermAgentMessagePartView = memo(({ part, isStreaming }: { part: WaveUIMessagePart; isStreaming: boolean }) => {
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

export const TermAgentOverlay = memo(({ model }: TermAgentOverlayProps) => {
    const visible = jotai.useAtomValue(model.termAgentVisible);
    const composerOpen = jotai.useAtomValue(model.termAgentComposerOpen);
    const inputValue = jotai.useAtomValue(model.termAgentInput);
    const errorText = jotai.useAtomValue(model.termAgentError);
    const agentMode = jotai.useAtomValue(model.termAgentAgentMode);
    const scrollRef = useRef<HTMLDivElement>(null);

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

    model.registerTermAgentChat(sendMessage, setMessages, status, stop);

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

    const shouldRender = visible && (composerOpen || messages.length > 0 || status === "streaming" || status === "submitted" || errorText);
    if (!shouldRender) {
        return null;
    }

    const mode = agentMode;

    return (
        <div className="pointer-events-none absolute inset-x-3 bottom-3 z-[var(--zindex-block-mask-inner)] flex justify-center">
            <div className="pointer-events-auto w-full max-w-[860px] overflow-hidden rounded-2xl border border-zinc-700/80 bg-[rgb(16_16_18_/_0.94)] shadow-[0_24px_80px_rgba(0,0,0,0.45)] backdrop-blur-xl">
                <div className="flex items-center justify-between border-b border-zinc-800 px-3 py-2 text-xs text-zinc-400">
                    <div className="flex items-center gap-2">
                        <span className="font-medium text-zinc-100">Terminal Agent</span>
                        <span className="rounded-full border border-zinc-700 px-2 py-0.5">{mode}</span>
                        {status === "streaming" || status === "submitted" ? (
                            <span className="text-[var(--color-accent)]">Running</span>
                        ) : null}
                    </div>
                    <div className="flex items-center gap-2">
                        {messages.length > 0 && (
                            <button
                                className="rounded border border-zinc-700 px-2 py-1 text-zinc-300 transition-colors hover:border-zinc-500 hover:text-white"
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
                        <div className="rounded-xl border border-zinc-700/80 bg-black/20 px-3 py-2 font-mono text-sm text-zinc-100">
                            <span className="mr-2 text-[var(--color-accent)]">:</span>
                            {inputValue.length > 0 ? (
                                <span className="whitespace-pre-wrap break-words">{inputValue}</span>
                            ) : (
                                <span className="text-zinc-500">help me build this feature</span>
                            )}
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
    );
});

TermAgentOverlay.displayName = "TermAgentOverlay";
