CREATE TABLE IF NOT EXISTS agent_approval_requests
(
    approval_id
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
    session_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    action_json JSONB NOT NULL,
    args_hash TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK
(
    status
    IN
(
    'pending',
    'approved',
    'rejected'
)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW
(
),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW
(
),
    resolved_at TIMESTAMPTZ,
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
  ON DELETE CASCADE
    );

CREATE INDEX IF NOT EXISTS agent_approval_requests_owner_idx
    ON agent_approval_requests (tenant_id, user_id, session_id, created_at DESC);

CREATE INDEX IF NOT EXISTS agent_approval_requests_run_idx
    ON agent_approval_requests (run_id, status);
