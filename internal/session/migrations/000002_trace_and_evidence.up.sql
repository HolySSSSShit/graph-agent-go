ALTER TABLE agent_messages DROP COLUMN IF EXISTS tool_name;

CREATE TABLE IF NOT EXISTS agent_runs
(
    run_id
    TEXT
    PRIMARY
    KEY,
    tenant_id
    TEXT
    NOT
    NULL,
    user_id
    TEXT
    NOT
    NULL,
    session_id
    TEXT
    NOT
    NULL,
    status
    TEXT
    NOT
    NULL,
    error_code
    TEXT,
    started_at
    TIMESTAMPTZ
    NOT
    NULL,
    completed_at
    TIMESTAMPTZ,
    duration_ms
    BIGINT,
    FOREIGN
    KEY
(
    tenant_id,
    user_id,
    session_id
)
    REFERENCES agent_sessions
(
    tenant_id,
    user_id,
    session_id
) ON DELETE CASCADE
    );

CREATE INDEX IF NOT EXISTS agent_runs_session_idx ON agent_runs (tenant_id, user_id, session_id, started_at DESC);
