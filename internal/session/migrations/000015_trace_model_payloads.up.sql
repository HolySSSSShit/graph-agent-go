ALTER TABLE agent_trace_events
    ADD COLUMN IF NOT EXISTS prompt_json JSONB,
    ADD COLUMN IF NOT EXISTS output_text TEXT;
