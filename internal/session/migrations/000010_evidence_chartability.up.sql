ALTER TABLE agent_evidence_facts
    ADD COLUMN IF NOT EXISTS chartable BOOLEAN;

UPDATE agent_evidence_facts
SET chartable = TRUE
WHERE chartable IS NULL;
