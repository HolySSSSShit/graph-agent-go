ALTER TABLE agent_evidence_facts DROP CONSTRAINT IF EXISTS agent_evidence_facts_pkey;
ALTER TABLE agent_evidence_facts DROP COLUMN IF EXISTS source_tool_call_id;
ALTER TABLE agent_evidence_facts DROP COLUMN IF EXISTS source_result_path;
ALTER TABLE agent_evidence_facts
    ADD COLUMN IF NOT EXISTS step_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_evidence_facts
    ADD COLUMN IF NOT EXISTS source_tool TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_evidence_facts
    ADD COLUMN IF NOT EXISTS source_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_evidence_facts
    ADD COLUMN IF NOT EXISTS source_path TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_evidence_facts
    ADD CONSTRAINT agent_evidence_facts_pkey PRIMARY KEY (run_id, evidence_id);

CREATE INDEX IF NOT EXISTS agent_evidence_facts_source_idx
    ON agent_evidence_facts (run_id, source_tool, source_ref);
