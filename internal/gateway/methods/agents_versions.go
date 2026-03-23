package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/gateway"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// --- agents.versions.list ---

func (m *AgentsMethods) handleVersionsList(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	if m.agentStore == nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, "versioning requires database mode"))
		return
	}

	var params struct {
		AgentID string `json:"agentId"`
		Limit   int    `json:"limit"`
		Offset  int    `json:"offset"`
	}
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}
	if params.AgentID == "" {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "agentId")))
		return
	}

	ag, err := m.agentStore.GetByKey(ctx, params.AgentID)
	if err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgAgentNotFound, params.AgentID)))
		return
	}

	versions, total, err := m.agentStore.ListVersions(ctx, ag.ID, params.Limit, params.Offset)
	if err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInternal, fmt.Sprintf("failed to list versions: %v", err)))
		return
	}

	items := make([]map[string]any, 0, len(versions))
	for _, v := range versions {
		items = append(items, map[string]any{
			"version":       v.Version,
			"displayName":   v.DisplayName,
			"provider":      v.Provider,
			"model":         v.Model,
			"changedBy":     v.ChangedBy,
			"changeSummary": v.ChangeSummary,
			"createdAt":     v.CreatedAt,
		})
	}

	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"versions": items,
		"total":    total,
	}))
}

// --- agents.versions.get ---

func (m *AgentsMethods) handleVersionsGet(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	if m.agentStore == nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, "versioning requires database mode"))
		return
	}

	var params struct {
		AgentID string `json:"agentId"`
		Version int    `json:"version"`
	}
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}
	if params.AgentID == "" {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "agentId")))
		return
	}
	if params.Version <= 0 {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "version")))
		return
	}

	ag, err := m.agentStore.GetByKey(ctx, params.AgentID)
	if err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgAgentNotFound, params.AgentID)))
		return
	}

	v, err := m.agentStore.GetVersion(ctx, ag.ID, params.Version)
	if err != nil {
		slog.Warn("agents.versions.get: query failed", "agent", params.AgentID, "version", params.Version, "error", err)
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInternal, fmt.Sprintf("failed to get version: %v", err)))
		return
	}
	if v == nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, fmt.Sprintf("version %d not found", params.Version)))
		return
	}

	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"version":            v.Version,
		"displayName":        v.DisplayName,
		"frontmatter":        v.Frontmatter,
		"provider":           v.Provider,
		"model":              v.Model,
		"contextWindow":      v.ContextWindow,
		"maxToolIterations":  v.MaxToolIterations,
		"workspace":          v.Workspace,
		"restrictToWorkspace": v.RestrictToWorkspace,
		"toolsConfig":        rawOrNil(v.ToolsConfig),
		"sandboxConfig":      rawOrNil(v.SandboxConfig),
		"subagentsConfig":    rawOrNil(v.SubagentsConfig),
		"memoryConfig":       rawOrNil(v.MemoryConfig),
		"compactionConfig":   rawOrNil(v.CompactionConfig),
		"contextPruning":     rawOrNil(v.ContextPruning),
		"otherConfig":        rawOrNil(v.OtherConfig),
		"contextFiles":       rawOrNil(v.ContextFiles),
		"changedBy":          v.ChangedBy,
		"changeSummary":      v.ChangeSummary,
		"createdAt":          v.CreatedAt,
	}))
}

// --- agents.versions.rollback ---

func (m *AgentsMethods) handleVersionsRollback(ctx context.Context, client *gateway.Client, req *protocol.RequestFrame) {
	locale := store.LocaleFromContext(ctx)
	if m.agentStore == nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, "versioning requires database mode"))
		return
	}

	var params struct {
		AgentID string `json:"agentId"`
		Version int    `json:"version"`
	}
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}
	if params.AgentID == "" {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "agentId")))
		return
	}
	if params.Version <= 0 {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInvalidRequest, i18n.T(locale, i18n.MsgRequired, "version")))
		return
	}

	ag, err := m.agentStore.GetByKey(ctx, params.AgentID)
	if err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, i18n.T(locale, i18n.MsgAgentNotFound, params.AgentID)))
		return
	}

	// 1. Fetch target version
	target, err := m.agentStore.GetVersion(ctx, ag.ID, params.Version)
	if err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInternal, fmt.Sprintf("failed to get version: %v", err)))
		return
	}
	if target == nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrNotFound, fmt.Sprintf("version %d not found", params.Version)))
		return
	}

	// 2. Snapshot current state before rollback (so rollback itself is reversible)
	userID := client.UserID()
	if err := m.agentStore.CreateVersion(ctx, ag.ID, userID, fmt.Sprintf("pre-rollback snapshot (rolling back to v%d)", params.Version)); err != nil {
		slog.Warn("agents.versions.rollback: failed to snapshot current state", "agent", params.AgentID, "error", err)
	}

	// 3. Apply version config via Update — include ALL fields so cleared configs
	// (NULL in target version = "use defaults") are properly restored.
	updates := map[string]any{
		"display_name":          target.DisplayName,
		"frontmatter":           nilIfEmpty(target.Frontmatter),
		"provider":              target.Provider,
		"model":                 target.Model,
		"context_window":        target.ContextWindow,
		"max_tool_iterations":   target.MaxToolIterations,
		"workspace":             target.Workspace,
		"restrict_to_workspace": target.RestrictToWorkspace,
		"tools_config":          rawOrDBNull(target.ToolsConfig),
		"sandbox_config":        rawOrDBNull(target.SandboxConfig),
		"subagents_config":      rawOrDBNull(target.SubagentsConfig),
		"memory_config":         rawOrDBNull(target.MemoryConfig),
		"compaction_config":     rawOrDBNull(target.CompactionConfig),
		"context_pruning":       rawOrDBNull(target.ContextPruning),
		"other_config":          rawOrDBNull(target.OtherConfig),
	}

	if err := m.agentStore.Update(ctx, ag.ID, updates); err != nil {
		client.SendResponse(protocol.NewErrorResponse(req.ID, protocol.ErrInternal, fmt.Sprintf("failed to apply rollback: %v", err)))
		return
	}

	// 4. Restore context files from snapshot
	if len(target.ContextFiles) > 0 {
		var files []store.AgentContextFileData
		if json.Unmarshal(target.ContextFiles, &files) == nil {
			for _, f := range files {
				if err := m.agentStore.SetAgentContextFile(ctx, ag.ID, f.FileName, f.Content); err != nil {
					slog.Warn("agents.versions.rollback: failed to restore context file",
						"agent", params.AgentID, "file", f.FileName, "error", err)
				}
			}
		}
	}

	// 5. Invalidate caches
	m.agents.InvalidateAgent(params.AgentID)
	if m.interceptor != nil {
		m.interceptor.InvalidateAgent(ag.ID)
	}

	client.SendResponse(protocol.NewOKResponse(req.ID, map[string]any{
		"ok":              true,
		"agentId":         params.AgentID,
		"rolledBackTo":    params.Version,
	}))
	emitAudit(m.eventBus, client, "agent.rolledback", "agent", params.AgentID)
}

// --- Helpers ---

// rawOrNil returns nil for empty JSON raw messages (avoids "null" in response).
func rawOrNil(data json.RawMessage) any {
	if len(data) == 0 {
		return nil
	}
	// Return as parsed JSON so it's not double-encoded
	var v any
	if json.Unmarshal(data, &v) == nil {
		return v
	}
	return nil
}

// nilIfEmpty returns nil for empty strings (maps to SQL NULL).
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// rawOrDBNull returns []byte for non-empty JSON, or nil (SQL NULL) for empty.
func rawOrDBNull(data json.RawMessage) any {
	if len(data) == 0 {
		return nil
	}
	return []byte(data)
}

// buildChangeSummary generates a human-readable summary of what changed.
func buildChangeSummary(ag *store.AgentData, updates map[string]any) string {
	var parts []string
	for key := range updates {
		switch key {
		case "restrict_to_workspace":
			continue // default field, not a real change
		case "model":
			if newModel, ok := updates[key].(string); ok && newModel != ag.Model {
				parts = append(parts, fmt.Sprintf("model: %s → %s", ag.Model, newModel))
			} else {
				parts = append(parts, "model updated")
			}
		case "provider":
			if newProv, ok := updates[key].(string); ok && newProv != ag.Provider {
				parts = append(parts, fmt.Sprintf("provider: %s → %s", ag.Provider, newProv))
			} else {
				parts = append(parts, "provider updated")
			}
		case "display_name":
			parts = append(parts, "display name updated")
		case "workspace":
			parts = append(parts, "workspace updated")
		default:
			parts = append(parts, strings.ReplaceAll(key, "_", " ")+" updated")
		}
	}
	if len(parts) == 0 {
		return "config updated"
	}
	return strings.Join(parts, ", ")
}
