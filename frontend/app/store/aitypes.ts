// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { ChatRequestOptions, FileUIPart, UIMessage, UIMessagePart } from "ai";

export type SuggestedRule = {
    toolname: string;
    content?: string;
    display: string;
};

export type ApprovalDestination = "session" | "localProject" | "sharedProject" | "user";

type WaveUIDataTypes = {
    userfile: {
        filename: string;
        size: number;
        mimetype: string;
        previewurl?: string;
    };
    tooluse: {
        toolcallid: string;
        toolname: string;
        tooldesc: string;
        status: "pending" | "error" | "completed";
        runts?: number;
        errormessage?: string;
        approval?: "needs-approval" | "user-approved" | "user-denied" | "auto-approved" | "timeout";
        blockid?: string;
        writebackupfilename?: string;
        inputfilename?: string;
        originalcontent?: string;
        modifiedcontent?: string;
        // Populated when approval == "needs-approval" and the permissions
        // engine emitted "remember this" suggestions. Empty/missing on
        // auto-approved or already-decided calls.
        suggestions?: SuggestedRule[];
    };
    toolprogress: {
        toolcallid: string;
        toolname: string;
        statuslines: string[];
    };
};

export type WaveUIMessage = UIMessage<unknown, WaveUIDataTypes, any>;
export type WaveUIMessagePart = UIMessagePart<WaveUIDataTypes, any>;

export type UseChatSetMessagesType = (
    messages: WaveUIMessage[] | ((messages: WaveUIMessage[]) => WaveUIMessage[])
) => void;

export type UseChatSendMessageType = (
    message?:
        | (Omit<WaveUIMessage, "id" | "role"> & {
              id?: string;
              role?: "system" | "user" | "assistant";
          } & {
              text?: never;
              files?: never;
              messageId?: string;
          })
        | {
              text: string;
              files?: FileList | FileUIPart[];
              metadata?: unknown;
              parts?: never;
              messageId?: string;
          }
        | {
              files: FileList | FileUIPart[];
              metadata?: unknown;
              parts?: never;
              messageId?: string;
          },
    options?: ChatRequestOptions
) => Promise<void>;
