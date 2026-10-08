package orchestrator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

type checkpointTestPayload struct {
	Observations  []core.ToolObservation `json:"observations"`
	EvidenceFacts []core.EvidenceFact    `json:"evidence_facts"`
	OpaqueHandles map[string]struct{}    `json:"opaque_handles"`
	FinalResponse *core.FinalResponse    `json:"final_response"`
}

func TestMemoryCheckpointStoreKeepsReplayableSnapshotsIndependent(t *testing.T) {
	store := NewMemoryCheckpointStore()
	identity := core.Identity{TenantID: "tenant-1", UserID: "user-1"}
	codec := JSONStateCodec[checkpointTestPayload]{}
	payload, err := codec.Encode(checkpointTestPayload{
		Observations:  []core.ToolObservation{{ToolName: "query", Message: core.Message{Content: `{"value":8}`}}},
		EvidenceFacts: []core.EvidenceFact{{ID: "fact", Value: 8}},
		OpaqueHandles: map[string]struct{}{"tool\x00ref": {}},
		FinalResponse: &core.FinalResponse{Answer: "原始结论"},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := core.RunState{
		WorkflowState: core.WorkflowState{Version: 1, WorkflowKind: "checkpoint-test", WorkflowPayload: payload},
		RuntimeState: core.RuntimeState{
			Input:   core.Message{Role: "user", Content: "分析销售"},
			Route:   core.RouteState{CurrentNode: core.NodeTypeReason},
			Metrics: core.RunMetrics{Steps: 2, NodeVisits: map[core.NodeType]int{core.NodeTypeReason: 2}, NodeDurationsMS: map[core.NodeType]int64{core.NodeTypeReason: 10}},
		},
	}
	checkpoint := core.Checkpoint{ID: "cp-1", RunID: "run-1", Identity: identity, Node: core.NodeTypeReason, State: state, Credential: "credential", Route: core.RouteDecision{SourceNode: core.NodeTypeReason, SelectedNext: core.NodeTypeToolExecute}}
	if err := store.SaveCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	state.WorkflowPayload[0] = '['
	state.Metrics.NodeVisits[core.NodeTypeReason] = 99
	latest, err := store.LatestCheckpoint(context.Background(), identity, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(json.RawMessage(latest.State.WorkflowPayload))
	if err != nil {
		t.Fatal(err)
	}
	if latest.Credential != "credential" || latest.State.Input.Content != "分析销售" || decoded.Observations[0].Message.Content != `{"value":8}` || decoded.EvidenceFacts[0].Value != float64(8) || decoded.FinalResponse.Answer != "原始结论" {
		t.Fatalf("checkpoint snapshot was mutated: %+v", latest)
	}
	if latest.Route.SelectedNext != core.NodeTypeToolExecute || len(latest.State.Route.History) != 0 {
		t.Fatalf("route snapshot = %+v", latest.Route)
	}
	if latest.State.Metrics.Steps != 2 || latest.State.Metrics.NodeVisits[core.NodeTypeReason] != 2 || latest.State.Metrics.NodeDurationsMS[core.NodeTypeReason] != 10 {
		t.Fatalf("checkpoint metrics were mutated: %+v", latest.State.Metrics)
	}
}

func TestMemoryCheckpointStoreRejectsOtherOwner(t *testing.T) {
	store := NewMemoryCheckpointStore()
	owner := core.Identity{TenantID: "tenant-1", UserID: "user-1"}
	if err := store.SaveCheckpoint(context.Background(), core.Checkpoint{ID: "cp-owner", RunID: "run-owner", Identity: owner, State: core.RunState{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LatestCheckpoint(context.Background(), core.Identity{TenantID: "tenant-2", UserID: "user-2"}, "run-owner"); err == nil {
		t.Fatal("cross-owner checkpoint read should not succeed")
	}
}

func TestMemoryCheckpointStoreFindsLatestSuspendedBySession(t *testing.T) {
	store := NewMemoryCheckpointStore()
	identity := core.Identity{TenantID: "tenant", UserID: "user"}
	for i, status := range []string{core.CheckpointStatusSuspended, core.CheckpointStatusCompleted, core.CheckpointStatusSuspended, core.CheckpointStatusCompleted} {
		if err := store.SaveCheckpoint(context.Background(), core.Checkpoint{ID: string(rune('a' + i)), RunID: "run-" + string(rune('a'+i)), SessionID: "session", Identity: identity, Status: status, CreatedAt: time.Unix(int64(i+1), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint, err := store.LatestSuspendedCheckpoint(context.Background(), identity, "session")
	if err != nil || checkpoint.RunID != "run-c" {
		t.Fatalf("latest suspended = %+v, %v", checkpoint, err)
	}
}

func TestMemoryCheckpointStoreUsesSequenceAndIDAsTieBreakers(t *testing.T) {
	store := NewMemoryCheckpointStore()
	identity := core.Identity{TenantID: "tenant", UserID: "user"}
	created := time.Unix(10, 0)
	for _, checkpoint := range []core.Checkpoint{
		{ID: "cp-a", RunID: "run-a", SessionID: "session", Identity: identity, Status: core.CheckpointStatusSuspended, Sequence: 2, CreatedAt: created},
		{ID: "cp-b", RunID: "run-b", SessionID: "session", Identity: identity, Status: core.CheckpointStatusSuspended, Sequence: 3, CreatedAt: created},
		{ID: "cp-c", RunID: "run-c", SessionID: "session", Identity: identity, Status: core.CheckpointStatusSuspended, Sequence: 3, CreatedAt: created},
	} {
		if err := store.SaveCheckpoint(context.Background(), checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint, err := store.LatestSuspendedCheckpoint(context.Background(), identity, "session")
	if err != nil || checkpoint.ID != "cp-c" {
		t.Fatalf("latest suspended tie-break = %+v, %v", checkpoint, err)
	}
}

func TestConfiguredRouteResolverValidatesTarget(t *testing.T) {
	builder := NewGraph[struct{}]()
	builder.Start(core.NodeTypeReason).From(core.NodeTypeReason).To(core.NodeTypeToolExecute).End(core.NodeTypeToolExecute)
	resolver := ConfiguredRouteResolver[struct{}]{Graph: builder.Build()}
	decision, err := resolver.Resolve(context.Background(), RouteInput[struct{}]{Current: core.NodeTypeReason})
	if err != nil || !decision.AdmissionPassed || decision.SelectedNext != core.NodeTypeToolExecute {
		t.Fatalf("valid route = %+v, %v", decision, err)
	}
	invalidGraph := NewGraph[struct{}]()
	invalidGraph.Start(core.NodeTypeReason).End(core.NodeTypeReason)
	invalidResolver := ConfiguredRouteResolver[struct{}]{Graph: invalidGraph.Build()}
	decision, err = invalidResolver.Resolve(context.Background(), RouteInput[struct{}]{Current: core.NodeTypeReason})
	if err == nil || decision.AdmissionPassed || decision.AdmissionReason == "" {
		t.Fatalf("route without a declared target was accepted: %+v, %v", decision, err)
	}
}

func TestConfiguredRouteResolverAppliesGlobalReasoningBudget(t *testing.T) {
	builder := NewGraph[struct{}]()
	builder.Start(core.NodeTypeReason)
	builder.From(core.NodeTypeReason).Route(func(RouteInput[struct{}]) (core.RouteKey, error) {
		return "finalize", nil
	}, map[core.RouteKey]core.NodeType{"reason": core.NodeTypeReason, "finalize": core.NodeTypeFinalize})
	builder.End(core.NodeTypeFinalize)
	// 访问上限是图策略，不是 resolver 的全局配置。
	builder.LimitVisits(core.NodeTypeReason, 2, core.NodeTypeFinalize)
	resolver := ConfiguredRouteResolver[struct{}]{Graph: builder.Build()}
	state := &core.RunState{RuntimeState: core.RuntimeState{Metrics: core.RunMetrics{NodeVisits: map[core.NodeType]int{core.NodeTypeReason: 2}}}}
	decision, err := resolver.Resolve(context.Background(), RouteInput[struct{}]{Current: core.NodeTypeReason, Runtime: state})
	if err != nil || decision.SelectedNext != core.NodeTypeFinalize || !decision.AdmissionPassed {
		t.Fatalf("global reasoning budget route = %+v, err=%v", decision, err)
	}
}

func TestConfiguredRouteResolverChecksConfiguredEdgesAndMetrics(t *testing.T) {
	builder := NewGraph[struct{}]()
	builder.Start(core.NodeTypeReason).From(core.NodeTypeReason).To(core.NodeTypeToolExecute).End(core.NodeTypeToolExecute)
	graph := builder.Build()
	graph.edges[0].Condition = "max_visits"
	graph.edges[0].MaxVisits = 1
	resolver := ConfiguredRouteResolver[struct{}]{Graph: graph}
	state := &core.RunState{RuntimeState: core.RuntimeState{Metrics: core.RunMetrics{NodeVisits: map[core.NodeType]int{core.NodeTypeToolExecute: 1}}}}
	if _, err := resolver.Resolve(context.Background(), RouteInput[struct{}]{Current: core.NodeTypeReason, Runtime: state}); err == nil {
		t.Fatal("visit-limited edge should reject route")
	}
}

func TestConfiguredRouteResolverChecksReentryAndPrerequisites(t *testing.T) {
	builder := NewGraph[struct{}]()
	builder.Start(core.NodeTypeReason).From(core.NodeTypeReason).To(core.NodeTypeToolExecute)
	builder.From(core.NodeTypeToolExecute).To(core.NodeTypeResultReduce).End(core.NodeTypeResultReduce)
	graph := builder.Build()
	graph.edges[1].Requires = []core.NodeType{core.NodeTypeReason}
	resolver := ConfiguredRouteResolver[struct{}]{Graph: graph}
	state := &core.RunState{RuntimeState: core.RuntimeState{Metrics: core.RunMetrics{NodeVisits: map[core.NodeType]int{core.NodeTypeToolExecute: 1}}}}
	if _, err := resolver.Resolve(context.Background(), RouteInput[struct{}]{Current: core.NodeTypeReason, Runtime: state}); err == nil {
		t.Fatal("non-reentrant edge should reject reentry")
	}
	state.Metrics.NodeVisits[core.NodeTypeToolExecute] = 0
	if _, err := resolver.Resolve(context.Background(), RouteInput[struct{}]{Current: core.NodeTypeToolExecute, Runtime: state}); err == nil {
		t.Fatal("missing prerequisite should reject route")
	}
}

func TestConfiguredRouteResolverAllowsImplicitApprovalTransitions(t *testing.T) {
	builder := NewGraph[struct{}]()
	builder.Start(core.NodeTypeToolExecute).From(core.NodeTypeToolExecute).To(core.NodeTypeToolExecute).End(core.NodeTypeApproval).ApprovalTarget(core.NodeTypeApproval)
	resolver := ConfiguredRouteResolver[struct{}]{Graph: builder.Build()}
	state := &core.RunState{RuntimeState: core.RuntimeState{Control: core.ControlState{PendingAction: &core.Action{
		Type:       "tool_call",
		Name:       "write_tool",
		ResumeNode: core.NodeTypeToolExecute,
	}}}}

	decision, err := resolver.Resolve(context.Background(), RouteInput[struct{}]{
		Current: core.NodeTypeToolExecute,
		Output:  core.NodeOutput{},
		Runtime: state,
	})
	if err != nil || !decision.AdmissionPassed || decision.SelectedNext != core.NodeTypeApproval {
		t.Fatalf("进入审批节点的隐式转移未放行：%+v，%v", decision, err)
	}

	decision, err = resolver.Resolve(context.Background(), RouteInput[struct{}]{
		Current: core.NodeTypeApproval,
		Output:  core.NodeOutput{},
		Runtime: state,
	})
	if err != nil || !decision.AdmissionPassed || decision.SelectedNext != core.NodeTypeToolExecute {
		t.Fatalf("审批恢复转移未放行：%+v，%v", decision, err)
	}
}

func TestConfiguredRouteResolverDoesNotInventApprovalTransition(t *testing.T) {
	builder := NewGraph[struct{}]()
	builder.Start(core.NodeTypeToolExecute).From(core.NodeTypeToolExecute).To(core.NodeTypeToolExecute).End(core.NodeTypeApproval).ApprovalTarget(core.NodeTypeApproval)
	resolver := ConfiguredRouteResolver[struct{}]{Graph: builder.Build()}
	decision, err := resolver.Resolve(context.Background(), RouteInput[struct{}]{
		Current: core.NodeTypeToolExecute,
		Output:  core.NodeOutput{},
		Runtime: &core.RunState{},
	})
	if err != nil || !decision.AdmissionPassed || decision.SelectedNext != core.NodeTypeToolExecute {
		t.Fatalf("没有待确认动作时不应进入审批节点：%+v，%v", decision, err)
	}
}

func TestConfiguredRouteResolverResumesApprovalFromPendingAction(t *testing.T) {
	builder := NewGraph[struct{}]()
	builder.Start(core.NodeTypeToolExecute).From(core.NodeTypeToolExecute).To(core.NodeTypeToolExecute).End(core.NodeTypeApproval).ApprovalTarget(core.NodeTypeApproval)
	resolver := ConfiguredRouteResolver[struct{}]{Graph: builder.Build()}
	state := &core.RunState{RuntimeState: core.RuntimeState{Control: core.ControlState{PendingAction: &core.Action{
		Type:       "tool_call",
		Name:       "write_tool",
		ResumeNode: core.NodeTypeToolExecute,
	}}}}
	decision, err := resolver.Resolve(context.Background(), RouteInput[struct{}]{
		Current: core.NodeTypeApproval,
		Output:  core.NodeOutput{},
		Runtime: state,
	})
	if err != nil || !decision.AdmissionPassed || decision.SelectedNext != core.NodeTypeToolExecute {
		t.Fatalf("审批恢复应使用动作声明的恢复节点：%+v，%v", decision, err)
	}
}

func TestValidateGraphRejectsInvalidEdges(t *testing.T) {
	routes := []core.NodeType{core.NodeTypeReason, core.NodeTypeFinalize}
	if err := ValidateGraph(routes, []core.NodeEdge{{From: core.NodeTypeReason, To: core.NodeTypeToolExecute}}); err == nil {
		t.Fatal("edge to disabled node should be rejected")
	}
	if err := ValidateGraph(routes, []core.NodeEdge{{From: core.NodeTypeReason, To: core.NodeTypeFinalize}, {From: core.NodeTypeReason, To: core.NodeTypeFinalize}}); err == nil {
		t.Fatal("duplicate edge should be rejected")
	}
}
