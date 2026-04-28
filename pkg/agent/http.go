// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/secretstore"
	"github.com/s-zx/crest/pkg/wconfig"
	"github.com/s-zx/crest/pkg/web/sse"
	"github.com/s-zx/crest/pkg/wstore"
)

const (
	planPathDirName  = ".crest-plans"
	planPathMaxBytes = 512 * 1024
)

// readPlanContext reads a plan markdown file but only when it lives inside
// the request's <cwd>/.crest-plans directory. Without this constraint the
// frontend (or anyone POSTing to /api/post-agent-message) could ask the
// server to read /etc/passwd, ~/.ssh/id_rsa, etc. and the contents would be
// echoed straight into the system prompt.
func readPlanContext(planPath, cwd string) (string, error) {
	if planPath == "" {
		return "", nil
	}
	if cwd == "" {
		return "", fmt.Errorf("cwd required to validate plan path")
	}
	absPlan, err := filepath.Abs(planPath)
	if err != nil {
		return "", fmt.Errorf("resolve plan path: %w", err)
	}
	absPlan = filepath.Clean(absPlan)
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	allowed := filepath.Join(absCwd, planPathDirName) + string(filepath.Separator)
	if !strings.HasPrefix(absPlan, allowed) {
		return "", fmt.Errorf("plan path %q is outside %s", planPath, allowed)
	}
	if filepath.Ext(absPlan) != ".md" {
		return "", fmt.Errorf("plan path must be a .md file")
	}
	f, err := os.Open(absPlan)
	if err != nil {
		return "", fmt.Errorf("open plan: %w", err)
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, planPathMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read plan: %w", err)
	}
	if len(buf) > planPathMaxBytes {
		return "", fmt.Errorf("plan file exceeds %d bytes", planPathMaxBytes)
	}
	return string(buf), nil
}

// PostAgentMessageRequest is the body shape for POST /api/post-agent-message.
// The frontend sends the user's message plus the terminal context it already
// has (cwd, connection, last command, recent commands). Mode prefix parsing
// happens client-side — this handler just reads the final mode.
//
// Permission posture lives on a separate axis from Mode (per
// docs/permissions-v2-design.md). The FE will eventually send
// `permission_posture` directly via Shift+Tab; until then, the only
// API-side posture override is the `mode: "bench"` alias used by eval
// harnesses (Harbor/TB2) — that gets translated to PostureBench
// inside the handler.
type PostAgentMessageRequest struct {
	ChatID            string            `json:"chatid"`
	TabId             string            `json:"tabid"`
	BlockId           string            `json:"blockid"`
	Mode              string            `json:"mode"`
	PermissionPosture string            `json:"permission_posture,omitempty"`
	ModelOverride     string            `json:"modeloverride,omitempty"`
	PlanPath          string            `json:"planpath,omitempty"`
	Msg               uctypes.AIMessage `json:"msg"`
	Context           AgentContext      `json:"context,omitempty"`
}

type AgentContext struct {
	Cwd         string   `json:"cwd,omitempty"`
	Connection  string   `json:"connection,omitempty"`
	LastCommand string   `json:"last_command,omitempty"`
	RecentCmds  []string `json:"recent_cmds,omitempty"`
}

// isValidModelName guards modeloverride against control chars, whitespace,
// and absurd lengths before passing the value to the upstream API. Provider
// model IDs use [A-Za-z0-9._:/@-] in practice; we accept that set up to 128
// chars. Stricter than necessary on purpose — a bad override should fail fast
// at the edge instead of producing a confusing 400 from the LLM provider.
func isValidModelName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == ':' || r == '/' || r == '@':
		default:
			return false
		}
	}
	return true
}

func buildAIOptsFromSettings() (*uctypes.AIOptsType, error) {
	fullConfig := wconfig.GetWatcher().GetFullConfig()
	settings := fullConfig.Settings
	apiType := settings.AiApiType
	baseUrl := settings.AiBaseURL
	model := settings.AiModel
	if apiType == "" {
		apiType = detectAPIType(baseUrl)
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
	if baseUrl == "" && apiType == uctypes.APIType_GoogleGemini && model != "" {
		baseUrl = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent", model)
	}
	if baseUrl == "" {
		return nil, fmt.Errorf("no ai:baseurl configured — open Settings → AI Provider to set one up")
	}
	maxTokens := int(settings.AiMaxTokens)
	if maxTokens <= 0 {
		maxTokens = 16384
	}
	capabilities := []string{uctypes.AICapabilityTools}
	if apiType == uctypes.APIType_GoogleGemini {
		capabilities = append(capabilities, uctypes.AICapabilityImages, uctypes.AICapabilityPdfs)
	}
	return &uctypes.AIOptsType{
		APIType:       apiType,
		Model:         model,
		Endpoint:      baseUrl,
		APIToken:      apiToken,
		MaxTokens:     maxTokens,
		ThinkingLevel: uctypes.ThinkingLevelMedium,
		Verbosity:     uctypes.VerbosityLevelMedium,
		Capabilities:  capabilities,
	}, nil
}

func detectAPIType(endpoint string) string {
	e := strings.ToLower(endpoint)
	switch {
	case strings.Contains(e, "anthropic.com"):
		return uctypes.APIType_AnthropicMessages
	case strings.Contains(e, "generativelanguage.googleapis.com"):
		return uctypes.APIType_GoogleGemini
	case strings.Contains(e, "responses"):
		return uctypes.APIType_OpenAIResponses
	default:
		return uctypes.APIType_OpenAIChat
	}
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
		Force  bool   `json:"force,omitempty"`
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
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		if err := wt.validateRemovePath(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Derive branch name from path so `git branch -D` actually runs
		// (the original handler left BranchName empty, leaking branches).
		base := filepath.Base(req.Path)
		if validateWorktreeName(base) == nil {
			wt.BranchName = "worktree-" + base
		}
		if !req.Force && wt.HasChanges() {
			http.Error(w, "worktree has uncommitted changes; pass force=true to discard", http.StatusConflict)
			return
		}
		if !req.Force && wt.HasUnpushedCommits() {
			http.Error(w, "worktree branch has commits not on any other branch; pass force=true to discard", http.StatusConflict)
			return
		}
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
		wt := &Worktree{Path: req.Path, RepoRoot: req.Cwd}
		if err := wt.validateRemovePath(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
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
	if !ValidMode(req.Mode) {
		http.Error(w, fmt.Sprintf("unknown agent mode %q (valid: ask, plan, do, bench)", req.Mode), http.StatusBadRequest)
		return
	}
	modeName := NormalizeMode(req.Mode)
	if err := req.Msg.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("Message validation failed: %v", err), http.StatusBadRequest)
		return
	}

	aiOpts, err := buildAIOptsFromSettings()
	if err != nil {
		http.Error(w, fmt.Sprintf("WaveAI configuration error: %v", err), http.StatusInternalServerError)
		return
	}
	if req.ModelOverride != "" {
		trimmed := strings.TrimSpace(req.ModelOverride)
		if !isValidModelName(trimmed) {
			http.Error(w, fmt.Sprintf("invalid modeloverride %q", req.ModelOverride), http.StatusBadRequest)
			return
		}
		aiOpts.Model = trimmed
	}

	// Resolve posture. `mode: "bench"` forces PostureBench (eval-harness
	// alias) regardless of any explicit permission_posture. Otherwise
	// take the explicit field if set, else "" so RunAgent's
	// resolvePosture picks the user default.
	posture := req.PermissionPosture
	if req.Mode == ModeBench {
		posture = "bench"
	}

	sess := &Session{
		ChatID:      req.ChatID,
		TabID:       req.TabId,
		BlockID:     req.BlockId,
		Mode:        modeName,
		AIOpts:      *aiOpts,
		Cwd:         req.Context.Cwd,
		Connection:  req.Context.Connection,
		LastCommand: req.Context.LastCommand,
		RecentCmds:  req.Context.RecentCmds,
		Posture:     posture,
	}

	var planContext string
	if req.PlanPath != "" {
		ctxStr, readErr := readPlanContext(req.PlanPath, req.Context.Cwd)
		if readErr != nil {
			log.Printf("agent: rejected plan path %s: %v\n", req.PlanPath, readErr)
		} else {
			planContext = ctxStr
		}
	}

	sseHandler := sse.MakeSSEHandlerCh(w, r.Context())
	defer sseHandler.Close()

	// Set up SSE before RunAgent so any error path — including backends
	// that bail before reaching their happy-path SetupSSE on a 4xx from
	// the upstream provider — can surface the failure as an error chunk
	// the FE actually parses. Without this, status!=200 from the provider
	// produced an empty 200 response and the agent appeared to hang.
	if err := sseHandler.SetupSSE(); err != nil {
		log.Printf("agent: SetupSSE failed: %v\n", err)
		http.Error(w, "failed to setup SSE", http.StatusInternalServerError)
		return
	}

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
