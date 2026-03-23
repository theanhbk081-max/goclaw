-- Agent version history: snapshot-on-write for rollback support.
CREATE TABLE agent_versions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id              UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    version               INT  NOT NULL,

    -- Snapshot of agent config at this version
    display_name          VARCHAR(255),
    frontmatter           TEXT,
    provider              VARCHAR(50),
    model                 VARCHAR(200),
    context_window        INT,
    max_tool_iterations   INT,
    workspace             TEXT,
    restrict_to_workspace BOOLEAN NOT NULL DEFAULT TRUE,
    tools_config          JSONB,
    sandbox_config        JSONB,
    subagents_config      JSONB,
    memory_config         JSONB,
    compaction_config     JSONB,
    context_pruning       JSONB,
    other_config          JSONB,

    -- Context file snapshots: [{"file_name":"SOUL.md","content":"..."},...]
    context_files         JSONB,

    -- Audit
    changed_by            VARCHAR(255) NOT NULL DEFAULT 'system',
    change_summary        TEXT,
    tenant_id             UUID NOT NULL REFERENCES tenants(id),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(agent_id, version)
);

CREATE INDEX idx_agent_versions_agent ON agent_versions(agent_id, version DESC);
CREATE INDEX idx_agent_versions_tenant ON agent_versions(tenant_id);
