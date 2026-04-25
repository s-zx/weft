// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/s-zx/crest/pkg/aiusechat"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/secretstore"
	"github.com/s-zx/crest/pkg/waveobj"
	"github.com/s-zx/crest/pkg/wconfig"
	"github.com/s-zx/crest/pkg/web/sse"
	"github.com/s-zx/crest/pkg/wstore"
)

// PostAgentMessageRequest is the body shape for POST /api/post-agent-message.
// The frontend sends the user's message plus the terminal context it already
// has (cwd, connection, last command, recent commands). Mode prefix parsing
// happens client-side — this handler just reads the final mode.
type PostAgentMessageRequest struct {
	ChatID        string            `json:"chatid"`
	TabId         string            `json:"tabid"`
	BlockId       string            `json:"blockid"`
	Mode          string            `json:"mode"`
	AIMode        string            `json:"aimode"`
	ModelOverride string            `json:"modeloverride,omitempty"`
	PlanPath      string            `json:"planpath,omitempty"`
	Msg           uctypes.AIMessage `json:"msg"`
	Context       AgentContext      `json:"context,omitempty"`
}

type AgentContext struct {
	Cwd         string   `json:"cwd,omitempty"`
	Connection  string   `json:"connection,omitempty"`
	LastCommand string   `json:"last_command,omitempty"`
	RecentCmds  []string `json:"recent_cmds,omitempty"`
}

// resolveAgentAIOpts tries the waveai mode system first (for users with
// waveai.json modes configured). If that fails — e.g. because the mode is a
// cloud mode requiring telemetry, or the user only configured settings.json —
// it falls back to building AIOptsType directly from the global AI settings.
func resolveAgentAIOpts(tabId string, aiMode string) (*uctypes.AIOptsType, error) {
	if aiMode != "" {
		rtInfo := &waveobj.ObjRTInfo{}
		if tabId != "" {
			oref := waveobj.MakeORef(waveobj.OType_Tab, tabId)
			if gotInfo := wstore.GetRTInfo(oref); gotInfo != nil {
				rtInfo = gotInfo
			}
		}
		opts, err := aiusechat.GetWaveAISettings(*rtInfo, aiMode)
		if err == nil {
			return opts, nil
		}
	}
	return buildAIOptsFromSettings()
}

func buildAIOptsFromSettings() (*uctypes.AIOptsType, error) {
	fullConfig := wconfig.GetWatcher().GetFullConfig()
	settings := fullConfig.Settings
	apiType := settings.AiApiType
	baseUrl := settings.AiBaseURL
	model := settings.AiModel
	if apiType == "" {
		apiType = "openai-chat"
	}
	apiToken := settings.AiApiToken
	if apiToken == "" && settings.AiApiTokenSecretName != "" {
		secret, exists, err := secretstore.GetSecret(settings.AiApiTokenSecretName)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve secret %s: %w", settings.AiApiTokenSecretName, err)
		}
		secret = strings.TrimSpace(secret)
		if !exists || secret == "" {
			return nil, fmt.Errorf("secret %s not found or empty — configure your API key in Settings → AI Provider", settings.AiApiTokenSecretName)
		}
		apiToken = secret
	}
	if apiToken == "" {
		return nil, fmt.Errorf("no API key configured — open Settings → AI Provider to set one up")
	}
	if baseUrl == "" {
		return nil, fmt.Errorf("no ai:baseurl configured — open Settings → AI Provider to set one up")
	}
	return &uctypes.AIOptsType{
		APIType:       apiType,
		Model:         model,
		Endpoint:      baseUrl,
		APIToken:      apiToken,
		MaxTokens:     4096,
		ThinkingLevel: uctypes.ThinkingLevelMedium,
		Verbosity:     uctypes.VerbosityLevelMedium,
		Capabilities:  []string{uctypes.AICapabilityTools},
	}, nil
}

func AgentWorktreeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action string `json:"action"`
		Cwd    string `json:"cwd"`
		Name   string `json:"name,omitempty"`
		Path   string `json:"path,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch req.Action {
	case "create":
		wt, err := MakeWorktree(req.Cwd, req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"name":   wt.Name,
			"path":   wt.Path,
			"branch": wt.BranchName,
		})

	case "remove":
		if req.Path == "" {
			http.Error(w, "path required for remove", http.StatusBadRequest)
			return
		}
		wt := &Worktree{Path: req.Path, RepoRoot: req.Cwd}
		if err := wt.Remove(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})

	case "status":
		if req.Path == "" {
			http.Error(w, "path required for status", http.StatusBadRequest)
			return
		}
		wt := &Worktree{Path: req.Path}
		json.NewEncoder(w).Encode(map[string]any{
			"has_changes": wt.HasChanges(),
		})

	default:
		http.Error(w, fmt.Sprintf("unknown action: %s (valid: create, remove, status)", req.Action), http.StatusBadRequest)
	}
}

func AgentRewindHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ChatID       string `json:"chatid"`
		CheckpointID string `json:"checkpointid,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}
	if req.ChatID == "" {
		http.Error(w, "chatid required", http.StatusBadRequest)
		return
	}
	chatId := AgentChatStorePrefix + req.ChatID
	var restored int
	var err error
	if req.CheckpointID != "" {
		restored, err = DefaultCheckpointStore.RewindTo(chatId, req.CheckpointID)
	} else {
		restored, err = DefaultCheckpointStore.RewindLast(chatId)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"restored": restored})
}

// PostAgentMessageHandler is the HTTP entrypoint for the native agent.
// Wired in pkg/web/web.go at /api/post-agent-message.
func PostAgentMessageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PostAgentMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.ChatID == "" {
		http.Error(w, "chatid is required in request body", http.StatusBadRequest)
		return
	}
	if _, err := uuid.Parse(req.ChatID); err != nil {
		http.Error(w, "chatid must be a valid UUID", http.StatusBadRequest)
		return
	}
	mode, ok := LookupMode(req.Mode)
	if !ok {
		http.Error(w, fmt.Sprintf("unknown agent mode %q (valid: ask, plan, do)", req.Mode), http.StatusBadRequest)
		return
	}

	if err := req.Msg.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("Message validation failed: %v", err), http.StatusBadRequest)
		return
	}

	aiOpts, err := resolveAgentAIOpts(req.TabId, req.AIMode)
	if err != nil {
		http.Error(w, fmt.Sprintf("WaveAI configuration error: %v", err), http.StatusInternalServerError)
		return
	}
	if req.ModelOverride != "" {
		aiOpts.Model = req.ModelOverride
	}

	sess := &Session{
		ChatID:      req.ChatID,
		TabID:       req.TabId,
		BlockID:     req.BlockId,
		Mode:        mode,
		AIOpts:      *aiOpts,
		Cwd:         req.Context.Cwd,
		Connection:  req.Context.Connection,
		LastCommand: req.Context.LastCommand,
		RecentCmds:  req.Context.RecentCmds,
	}

	var planContext string
	if req.PlanPath != "" {
		planBytes, readErr := os.ReadFile(req.PlanPath)
		if readErr != nil {
			log.Printf("agent: failed to read plan file %s: %v\n", req.PlanPath, readErr)
		} else {
			planContext = string(planBytes)
		}
	}

	sseHandler := sse.MakeSSEHandlerCh(w, r.Context())
	defer sseHandler.Close()

	err = RunAgent(r.Context(), sseHandler, wstore.GetClientId(), AgentOpts{
		Session:     sess,
		UserMsg:     &req.Msg,
		AIOpts:      *aiOpts,
		PlanContext: planContext,
	})
	if err != nil {
		log.Printf("agent: RunAgent error: %v\n", err)
		// SSE stream may already be closed by RunAgent via AiMsgError.
	}
}
