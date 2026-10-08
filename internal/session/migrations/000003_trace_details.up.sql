CREATE TABLE IF NOT EXISTS agent_tool_calls
(
    run_id
    TEXT
    NOT
    NULL
    REFERENCES
    agent_runs
(
    run_id
) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    attempt INTEGER NOT NULL,
    tool_name TEXT NOT NULL,
    status TEXT NOT NULL,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    duration_ms BIGINT,
    result_bytes BIGINT,
    result_handle_id TEXT,
    PRIMARY KEY
(
    run_id,
    step_id,
    attempt
)
    );

CREATE INDEX IF NOT EXISTS agent_tool_calls_run_idx
    ON agent_tool_calls (run_id, started_at, step_id, attempt);

CREATE TABLE IF NOT EXISTS agent_trace_events
(
    run_id
    TEXT
    NOT
    NULL
    REFERENCES
    agent_runs
(
    run_id
) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    type TEXT NOT NULL,
    stage TEXT,
    status TEXT,
    model_name TEXT,
    tool_name TEXT,
    input_tokens INTEGER,
    output_tokens INTEGER,
    reasoning_tokens INTEGER,
    finish_reason TEXT,
    duration_ms BIGINT,
    error_code TEXT,
    occurred_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY
(
    run_id,
    sequence
)
    );

CREATE INDEX IF NOT EXISTS agent_trace_events_run_idx
    ON agent_trace_events (run_id, occurred_at, sequence);

CREATE TABLE IF NOT EXISTS agent_evidence_facts
(
    evidence_id
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
    run_id
    TEXT
    NOT
    NULL
    REFERENCES
    agent_runs
(
    run_id
) ON DELETE CASCADE,
    metric_id TEXT NOT NULL,
    value_json JSONB NOT NULL,
    unit TEXT NOT NULL,
    time_range JSONB,
    dimensions JSONB,
    source_tool_call_id TEXT,
    source_result_path TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW
(
)
    );

CREATE INDEX IF NOT EXISTS agent_evidence_facts_session_idx
    ON agent_evidence_facts (tenant_id, user_id, session_id, created_at DESC);

CREATE INDEX IF NOT EXISTS agent_evidence_facts_run_idx
    ON agent_evidence_facts (run_id, metric_id);
