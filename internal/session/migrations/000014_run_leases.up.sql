ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS worker_id TEXT,
    ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS active_slot BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE TABLE IF NOT EXISTS agent_tenant_runtime_limits
(
    tenant_id
    TEXT
    PRIMARY
    KEY,
    max_concurrent_runs
    INTEGER
    NOT
    NULL
    DEFAULT
    5
    CHECK
(
    max_concurrent_runs >
    0
),
    active_runs INTEGER NOT NULL DEFAULT 0 CHECK
(
    active_runs
    >=
    0
),
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW
(
)
    );
