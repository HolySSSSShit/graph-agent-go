CREATE TABLE IF NOT EXISTS agent_checkpoints
(
    checkpoint_id
    TEXT
    PRIMARY
    KEY,
    run_id
    TEXT
    NOT
    NULL
    REFERENCES
    agent_runs
(
    run_id
) ON DELETE CASCADE,
    trace_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    credential TEXT NOT NULL DEFAULT '',
    node TEXT NOT NULL,
    status TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    state_json JSONB NOT NULL,
    route_json JSONB NOT NULL,
    trace_events_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW
(
),
    FOREIGN KEY
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
)
  ON DELETE CASCADE,
    UNIQUE
(
    run_id,
    sequence
)
    );

CREATE INDEX IF NOT EXISTS agent_checkpoints_run_idx
    ON agent_checkpoints (run_id, sequence);

CREATE INDEX IF NOT EXISTS agent_checkpoints_owner_idx
    ON agent_checkpoints (tenant_id, user_id, session_id, created_at DESC);
