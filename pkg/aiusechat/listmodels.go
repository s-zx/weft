// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package aiusechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/wshrpc"
)

const listModelsTimeout = 15 * time.Second

// ListProviderModels resolves the right /models endpoint for the given
// provider settings, calls it with the user's API key, and returns a
// normalized slice the FE can render in a picker. Errors are returned
// verbatim — the FE shows them inline so the user can fix the inputs
// (bad key, wrong base URL, network) without leaving the settings panel.
func ListProviderModels(ctx context.Context, apiType, baseURL, apiToken string) ([]wshrpc.ProviderModelInfo, error) {
	apiType = strings.TrimSpace(apiType)
	baseURL = strings.TrimSpace(baseURL)
	apiToken = strings.TrimSpace(apiToken)
	if apiType == "" {
		return nil, fmt.Errorf("apitype is required")
	}
	if err := validateBaseURL(baseURL); err != nil {
		return nil, err
	}

	switch apiType {
	case uctypes.APIType_AnthropicMessages:
		return listAnthropicModels(ctx, baseURL, apiToken)
	case uctypes.APIType_GoogleGemini:
		return listGeminiModels(ctx, baseURL, apiToken)
	case uctypes.APIType_OpenAIChat, uctypes.APIType_OpenAIResponses:
		return listOpenAICompatibleModels(ctx, baseURL, apiToken)
	default:
		// Unknown apiType — best-effort try the OpenAI-compatible path.
		return listOpenAICompatibleModels(ctx, baseURL, apiToken)
	}
}

// validateBaseURL rejects baseurls that aren't plain http(s). Without
// this guard a typo or malicious settings edit could send the user's
// API key (Authorization / x-api-key / ?key=) to a non-http scheme
// or — worse — to file://, gopher://, etc. Empty is allowed; each
// per-provider helper supplies a sane default in that case.
func validateBaseURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid baseurl: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("baseurl must use http or https scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("baseurl must include a host")
	}
	return nil
}

// modelsURLFromChatURL takes a provider's *chat* endpoint (the value the
// user actually configures) and converts it to the corresponding /models
// endpoint. It strips the well-known operation suffixes and appends
// "/models" — works for OpenAI / OpenRouter / Together / Mistral / Groq /
// DeepSeek / Anthropic. Returns the input unchanged if no transformation
// applies (caller decides what to do).
func modelsURLFromChatURL(chatURL string) string {
	if chatURL == "" {
		return ""
	}
	s := strings.TrimRight(chatURL, "/")
	for _, suffix := range []string{
		"/chat/completions",
		"/responses",
		"/messages",
		"/completions",
	} {
		if strings.HasSuffix(s, suffix) {
			s = strings.TrimSuffix(s, suffix)
			return s + "/models"
		}
	}
	if strings.HasSuffix(s, "/models") {
		return s
	}
	return s + "/models"
}

func listOpenAICompatibleModels(ctx context.Context, baseURL, apiToken string) ([]wshrpc.ProviderModelInfo, error) {
	endpoint := modelsURLFromChatURL(baseURL)
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/models"
	}
	req, err := http.NewRequestWithContext(withTimeout(ctx), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+apiToken)
	}
	body, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	// OpenRouter and OpenAI both return {data: [...]}, with OpenRouter
	// adding name/description/context_length fields on top of the OpenAI
	// shape. One decode path covers both.
	var resp struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Description   string `json:"description"`
			ContextLength int    `json:"context_length"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode /models response: %w", err)
	}
	out := make([]wshrpc.ProviderModelInfo, 0, len(resp.Data))
	for _, m := range resp.Data {
		if m.ID == "" {
			continue
		}
		out = append(out, wshrpc.ProviderModelInfo{
			ID:          m.ID,
			Name:        m.Name,
			Description: m.Description,
			Context:     m.ContextLength,
		})
	}
	sortModels(out)
	return out, nil
}

func listAnthropicModels(ctx context.Context, baseURL, apiToken string) ([]wshrpc.ProviderModelInfo, error) {
	if apiToken == "" {
		return nil, fmt.Errorf("Anthropic /models requires an API key")
	}
	endpoint := modelsURLFromChatURL(baseURL)
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/models"
	}
	req, err := http.NewRequestWithContext(withTimeout(ctx), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", apiToken)
	req.Header.Set("anthropic-version", "2023-06-01")
	body, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode /models response: %w", err)
	}
	out := make([]wshrpc.ProviderModelInfo, 0, len(resp.Data))
	for _, m := range resp.Data {
		if m.ID == "" {
			continue
		}
		out = append(out, wshrpc.ProviderModelInfo{
			ID:   m.ID,
			Name: m.DisplayName,
		})
	}
	sortModels(out)
	return out, nil
}

func listGeminiModels(ctx context.Context, baseURL, apiToken string) ([]wshrpc.ProviderModelInfo, error) {
	if apiToken == "" {
		return nil, fmt.Errorf("Gemini /models requires an API key")
	}
	// Gemini puts the key in the query string. The chat URL is per-model
	// (.../models/<id>:streamGenerateContent), so derive the v1beta root
	// by snipping at /models, then list at /models with ?key=...
	endpoint := geminiModelsURL(baseURL)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid Gemini endpoint: %w", err)
	}
	q := parsed.Query()
	q.Set("key", apiToken)
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(withTimeout(ctx), http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	body, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Models []struct {
			Name             string   `json:"name"`
			DisplayName      string   `json:"displayName"`
			Description      string   `json:"description"`
			InputTokenLimit  int      `json:"inputTokenLimit"`
			SupportedMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode /models response: %w", err)
	}
	out := make([]wshrpc.ProviderModelInfo, 0, len(resp.Models))
	for _, m := range resp.Models {
		// Skip non-generative models (embedding, etc.) — they would 4xx
		// if the user picked one for chat.
		if !supportsGeneration(m.SupportedMethods) {
			continue
		}
		id := strings.TrimPrefix(m.Name, "models/")
		if id == "" {
			continue
		}
		out = append(out, wshrpc.ProviderModelInfo{
			ID:          id,
			Name:        m.DisplayName,
			Description: m.Description,
			Context:     m.InputTokenLimit,
		})
	}
	sortModels(out)
	return out, nil
}

func geminiModelsURL(chatURL string) string {
	if chatURL == "" {
		return "https://generativelanguage.googleapis.com/v1beta/models"
	}
	s := strings.TrimRight(chatURL, "/")
	if idx := strings.Index(s, "/models"); idx >= 0 {
		return s[:idx] + "/models"
	}
	return s + "/models"
}

func supportsGeneration(methods []string) bool {
	for _, m := range methods {
		if m == "generateContent" || m == "streamGenerateContent" {
			return true
		}
	}
	return false
}

func withTimeout(ctx context.Context) context.Context {
	if _, ok := ctx.Deadline(); ok {
		return ctx
	}
	c, _ := context.WithTimeout(ctx, listModelsTimeout)
	return c
}

func doRequest(req *http.Request) ([]byte, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Surface the upstream body — providers usually return a JSON
		// error envelope worth showing the user verbatim.
		snippet := string(body)
		if len(snippet) > 500 {
			snippet = snippet[:500] + "..."
		}
		return nil, fmt.Errorf("provider returned %d: %s", resp.StatusCode, snippet)
	}
	return body, nil
}

func sortModels(models []wshrpc.ProviderModelInfo) {
	sort.SliceStable(models, func(i, j int) bool {
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})
}
