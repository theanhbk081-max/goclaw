package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// --- Agent Versioning ---

// CreateVersion snapshots the current agent config (including context files) into agent_versions.
func (s *PGAgentStore) CreateVersion(ctx context.Context, agentID uuid.UUID, changedBy, changeSummary string) error {
	// 1. Get current agent state
	ag, err := s.GetByID(ctx, agentID)
	if err != nil {
		return err
	}

	// 2. Get current context files and serialize
	files, err := s.GetAgentContextFiles(ctx, agentID)
	if err != nil {
		return err
	}
	var filesJSON []byte
	if len(files) > 0 {
		filesJSON, _ = json.Marshal(files)
	}

	// 3. Atomic INSERT with version number derived in the same statement (no race condition)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO agent_versions (
			id, agent_id, version,
			display_name, frontmatter, provider, model,
			context_window, max_tool_iterations, workspace, restrict_to_workspace,
			tools_config, sandbox_config, subagents_config,
			memory_config, compaction_config, context_pruning, other_config,
			context_files, changed_by, change_summary, tenant_id, created_at
		) VALUES (
			$1, $2, (SELECT COALESCE(MAX(version), 0) + 1 FROM agent_versions WHERE agent_id = $2),
			$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22
		)`,
		store.GenNewID(), agentID,
		ag.DisplayName, nilStr(ag.Frontmatter), ag.Provider, ag.Model,
		nilInt(ag.ContextWindow), nilInt(ag.MaxToolIterations), ag.Workspace, ag.RestrictToWorkspace,
		jsonOrNull(ag.ToolsConfig), jsonOrNull(ag.SandboxConfig), jsonOrNull(ag.SubagentsConfig),
		jsonOrNull(ag.MemoryConfig), jsonOrNull(ag.CompactionConfig), jsonOrNull(ag.ContextPruning),
		jsonOrNull(ag.OtherConfig),
		jsonOrNull(filesJSON), changedBy, nilStr(changeSummary), tenantIDForInsert(ctx), time.Now(),
	)
	return err
}

// ListVersions returns a paginated list of version summaries (without context_files content).
func (s *PGAgentStore) ListVersions(ctx context.Context, agentID uuid.UUID, limit, offset int) ([]store.AgentVersionData, int, error) {
	tClause, tArgs, err := tenantClauseN(ctx, 2)
	if err != nil {
		return nil, 0, err
	}
	baseArgs := append([]any{agentID}, tArgs...)

	// Total count
	var total int
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_versions WHERE agent_id = $1`+tClause,
		baseArgs...,
	).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	// Paginated query — lightweight (no context_files)
	if limit <= 0 {
		limit = 20
	}
	nextParam := len(baseArgs) + 1
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, version,
		        COALESCE(display_name, ''), COALESCE(provider, ''), COALESCE(model, ''),
		        changed_by, COALESCE(change_summary, ''),
		        TO_CHAR(created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		 FROM agent_versions
		 WHERE agent_id = $1`+tClause+`
		 ORDER BY version DESC
		 LIMIT $`+itoa(nextParam)+` OFFSET $`+itoa(nextParam+1),
		append(baseArgs, limit, offset)...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var result []store.AgentVersionData
	for rows.Next() {
		var v store.AgentVersionData
		if err := rows.Scan(
			&v.ID, &v.AgentID, &v.Version, &v.DisplayName,
			&v.Provider, &v.Model, &v.ChangedBy, &v.ChangeSummary, &v.CreatedAt,
		); err != nil {
			continue
		}
		result = append(result, v)
	}
	return result, total, nil
}

// GetVersion returns the full version data including context files.
func (s *PGAgentStore) GetVersion(ctx context.Context, agentID uuid.UUID, version int) (*store.AgentVersionData, error) {
	tClause, tArgs, err := tenantClauseN(ctx, 3)
	if err != nil {
		return nil, err
	}

	var v store.AgentVersionData
	var displayName, frontmatter, provider, model, workspace, changeSummary sql.NullString
	var toolsConfig, sandboxConfig, subagentsConfig *string
	var memoryConfig, compactionConfig, contextPruning, otherConfig *string
	var contextFiles *string
	err = s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, version,
		        COALESCE(display_name, ''), COALESCE(frontmatter, ''),
		        COALESCE(provider, ''), COALESCE(model, ''),
		        COALESCE(context_window, 0), COALESCE(max_tool_iterations, 0),
		        COALESCE(workspace, ''), restrict_to_workspace,
		        tools_config::text, sandbox_config::text, subagents_config::text,
		        memory_config::text, compaction_config::text, context_pruning::text, other_config::text,
		        context_files::text, changed_by, change_summary,
		        TO_CHAR(created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		 FROM agent_versions
		 WHERE agent_id = $1 AND version = $2`+tClause,
		append([]any{agentID, version}, tArgs...)...,
	).Scan(
		&v.ID, &v.AgentID, &v.Version,
		&displayName, &frontmatter, &provider, &model,
		&v.ContextWindow, &v.MaxToolIterations,
		&workspace, &v.RestrictToWorkspace,
		&toolsConfig, &sandboxConfig, &subagentsConfig,
		&memoryConfig, &compactionConfig, &contextPruning, &otherConfig,
		&contextFiles, &v.ChangedBy, &changeSummary, &v.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	v.DisplayName = displayName.String
	v.Frontmatter = frontmatter.String
	v.Provider = provider.String
	v.Model = model.String
	v.Workspace = workspace.String
	v.ChangeSummary = changeSummary.String
	if toolsConfig != nil {
		v.ToolsConfig = json.RawMessage(*toolsConfig)
	}
	if sandboxConfig != nil {
		v.SandboxConfig = json.RawMessage(*sandboxConfig)
	}
	if subagentsConfig != nil {
		v.SubagentsConfig = json.RawMessage(*subagentsConfig)
	}
	if memoryConfig != nil {
		v.MemoryConfig = json.RawMessage(*memoryConfig)
	}
	if compactionConfig != nil {
		v.CompactionConfig = json.RawMessage(*compactionConfig)
	}
	if contextPruning != nil {
		v.ContextPruning = json.RawMessage(*contextPruning)
	}
	if otherConfig != nil {
		v.OtherConfig = json.RawMessage(*otherConfig)
	}
	if contextFiles != nil {
		v.ContextFiles = json.RawMessage(*contextFiles)
	}
	return &v, nil
}

