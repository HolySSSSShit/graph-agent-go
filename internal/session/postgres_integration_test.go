package session

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

func TestPostgresMigrationCreatesTraceTables(t *testing.T) {
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AGENT_TEST_POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := NewPostgres(ctx, dsn, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, table := range []string{"agent_runs", "agent_tool_calls", "agent_trace_events", "agent_evidence_facts", "agent_checkpoints"} {
		var exists bool
		if err := store.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("migration did not create %s", table)
		}
	}
}

func TestPostgresStorePersistsReplayableCheckpoint(t *testing.T) {
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AGENT_TEST_POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := NewPostgres(ctx, dsn, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identity := core.Identity{TenantID: "checkpoint-tenant", UserID: "checkpoint-user"}
	if _, err := store.Get(ctx, identity, "checkpoint-session"); err != nil {
		t.Fatal(err)
	}
	checkpoint := core.Checkpoint{ID: "checkpoint-1", RunID: "checkpoint-run", TraceID: "checkpoint-trace", SessionID: "checkpoint-session", Identity: identity, Credential: "credential-value", Node: core.NodeTypeReason, Status: "continue", Sequence: 1, State: core.RunState{RuntimeState: core.RuntimeState{Input: core.Message{Role: "user", Content: "查询"}, Metrics: core.RunMetrics{Steps: 1, NodeVisits: map[core.NodeType]int{core.NodeTypeReason: 1}}}}, Route: core.RouteDecision{SourceNode: core.NodeTypeReason, SelectedNext: core.NodeTypeToolExecute, AdmissionPassed: true}, TraceEvents: []core.TraceEvent{{Type: "model.output", Output: `{"kind":"tool_plan"}`}}}
	if err := store.SaveCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	latest, err := store.LatestCheckpoint(ctx, identity, checkpoint.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Credential != checkpoint.Credential || latest.State.Input.Content != "查询" || latest.Route.SelectedNext != core.NodeTypeToolExecute || len(latest.TraceEvents) != 1 || latest.State.Metrics.Steps != 1 {
		t.Fatalf("checkpoint replay mismatch: %#v", latest)
	}
	if _, err := store.LatestCheckpoint(ctx, core.Identity{TenantID: "other", UserID: "user"}, checkpoint.RunID); err == nil {
		t.Fatal("cross-owner checkpoint read should fail")
	}
}

func TestPostgresStorePersistsRefBackedEvidenceFacts(t *testing.T) {
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AGENT_TEST_POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := NewPostgres(ctx, dsn, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identity := core.Identity{TenantID: "evidence-test-tenant", UserID: "evidence-test-user"}
	if _, err := store.Get(ctx, identity, "evidence-test-session"); err != nil {
		t.Fatal(err)
	}
	facts := []core.EvidenceFact{{
		ID: "fact", MetricID: "metric", Value: 8, Unit: "unit", Dimensions: map[string]any{"scope": "test"},
		StepID: "step", SourceTool: "source", SourceRef: "ref", SourcePath: "$.metric",
	}}
	if err := store.SaveEvidenceFacts(ctx, identity, "evidence-test-session", "evidence-test-run", facts); err != nil {
		t.Fatal(err)
	}
	stored, err := store.ListEvidenceFacts(ctx, identity, "evidence-test-session", "evidence-test-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].SourceRef != "ref" || stored[0].SourceTool != "source" || stored[0].SourcePath != "$.metric" {
		t.Fatalf("stored evidence = %#v", stored)
	}
}
