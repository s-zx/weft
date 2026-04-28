// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { atoms } from "@/store/global";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import type { WaveConfigViewModel } from "@/app/view/waveconfig/waveconfig-model";
import { cn } from "@/util/util";
import { useAtomValue } from "jotai";
import { memo, useCallback, useEffect, useRef, useState } from "react";

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
        endpoint: "https://api.openai.com/v1/chat/completions",
        secretName: "OPENAI_KEY",
        modelPlaceholder: "gpt-4o",
    },
    openrouter: {
        label: "OpenRouter",
        apitype: "openai-chat",
        endpoint: "https://openrouter.ai/api/v1/chat/completions",
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
        endpoint: "https://api.anthropic.com/v1/messages",
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
    // Advanced fields are write-only overrides — empty input means
    // "I'm not touching this, leave settings alone". They never reflect
    // the saved value, so opening Settings → Save (no edits) doesn't
    // stamp defaults the user never explicitly chose.
    const [baseUrl, setBaseUrl] = useState("");
    const [maxTokens, setMaxTokens] = useState("");
    const [showAdvanced, setShowAdvanced] = useState(false);
    const [saving, setSaving] = useState(false);
    const [saveMsg, setSaveMsg] = useState<string | null>(null);

    // Combobox state for the Model field. The Model input doubles as the
    // value AND the filter — typing narrows the list, clicking an item
    // overwrites the value. `models` is fetched lazily on first focus and
    // cached per (provider, baseUrl, apiKey) tuple so re-opening is free.
    const [comboOpen, setComboOpen] = useState(false);
    const [comboLoading, setComboLoading] = useState(false);
    const [comboError, setComboError] = useState<string | null>(null);
    const [models, setModels] = useState<ProviderModelInfo[]>([]);
    const [activeIdx, setActiveIdx] = useState(-1);
    const lastFetchKey = useRef<string>("");
    const comboWrapRef = useRef<HTMLDivElement | null>(null);
    const inputRef = useRef<HTMLInputElement | null>(null);
    const listRef = useRef<HTMLDivElement | null>(null);

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
        // Clear baseUrl on provider switch — placeholder will show the
        // new provider's default. User can still type a custom URL.
        setBaseUrl("");
        setApiKey("");
        setApiKeyShown(false);
        setApiKeyLoaded(true);
        setSaveMsg(null);
        setComboOpen(false);
        setModels([]);
        setComboError(null);
        lastFetchKey.current = "";
    }, []);

    // Fetch lazily on first focus. Re-fetch automatically only when the
    // tuple of inputs that *changes the URL/auth* changes — moving from
    // OpenAI to OpenRouter, swapping keys, etc. Re-opening with the same
    // tuple replays the cached list instantly.
    const fetchModelsIfNeeded = useCallback(async () => {
        const def = ProviderDefs[provider] ?? ProviderDefs["custom"];
        const effectiveBase = baseUrl || def.endpoint;
        const key = `${def.apitype}|${effectiveBase}|${apiKey}`;
        if (key === lastFetchKey.current) return;
        lastFetchKey.current = key;
        setComboError(null);
        setComboLoading(true);
        try {
            const result = await RpcApi.ListProviderModelsCommand(TabRpcClient, {
                apitype: def.apitype,
                baseurl: effectiveBase,
                apitoken: apiKey,
            });
            setModels(result.models ?? []);
        } catch (e: any) {
            // Reset cache key on error so the next focus retries instead
            // of silently reusing the empty list. Without this, fixing a
            // bad API key wouldn't refetch.
            lastFetchKey.current = "";
            setComboError(e?.message ?? "failed to fetch models");
            setModels([]);
        } finally {
            setComboLoading(false);
        }
    }, [provider, baseUrl, apiKey]);

    // Rank tiers (lower = better). Within each tier we keep the
    // backend's alphabetical order so the relative order of e.g.
    // "openai/gpt-4o", "openai/gpt-4o-mini" stays sensible.
    //   0  id == query                    (perfect hit, e.g. typing exactly "openai/gpt-4o")
    //   1  id starts with query           (best partial — what people usually mean)
    //   2  id contains query              (substring, less specific)
    //   3  name starts with query
    //   4  name contains query
    //   5  description contains query     (loosest — kept so descriptive searches still work)
    //   ∞  no match                       (filtered out)
    const filteredModels = (() => {
        if (!modelName) return models;
        const q = modelName.toLowerCase();
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
        scored.sort((a, b) => (a.tier - b.tier) || (a.idx - b.idx));
        return scored.map((s) => s.m);
    })();

    const openCombo = useCallback(() => {
        setComboOpen(true);
        // Pre-select the current model in the list so Enter immediately
        // reaffirms the saved choice instead of accidentally jumping to
        // the first item. Only highlight when the value matches an item;
        // otherwise leave activeIdx at -1 so Enter just closes.
        const idx = filteredModels.findIndex((m) => m.id === modelName);
        setActiveIdx(idx);
        void fetchModelsIfNeeded();
    }, [fetchModelsIfNeeded, filteredModels, modelName]);

    // Close on outside click. Capture phase so it fires before the
    // input's own onMouseDown can re-open us.
    useEffect(() => {
        if (!comboOpen) return;
        const onDocMouseDown = (e: MouseEvent) => {
            const wrap = comboWrapRef.current;
            if (wrap && !wrap.contains(e.target as Node)) {
                setComboOpen(false);
            }
        };
        document.addEventListener("mousedown", onDocMouseDown, true);
        return () => document.removeEventListener("mousedown", onDocMouseDown, true);
    }, [comboOpen]);

    // Keep the active item visible when arrow-keying past the viewport.
    useEffect(() => {
        if (!comboOpen || activeIdx < 0) return;
        const list = listRef.current;
        if (!list) return;
        const el = list.querySelector<HTMLElement>(`[data-combo-idx="${activeIdx}"]`);
        if (el) el.scrollIntoView({ block: "nearest" });
    }, [activeIdx, comboOpen]);

    const pickModel = useCallback((id: string) => {
        setModelName(id);
        setComboOpen(false);
        setActiveIdx(-1);
        // Defer focus restoration so the click event finishes first.
        setTimeout(() => inputRef.current?.focus(), 0);
    }, []);

    const onModelInputKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === "ArrowDown") {
            e.preventDefault();
            if (!comboOpen) {
                openCombo();
                return;
            }
            setActiveIdx((i) => Math.min(filteredModels.length - 1, i + 1));
        } else if (e.key === "ArrowUp") {
            e.preventDefault();
            if (!comboOpen) return;
            setActiveIdx((i) => Math.max(0, i - 1));
        } else if (e.key === "Enter") {
            if (comboOpen && activeIdx >= 0 && filteredModels[activeIdx]) {
                e.preventDefault();
                pickModel(filteredModels[activeIdx].id);
            } else if (comboOpen) {
                setComboOpen(false);
            }
        } else if (e.key === "Escape") {
            if (comboOpen) {
                e.preventDefault();
                setComboOpen(false);
            }
        }
    };

    const handleSave = useCallback(async () => {
        if (provider === "custom" && !baseUrl.trim()) {
            setSaveMsg("Error: Base URL is required for Custom providers");
            return;
        }
        setSaving(true);
        setSaveMsg(null);
        try {
            const def = ProviderDefs[provider] ?? ProviderDefs["custom"];
            const secretName = provider === "custom" ? "" : def.secretName;

            if (secretName && apiKey) {
                await RpcApi.SetSecretsCommand(TabRpcClient, { [secretName]: apiKey });
            }

            const configUpdate: Record<string, any> = {
                "ai:apitype": def.apitype,
                "ai:model": modelName || null,
            };

            // Advanced overrides — write only when the user actually
            // typed a value. Empty input is a no-op, never a clear.
            if (baseUrl) {
                configUpdate["ai:baseurl"] = baseUrl;
            }
            if (maxTokens) {
                const parsed = parseInt(maxTokens, 10);
                if (!isNaN(parsed) && parsed > 0) {
                    configUpdate["ai:maxtokens"] = parsed;
                }
            }

            if (secretName) {
                configUpdate["ai:apitokensecretname"] = secretName;
                configUpdate["ai:apitoken"] = null;
            } else if (apiKey) {
                configUpdate["ai:apitoken"] = apiKey;
                configUpdate["ai:apitokensecretname"] = null;
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

            {provider === "custom" && (
                <div className="flex flex-col gap-2">
                    <label className={labelClass}>
                        Base URL <span className="text-red-400">*</span>
                    </label>
                    <input
                        type="text"
                        className={inputClass + " font-mono"}
                        value={baseUrl}
                        onChange={(e) => setBaseUrl(e.target.value)}
                        placeholder="https://api.example.com/v1/chat/completions"
                    />
                    <div className="text-xs text-zinc-500">
                        Required for Custom providers — the full chat-completions URL.
                    </div>
                </div>
            )}

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
                <div className="relative" ref={comboWrapRef}>
                    <input
                        ref={inputRef}
                        type="text"
                        className={inputClass + " font-mono pr-9"}
                        value={modelName}
                        onChange={(e) => {
                            setModelName(e.target.value);
                            if (!comboOpen) openCombo();
                            setActiveIdx(-1);
                        }}
                        onFocus={openCombo}
                        onMouseDown={() => {
                            if (!comboOpen) openCombo();
                        }}
                        onKeyDown={onModelInputKeyDown}
                        placeholder={providerDef.modelPlaceholder}
                        autoComplete="off"
                        spellCheck={false}
                        role="combobox"
                        aria-expanded={comboOpen}
                        aria-controls="model-combobox-list"
                    />
                    <i
                        className={cn(
                            "fa-sharp fa-solid fa-chevron-down text-xs text-zinc-500 absolute right-3 top-1/2 -translate-y-1/2 pointer-events-none transition-transform",
                            comboOpen && "rotate-180"
                        )}
                    />
                    {comboOpen && (
                        <div
                            id="model-combobox-list"
                            ref={listRef}
                            className="absolute left-0 right-0 top-full mt-1 z-10 border border-zinc-700 rounded bg-zinc-900 shadow-xl max-h-72 overflow-y-auto"
                            role="listbox"
                        >
                            {comboLoading && (
                                <div className="px-3 py-3 text-sm text-zinc-400 flex items-center gap-2">
                                    <i className="fa-sharp fa-solid fa-spinner fa-spin text-xs" />
                                    Loading models...
                                </div>
                            )}
                            {comboError && !comboLoading && (
                                <div className="px-3 py-3 text-sm text-red-400">{comboError}</div>
                            )}
                            {!comboError && !comboLoading && filteredModels.length === 0 && (
                                <div className="px-3 py-3 text-sm text-zinc-500">
                                    {models.length === 0 ? "No models returned." : "No matches."}
                                </div>
                            )}
                            {!comboLoading &&
                                filteredModels.map((m, idx) => (
                                    <button
                                        key={m.id}
                                        type="button"
                                        data-combo-idx={idx}
                                        onMouseDown={(e) => e.preventDefault()}
                                        onClick={() => pickModel(m.id)}
                                        onMouseEnter={() => setActiveIdx(idx)}
                                        role="option"
                                        aria-selected={idx === activeIdx}
                                        className={cn(
                                            "w-full text-left px-3 py-2 transition-colors cursor-pointer border-b border-zinc-800 last:border-b-0",
                                            idx === activeIdx ? "bg-zinc-800" : "hover:bg-zinc-800/60",
                                            m.id === modelName && "ring-1 ring-inset ring-[var(--color-accent)]/40"
                                        )}
                                    >
                                        <div className="font-mono text-xs text-zinc-200">{m.id}</div>
                                        {(m.name || m.context > 0) && (
                                            <div className="text-[11px] text-zinc-500 mt-0.5 flex items-center gap-2">
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
                    )}
                </div>
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
