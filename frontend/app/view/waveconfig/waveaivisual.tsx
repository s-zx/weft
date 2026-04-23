// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { atoms } from "@/store/global";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import type { WaveConfigViewModel } from "@/app/view/waveconfig/waveconfig-model";
import { useAtomValue } from "jotai";
import { memo, useCallback, useEffect, useState } from "react";

type ProviderDef = {
    label: string;
    apitype: string;
    endpoint: string;
    secretName: string;
    modelPlaceholder: string;
};

const ProviderDefs: Record<string, ProviderDef> = {
    openai: {
        label: "OpenAI",
        apitype: "openai-chat",
        endpoint: "",
        secretName: "OPENAI_KEY",
        modelPlaceholder: "gpt-4o",
    },
    openrouter: {
        label: "OpenRouter",
        apitype: "openai-chat",
        endpoint: "https://openrouter.ai/api/v1",
        secretName: "OPENROUTER_KEY",
        modelPlaceholder: "anthropic/claude-sonnet-4-20250514",
    },
    google: {
        label: "Google Gemini",
        apitype: "google-gemini",
        endpoint: "",
        secretName: "GOOGLE_AI_KEY",
        modelPlaceholder: "gemini-2.5-pro",
    },
    anthropic: {
        label: "Anthropic",
        apitype: "anthropic-messages",
        endpoint: "https://api.anthropic.com",
        secretName: "ANTHROPIC_KEY",
        modelPlaceholder: "claude-sonnet-4-20250514",
    },
    custom: {
        label: "Custom (OpenAI-compatible)",
        apitype: "openai-chat",
        endpoint: "",
        secretName: "",
        modelPlaceholder: "model-name",
    },
};

const ProviderOrder = ["openai", "openrouter", "anthropic", "google", "custom"];

function detectProvider(settings: SettingsType): string {
    const baseUrl = settings?.["ai:baseurl"] ?? "";
    if (baseUrl.includes("openrouter.ai")) return "openrouter";
    if (baseUrl.includes("anthropic.com")) return "anthropic";
    if (baseUrl.includes("generativelanguage.googleapis.com")) return "google";
    const apiType = settings?.["ai:apitype"] ?? "";
    if (apiType === "anthropic-messages") return "anthropic";
    if (apiType === "google-gemini") return "google";
    if (baseUrl && apiType) return "custom";
    return "openrouter";
}

interface WaveAIVisualContentProps {
    model: WaveConfigViewModel;
}

export const WaveAIVisualContent = memo(({ model }: WaveAIVisualContentProps) => {
    const settings = useAtomValue(atoms.settingsAtom);

    const [provider, setProvider] = useState(() => detectProvider(settings));
    const [apiKey, setApiKey] = useState("");
    const [apiKeyShown, setApiKeyShown] = useState(false);
    const [apiKeyLoaded, setApiKeyLoaded] = useState(false);
    const [modelName, setModelName] = useState(() => settings?.["ai:model"] ?? "");
    const [baseUrl, setBaseUrl] = useState(() => {
        const saved = settings?.["ai:baseurl"] ?? "";
        if (saved) return saved;
        const detected = detectProvider(settings);
        return ProviderDefs[detected]?.endpoint ?? "";
    });
    const [maxTokens, setMaxTokens] = useState(() => String(settings?.["ai:maxtokens"] ?? ""));
    const [showAdvanced, setShowAdvanced] = useState(false);
    const [saving, setSaving] = useState(false);
    const [saveMsg, setSaveMsg] = useState<string | null>(null);

    const providerDef = ProviderDefs[provider] ?? ProviderDefs["custom"];

    const loadApiKey = useCallback(async (secretName: string) => {
        if (!secretName) {
            setApiKeyLoaded(true);
            return;
        }
        try {
            const secrets = await RpcApi.GetSecretsCommand(TabRpcClient, [secretName]);
            if (secrets?.[secretName]) {
                setApiKey(secrets[secretName]);
                setApiKeyShown(true);
            }
            setApiKeyLoaded(true);
        } catch {
            setApiKeyLoaded(true);
        }
    }, []);

    useEffect(() => {
        const secretName = settings?.["ai:apitokensecretname"] ?? providerDef.secretName;
        if (secretName) {
            loadApiKey(secretName);
        } else {
            const plainToken = settings?.["ai:apitoken"] ?? "";
            if (plainToken) {
                setApiKey(plainToken);
                setApiKeyShown(true);
            }
            setApiKeyLoaded(true);
        }
    }, []);

    const handleProviderChange = useCallback((newProvider: string) => {
        setProvider(newProvider);
        const def = ProviderDefs[newProvider] ?? ProviderDefs["custom"];
        setBaseUrl(def.endpoint);
        setApiKey("");
        setApiKeyShown(false);
        setApiKeyLoaded(true);
        setSaveMsg(null);
    }, []);

    const handleSave = useCallback(async () => {
        setSaving(true);
        setSaveMsg(null);
        try {
            const def = ProviderDefs[provider] ?? ProviderDefs["custom"];
            const secretName = provider === "custom" ? "" : def.secretName;

            if (secretName && apiKey) {
                await RpcApi.SetSecretsCommand(TabRpcClient, { [secretName]: apiKey });
            }

            const effectiveBaseUrl = baseUrl || def.endpoint || null;
            const configUpdate: Record<string, any> = {
                "ai:apitype": def.apitype,
                "ai:model": modelName || null,
                "ai:baseurl": effectiveBaseUrl,
            };

            if (secretName) {
                configUpdate["ai:apitokensecretname"] = secretName;
                configUpdate["ai:apitoken"] = null;
            } else if (apiKey) {
                configUpdate["ai:apitoken"] = apiKey;
                configUpdate["ai:apitokensecretname"] = null;
            }

            if (maxTokens) {
                const parsed = parseInt(maxTokens, 10);
                if (!isNaN(parsed) && parsed > 0) {
                    configUpdate["ai:maxtokens"] = parsed;
                }
            } else {
                configUpdate["ai:maxtokens"] = null;
            }

            await RpcApi.SetConfigCommand(TabRpcClient, configUpdate as any);
            setSaveMsg("Saved");
            setTimeout(() => setSaveMsg(null), 3000);
        } catch (e: any) {
            setSaveMsg("Error: " + (e?.message ?? "save failed"));
        } finally {
            setSaving(false);
        }
    }, [provider, apiKey, modelName, baseUrl, maxTokens]);

    const inputClass =
        "w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-sm focus:outline-none focus:border-[var(--color-accent)] transition-colors";
    const labelClass = "text-sm font-medium text-zinc-300";

    return (
        <div className="flex flex-col gap-6 p-6 h-full overflow-y-auto">
            <div>
                <div className="text-lg font-semibold text-zinc-100">AI Provider</div>
                <div className="mt-1 text-sm text-zinc-400">
                    Configure which AI service powers the terminal agent and chat.
                </div>
            </div>

            <div className="flex flex-col gap-2">
                <label className={labelClass}>Provider</label>
                <select
                    className={inputClass + " cursor-pointer"}
                    value={provider}
                    onChange={(e) => handleProviderChange(e.target.value)}
                >
                    {ProviderOrder.map((key) => (
                        <option key={key} value={key}>
                            {ProviderDefs[key].label}
                        </option>
                    ))}
                </select>
            </div>

            <div className="flex flex-col gap-2">
                <label className={labelClass}>API Key</label>
                <div className="relative">
                    <input
                        type={apiKeyShown ? "text" : "password"}
                        className={inputClass + " pr-10 font-mono"}
                        value={apiKey}
                        onChange={(e) => setApiKey(e.target.value)}
                        placeholder={apiKeyLoaded ? "Enter API key..." : "Loading..."}
                    />
                    <button
                        className="absolute right-2 top-1/2 -translate-y-1/2 text-zinc-400 hover:text-zinc-200 cursor-pointer p-1"
                        onClick={() => setApiKeyShown(!apiKeyShown)}
                        title={apiKeyShown ? "Hide" : "Show"}
                    >
                        <i className={`fa-sharp fa-solid ${apiKeyShown ? "fa-eye-slash" : "fa-eye"} text-xs`} />
                    </button>
                </div>
                {providerDef.secretName && (
                    <div className="text-xs text-zinc-500">
                        Stored securely as <code className="text-zinc-400">{providerDef.secretName}</code> in your OS
                        keychain.
                    </div>
                )}
            </div>

            <div className="flex flex-col gap-2">
                <label className={labelClass}>Model</label>
                <input
                    type="text"
                    className={inputClass + " font-mono"}
                    value={modelName}
                    onChange={(e) => setModelName(e.target.value)}
                    placeholder={providerDef.modelPlaceholder}
                />
            </div>

            <div className="flex flex-col gap-2">
                <button
                    className="flex items-center gap-2 text-sm text-zinc-400 hover:text-zinc-200 cursor-pointer w-fit"
                    onClick={() => setShowAdvanced(!showAdvanced)}
                >
                    <i
                        className={`fa-sharp fa-solid fa-chevron-right text-xs transition-transform ${showAdvanced ? "rotate-90" : ""}`}
                    />
                    Advanced
                </button>
                {showAdvanced && (
                    <div className="ml-4 flex flex-col gap-4 pt-2">
                        <div className="flex flex-col gap-2">
                            <label className={labelClass}>Base URL</label>
                            <input
                                type="text"
                                className={inputClass + " font-mono"}
                                value={baseUrl}
                                onChange={(e) => setBaseUrl(e.target.value)}
                                placeholder={providerDef.endpoint || "https://api.example.com/v1"}
                            />
                            <div className="text-xs text-zinc-500">
                                Override the default endpoint. Leave empty to use the provider default.
                            </div>
                        </div>
                        <div className="flex flex-col gap-2">
                            <label className={labelClass}>Max Tokens</label>
                            <input
                                type="text"
                                className={inputClass}
                                value={maxTokens}
                                onChange={(e) => setMaxTokens(e.target.value.replace(/\D/g, ""))}
                                placeholder="4096"
                            />
                        </div>
                    </div>
                )}
            </div>

            <div className="flex items-center gap-3 pt-2">
                <button
                    className="px-5 py-2 bg-accent/80 text-primary rounded hover:bg-accent transition-colors cursor-pointer disabled:opacity-50"
                    onClick={handleSave}
                    disabled={saving}
                >
                    {saving ? "Saving..." : "Save"}
                </button>
                {saveMsg && (
                    <span
                        className={`text-sm ${saveMsg.startsWith("Error") ? "text-red-400" : "text-emerald-400"}`}
                    >
                        {saveMsg}
                    </span>
                )}
            </div>
        </div>
    );
});

WaveAIVisualContent.displayName = "WaveAIVisualContent";
