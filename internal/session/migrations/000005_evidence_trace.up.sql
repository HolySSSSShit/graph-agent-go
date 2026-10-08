ALTER TABLE agent_trace_events
    ADD COLUMN IF NOT EXISTS evidence_count INTEGER;
