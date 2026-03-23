package http

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/bootstrap"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// AgentsHandler handles agent CRUD and sharing endpoints.
type AgentsHandler struct {
	agents           store.AgentStore
	token            string
	defaultWorkspace string            // default workspace path template (e.g. "~/.goclaw/workspace")
	msgBus           *bus.MessageBus   // for cache invalidation events (nil = no events)
	summoner         *AgentSummoner    // LLM-based agent setup (nil = disabled)
	isOwner          func(string) bool // checks if user ID is a system owner (nil = no owners configured)
}

// NewAgentsHandler creates a handler for agent management endpoints.
// isOwner is a function that checks if a user ID is in GOCLAW_OWNER_IDS (nil = disabled).
func NewAgentsHandler(agents store.AgentStore, token, defaultWorkspace string, msgBus *bus.MessageBus, summoner *AgentSummoner, isOwner func(string) bool) *AgentsHandler {
	return &AgentsHandler{agents: agents, token: token, defaultWorkspace: defaultWorkspace, msgBus: msgBus, summoner: summoner, isOwner: isOwner}
}

// isOwnerUser checks if the given user ID is a system owner.
func (h *AgentsHandler) isOwnerUser(userID string) bool {
	return userID != "" && h.isOwner != nil && h.isOwner(userID)
}

// emitCacheInvalidate broadcasts a cache invalidation event if msgBus is set.
func (h *AgentsHandler) emitCacheInvalidate(kind, key string) {
	if h.msgBus == nil {
		return
	}
	h.msgBus.Broadcast(bus.Event{
		Name:    protocol.EventCacheInvalidate,
		Payload: bus.CacheInvalidatePayload{Kind: kind, Key: key},
	})
}

// RegisterRoutes registers all agent management routes on the given mux.
func (h *AgentsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/agents", h.authMiddleware(h.handleList))
	mux.HandleFunc("POST /v1/agents", h.authMiddleware(h.handleCreate))
	mux.HandleFunc("GET /v1/agents/{id}", h.authMiddleware(h.handleGet))
	mux.HandleFunc("PUT /v1/agents/{id}", h.authMiddleware(h.handleUpdate))
	mux.HandleFunc("DELETE /v1/agents/{id}", h.authMiddleware(h.handleDelete))
	mux.HandleFunc("GET /v1/agents/{id}/shares", h.authMiddleware(h.handleListShares))
	mux.HandleFunc("POST /v1/agents/{id}/shares", h.authMiddleware(h.handleShare))
	mux.HandleFunc("DELETE /v1/agents/{id}/shares/{userID}", h.authMiddleware(h.handleRevokeShare))
	mux.HandleFunc("POST /v1/agents/{id}/regenerate", h.authMiddleware(h.handleRegenerate))
	mux.HandleFunc("POST /v1/agents/{id}/resummon", h.authMiddleware(h.handleResummon))
	mux.HandleFunc("GET /v1/agents/{id}/instances", h.authMiddleware(h.handleListInstances))
	mux.HandleFunc("GET /v1/agents/{id}/instances/{userID}/files", h.authMiddleware(h.handleGetInstanceFiles))
	mux.HandleFunc("PUT /v1/agents/{id}/instances/{userID}/files/{fileName}", h.authMiddleware(h.handleSetInstanceFile))
	mux.HandleFunc("PATCH /v1/agents/{id}/instances/{userID}/metadata", h.authMiddleware(h.handleUpdateInstanceMetadata))
}

func (h *AgentsHandler) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(h.token, "", next)
}

func (h *AgentsHandler) handleList(w http.ResponseWriter, r *http.Request) {
	userID := store.UserIDFromContext(r.Context())
	if userID == "" {
		locale := store.LocaleFromContext(r.Context())
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgUserIDHeader)})
		return
	}

	var agents []store.AgentData
	var err error
	if h.isOwnerUser(userID) {
		agents, err = h.agents.List(r.Context(), "") // owners see all agents
	} else {
		agents, err = h.agents.ListAccessible(r.Context(), userID)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

func (h *AgentsHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	userID := store.UserIDFromContext(r.Context())
	locale := store.LocaleFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgUserIDHeader)})
		return
	}

	var req store.AgentData
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgInvalidRequest, err.Error())})
		return
	}

	if !isValidSlug(req.AgentKey) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgInvalidSlug, "agent_key")})
		return
	}

	// Check for duplicate agent_key before creating
	if existing, _ := h.agents.GetByKey(r.Context(), req.AgentKey); existing != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": i18n.T(locale, i18n.MsgAlreadyExists, "agent", req.AgentKey)})
		return
	}

	req.OwnerID = userID

	// Resolve tenant_id: cross-tenant callers must provide it; others inherit their own tenant.
	if store.IsCrossTenant(r.Context()) {
		if req.TenantID == uuid.Nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgRequired, "tenant_id")})
			return
		}
	} else {
		req.TenantID = store.TenantIDFromContext(r.Context())
	}

	if req.AgentType == "" {
		req.AgentType = store.AgentTypeOpen
	}
	if req.ContextWindow <= 0 {
		req.ContextWindow = config.DefaultContextWindow
	}
	if req.MaxToolIterations <= 0 {
		req.MaxToolIterations = config.DefaultMaxIterations
	}
	if req.Workspace == "" {
		req.Workspace = fmt.Sprintf("%s/%s", h.defaultWorkspace, req.AgentKey)
	}
	req.RestrictToWorkspace = true

	// Default: enable compaction and memory for new agents
	if len(req.CompactionConfig) == 0 {
		req.CompactionConfig = json.RawMessage(`{}`)
	}
	if len(req.MemoryConfig) == 0 {
		req.MemoryConfig = json.RawMessage(`{"enabled":true}`)
	}

	// Check if predefined agent has a description for LLM summoning
	description := extractDescription(req.OtherConfig)
	if req.AgentType == store.AgentTypePredefined && description != "" && h.summoner != nil {
		req.Status = store.AgentStatusSummoning
	} else if req.Status == "" {
		req.Status = store.AgentStatusActive
	}

	if err := h.agents.Create(r.Context(), &req); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": i18n.T(locale, i18n.MsgAlreadyExists, "agent", req.AgentKey)})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}

	// Seed context files into agent_context_files (skipped for open agents).
	// For summoning agents, templates serve as fallback if LLM fails.
	if _, err := bootstrap.SeedToStore(r.Context(), h.agents, req.ID, req.AgentType); err != nil {
		slog.Warn("failed to seed context files for new agent", "agent", req.AgentKey, "error", err)
	}

	// Start LLM summoning in background if applicable
	if req.Status == store.AgentStatusSummoning {
		go h.summoner.SummonAgent(req.ID, req.TenantID, req.Provider, req.Model, description)
	}

	emitAudit(h.msgBus, r, "agent.created", "agent", req.ID.String())
	writeJSON(w, http.StatusCreated, req)
}

func (h *AgentsHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	userID := store.UserIDFromContext(r.Context())
	locale := store.LocaleFromContext(r.Context())
	isOwner := h.isOwnerUser(userID)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		// Try by agent_key
		ag, err2 := h.agents.GetByKey(r.Context(), r.PathValue("id"))
		if err2 != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(locale, i18n.MsgNotFound, "agent", r.PathValue("id"))})
			return
		}
		if userID != "" && !isOwner {
			if ok, _, _ := h.agents.CanAccess(r.Context(), ag.ID, userID); !ok {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": i18n.T(locale, i18n.MsgNoAccess, "agent")})
				return
			}
		}
		writeJSON(w, http.StatusOK, ag)
		return
	}

	ag, err := h.agents.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(locale, i18n.MsgNotFound, "agent", id.String())})
		return
	}

	if userID != "" && !isOwner {
		if ok, _, _ := h.agents.CanAccess(r.Context(), id, userID); !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": i18n.T(locale, i18n.MsgNoAccess, "agent")})
			return
		}
	}

	writeJSON(w, http.StatusOK, ag)
}

func (h *AgentsHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	userID := store.UserIDFromContext(r.Context())
	locale := store.LocaleFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgInvalidID, "agent")})
		return
	}

	// Only owner can update
	ag, err := h.agents.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(locale, i18n.MsgNotFound, "agent", id.String())})
		return
	}
	if userID != "" && ag.OwnerID != userID && !h.isOwnerUser(userID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": i18n.T(locale, i18n.MsgOwnerOnly, "update agent")})
		return
	}

	var updates map[string]any
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgInvalidRequest, err.Error())})
		return
	}

	// Allowlist: only permit known agent columns to be updated.
	// Defense-in-depth against column injection via arbitrary JSON keys.
	allowed := filterAllowedKeys(updates, agentAllowedFields)
	allowed["restrict_to_workspace"] = true

	// Snapshot current state before applying changes (non-fatal).
	// Only create version if there are actual value changes (not just re-sending same data).
	if changedBy := userID; changedBy != "" {
		summary := buildHTTPChangeSummary(ag, allowed)
		if summary != "" {
			if err := h.agents.CreateVersion(r.Context(), id, changedBy, summary); err != nil {
				slog.Warn("http: failed to create version snapshot", "agent", ag.AgentKey, "error", err)
			}
		}
	}

	if err := h.agents.Update(r.Context(), id, allowed); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Invalidate caches: agent Loop + bootstrap files
	h.emitCacheInvalidate(bus.CacheKindAgent, ag.AgentKey)
	h.emitCacheInvalidate(bus.CacheKindBootstrap, id.String())

	// Cascade: if status changed, broadcast so channel instances and cron jobs react.
	if newStatus, ok := allowed["status"].(string); ok && newStatus != ag.Status {
		if h.msgBus != nil {
			bus.BroadcastForTenant(h.msgBus, bus.EventAgentStatusChanged,
				store.TenantIDFromContext(r.Context()),
				bus.AgentStatusChangedPayload{
					AgentID:   id.String(),
					OldStatus: ag.Status,
					NewStatus: newStatus,
				})
		}
	}

	emitAudit(h.msgBus, r, "agent.updated", "agent", id.String())
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (h *AgentsHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	userID := store.UserIDFromContext(r.Context())
	locale := store.LocaleFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgInvalidID, "agent")})
		return
	}

	// Only owner can delete
	ag, err := h.agents.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(locale, i18n.MsgNotFound, "agent", id.String())})
		return
	}
	if userID != "" && ag.OwnerID != userID && !h.isOwnerUser(userID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": i18n.T(locale, i18n.MsgOwnerOnly, "delete agent")})
		return
	}

	if err := h.agents.Delete(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Invalidate caches: agent Loop + bootstrap files
	h.emitCacheInvalidate(bus.CacheKindAgent, ag.AgentKey)
	h.emitCacheInvalidate(bus.CacheKindBootstrap, id.String())

	emitAudit(h.msgBus, r, "agent.deleted", "agent", id.String())
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

// buildHTTPChangeSummary compares updates against the current agent state
// and only includes fields that actually changed.
func buildHTTPChangeSummary(ag *store.AgentData, updates map[string]any) string {
	// Build a map of current values for comparison
	current := map[string]any{
		"display_name":        ag.DisplayName,
		"frontmatter":         ag.Frontmatter,
		"provider":            ag.Provider,
		"model":               ag.Model,
		"context_window":      float64(ag.ContextWindow),      // JSON numbers decode as float64
		"max_tool_iterations": float64(ag.MaxToolIterations),
		"workspace":           ag.Workspace,
		"restrict_to_workspace": ag.RestrictToWorkspace,
		"status":              ag.Status,
		"is_default":          ag.IsDefault,
	}

	var parts []string
	for key, newVal := range updates {
		if key == "restrict_to_workspace" {
			continue
		}
		// Compare: skip if value unchanged
		if oldVal, ok := current[key]; ok {
			if fmt.Sprintf("%v", oldVal) == fmt.Sprintf("%v", newVal) {
				continue
			}
		}
		// JSONB fields: compare JSON content
		if isJSONBField(key) {
			oldJSON := agentJSONBField(ag, key)
			if newBytes, ok := newVal.([]byte); ok && jsonEqual(oldJSON, newBytes) {
				continue
			}
		}

		switch key {
		case "model":
			if s, ok := newVal.(string); ok {
				parts = append(parts, fmt.Sprintf("model: %s → %s", ag.Model, s))
			}
		case "provider":
			if s, ok := newVal.(string); ok {
				parts = append(parts, fmt.Sprintf("provider: %s → %s", ag.Provider, s))
			}
		case "display_name":
			if s, ok := newVal.(string); ok {
				parts = append(parts, fmt.Sprintf("name: %s → %s", ag.DisplayName, s))
			}
		default:
			parts = append(parts, strings.ReplaceAll(key, "_", " ")+" updated")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func isJSONBField(key string) bool {
	switch key {
	case "tools_config", "sandbox_config", "subagents_config",
		"memory_config", "compaction_config", "context_pruning", "other_config":
		return true
	}
	return false
}

func agentJSONBField(ag *store.AgentData, key string) []byte {
	switch key {
	case "tools_config":
		return ag.ToolsConfig
	case "sandbox_config":
		return ag.SandboxConfig
	case "subagents_config":
		return ag.SubagentsConfig
	case "memory_config":
		return ag.MemoryConfig
	case "compaction_config":
		return ag.CompactionConfig
	case "context_pruning":
		return ag.ContextPruning
	case "other_config":
		return ag.OtherConfig
	}
	return nil
}

func jsonEqual(a, b []byte) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	ra, _ := json.Marshal(va)
	rb, _ := json.Marshal(vb)
	return string(ra) == string(rb)
}
