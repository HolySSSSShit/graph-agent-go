ALTER TABLE agent_messages
    ADD COLUMN IF NOT EXISTS visualizations_json JSONB;
