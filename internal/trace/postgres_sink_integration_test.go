package trace

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
	"github.com/HolySSSSShit/go-agent/internal/session"
)

func TestPostgresSinkStoresOnlySanitizedProjection(t *testing.T) {
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AGENT_TEST_POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := session.NewPostgres(ctx, dsn, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identity := core.Identity{TenantID: "trace-test", UserID: "trace-user"}
	if _, err := store.Get(ctx, identity, "trace-session"); err != nil {
		t.Fatal(err)
	}
	sink, err := NewPostgresSink(ctx, dsn, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	runID := fmt.Sprintf("trace-sink-%d", time.Now().UnixNano())
	base := core.TraceEvent{RunID: runID, TraceID: runID, SessionID: "trace-session", TenantID: identity.TenantID, UserID: identity.UserID, At: time.Now().UTC()}
	if err := sink.Record(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Sequence = 1
	base.Type = "run.started"
	base.Status = "running"
	if err := sink.Record(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Sequence = 2
	base.Type = "tool.completed"
	base.Stage = core.NodeTypeToolExecute
	base.Status = core.ToolStatusSuccess
	base.ToolName = "get_sales_trend"
	base.ToolArguments = map[string]any{"start_date": "2026-08-01", "end_date": "2026-08-31"}
	base.StepID = "sales"
	base.Attempt = 1
	base.ResultBytes = 1234
	base.ResultHandleID = "hdl_test"
	base.Prompt = []core.Message{{Role: "user", Content: "must not persist"}}
	base.Output = "must not persist"
	if err := sink.Record(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Sequence = 3
	base.Type = "model.output"
	if err := sink.Record(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Sequence = 4
	base.Type = "evidence.compiled"
	base.Stage = core.NodeTypeResultReduce
	base.Status = "completed"
	base.EvidenceCount = 2
	base.Message = "source_ref must not persist"
	if err := sink.Record(ctx, base); err != nil {
		t.Fatal(err)
	}
	var toolName, handleID, arguments string
	var resultBytes int64
	if err := sink.db.QueryRowContext(ctx, `SELECT tool_name, result_bytes, result_handle_id, arguments_json::text FROM agent_tool_calls WHERE run_id = $1 AND step_id = 'sales' AND attempt = 1`, runID).Scan(&toolName, &resultBytes, &handleID, &arguments); err != nil {
		t.Fatal(err)
	}
	if toolName != "get_sales_trend" || resultBytes != 1234 || handleID != "hdl_test" {
		t.Fatalf("unexpected stored tool audit: %q %d %q", toolName, resultBytes, handleID)
	}
	if arguments != `{"end_date": "2026-08-31", "start_date": "2026-08-01"}` && arguments != `{"start_date":"2026-08-01","end_date":"2026-08-31"}` {
		t.Fatalf("unexpected tool arguments: %s", arguments)
	}
	var events int
	if err := sink.db.QueryRowContext(ctx, `SELECT count(*) FROM agent_trace_events WHERE run_id = $1`, runID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 3 {
		t.Fatalf("trace event count = %d, want 3; raw model output must be rejected", events)
	}
	var evidenceCount int
	if err := sink.db.QueryRowContext(ctx, `SELECT evidence_count FROM agent_trace_events WHERE run_id = $1 AND type = 'evidence.compiled'`, runID).Scan(&evidenceCount); err != nil {
		t.Fatal(err)
	}
	if evidenceCount != 2 {
		t.Fatalf("evidence count = %d", evidenceCount)
	}
	var message string
	if err := sink.db.QueryRowContext(ctx, `SELECT message FROM agent_trace_events WHERE run_id = $1 AND type = 'evidence.compiled'`, runID).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if message != "source_ref must not persist" {
		t.Fatalf("trace message = %q", message)
	}
}
