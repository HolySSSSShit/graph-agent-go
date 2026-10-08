package trace

import (
	"strings"
	"testing"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestProjectRetainsToolAuditFields(t *testing.T) {
	thinking := true
	event := core.TraceEvent{
		TraceID: "trace-1", RunID: "run-1", SessionID: "session-1", TenantID: "tenant-1", UserID: "user-1",
		Sequence: 7, Type: "tool.failed", Stage: core.NodeTypeToolExecute, Status: core.ToolStatusFailed,
		Message: "MCP https://private.example/api failed with timeout", Prompt: []core.Message{{Role: "user", Content: "secret prompt"}},
		Output: "secret model output", ToolName: "get_sales_trend", ToolArguments: map[string]any{"range": "2026-08", "api_key": "debug-only"}, StepID: "sales", Attempt: 1,
		ResultBytes: 4096, ResultHandleID: "hdl_abc", ThinkingEnabled: &thinking, At: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}
	projected, ok := project(event)
	if !ok {
		t.Fatal("tool failure should be projected")
	}
	if projected.errorCode != "timeout" || !projected.isTool || !projected.toolComplete {
		t.Fatalf("unexpected projection: %+v", projected)
	}
	if projected.runID != "run-1" || projected.toolName != "get_sales_trend" || projected.resultBytes != 4096 || projected.resultHandleID != "hdl_abc" {
		t.Fatalf("allowlisted fields missing: %+v", projected)
	}
	if projected.argumentsJSON != `{"api_key":"debug-only","range":"2026-08"}` {
		t.Fatalf("tool arguments must be retained for replay: %s", projected.argumentsJSON)
	}
}

func TestProjectAcceptsModelPromptAndDiagnosticOutput(t *testing.T) {
	for _, kind := range []string{"run.input", "tool.arguments", "tool.result"} {
		if _, ok := project(core.TraceEvent{Type: kind}); ok {
			t.Fatalf("%s must not enter PostgreSQL projection", kind)
		}
	}
	prompt, ok := project(core.TraceEvent{Type: "model.prompt", Prompt: []core.Message{{Role: "user", Content: "hello"}}})
	if !ok || !strings.Contains(prompt.promptJSON, "hello") {
		t.Fatalf("model prompt should be projected: %#v, %v", prompt, ok)
	}
	projected, ok := project(core.TraceEvent{Type: "model.output", Message: "type=final output={\"action\":\"select\"}"})
	if !ok || projected.eventType != "model.output" || !strings.Contains(projected.message, "action") {
		t.Fatalf("model diagnostic output should be projected: %#v, %v", projected, ok)
	}
}

func TestProjectAcceptsEvidenceLifecycleWithoutProvenance(t *testing.T) {
	event := core.TraceEvent{Type: "evidence.compiled", RunID: "run", TraceID: "trace", SessionID: "session", TenantID: "tenant", UserID: "user", EvidenceCount: 3, Message: "source_ref=hdl_secret"}
	projected, ok := project(event)
	if !ok {
		t.Fatal("evidence lifecycle event should be projected")
	}
	if projected.evidenceCount != 3 || projected.errorCode != "" {
		t.Fatalf("unexpected evidence projection: %#v", projected)
	}
}

func TestProjectAcceptsSessionEntityLifecycleWithoutEntities(t *testing.T) {
	event := core.TraceEvent{Type: "session.entities.inherited", RunID: "run", TraceID: "trace", SessionID: "session", TenantID: "tenant", UserID: "user", Message: `{"entities":{"private":"value"}}`}
	projected, ok := project(event)
	if !ok {
		t.Fatal("session entity lifecycle event should be projected")
	}
	if projected.eventType != "session.entities.inherited" || projected.errorCode != "" {
		t.Fatalf("unexpected session entity projection: %#v", projected)
	}
}

func TestProjectPreservesVisualizationsAsTraceMetadata(t *testing.T) {
	event := core.TraceEvent{
		Type: "message.completed", RunID: "run", TraceID: "trace", SessionID: "session", TenantID: "tenant", UserID: "user",
		Metadata: map[string]any{"visualizations": []core.Visualization{{ID: "trend", Type: "line", Title: "趋势", Option: map[string]any{"series": []any{}}}}},
	}
	projected, ok := project(event)
	if !ok {
		t.Fatal("message.completed should be projected")
	}
	if projected.metadataJSON == "" || !strings.Contains(projected.metadataJSON, "visualizations") || !strings.Contains(projected.metadataJSON, "trend") {
		t.Fatalf("visualizations missing from trace metadata: %s", projected.metadataJSON)
	}
}

func TestFixedErrorCodeDoesNotRetainRawMessage(t *testing.T) {
	if code := fixedErrorCode("authorization bearer secret rejected"); code != "permission_denied" {
		t.Fatalf("error code = %q", code)
	}
}
