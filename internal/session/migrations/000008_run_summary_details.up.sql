ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS last_event_type TEXT,
    ADD COLUMN IF NOT EXISTS last_tool_name TEXT,
    ADD COLUMN IF NOT EXISTS last_model_name TEXT;
