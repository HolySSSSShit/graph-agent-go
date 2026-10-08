CREATE TABLE IF NOT EXISTS agent_sessions
(
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
    state
    TEXT
    NOT
    NULL
    DEFAULT
    'new',
    created_at
    TIMESTAMPTZ
    NOT
    NULL
    DEFAULT
    NOW
(
),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW
(
),
    PRIMARY KEY
(
    tenant_id,
    user_id,
    session_id
)
    );

CREATE TABLE IF NOT EXISTS agent_messages
(
    message_id
    BIGSERIAL
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
    role
    TEXT
    NOT
    NULL,
    content
    TEXT
    NOT
    NULL,
    tool_name
    TEXT,
    created_at
    TIMESTAMPTZ
    NOT
    NULL
    DEFAULT
    NOW
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
) ON DELETE CASCADE
    );

CREATE INDEX IF NOT EXISTS agent_messages_session_order_idx
    ON agent_messages (tenant_id, user_id, session_id, message_id);

CREATE TABLE IF NOT EXISTS session_compacts
(
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
    version
    BIGINT
    NOT
    NULL,
    covered_messages
    INTEGER
    NOT
    NULL,
    content
    TEXT
    NOT
    NULL,
    updated_at
    TIMESTAMPTZ
    NOT
    NULL
    DEFAULT
    NOW
(
),
    PRIMARY KEY
(
    tenant_id,
    user_id,
    session_id
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
) ON DELETE CASCADE
    );
