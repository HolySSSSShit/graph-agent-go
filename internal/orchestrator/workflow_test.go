package orchestrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

type countingOrderCodec struct {
	encodes int
	decodes int
}

func (c *countingOrderCodec) New() workflowOrderState {
	return workflowOrderState{Version: 1, Stage: "new"}
}

func (c *countingOrderCodec) Encode(state workflowOrderState) (json.RawMessage, error) {
	c.encodes++
	return json.Marshal(state)
}

func (c *countingOrderCodec) Decode(payload json.RawMessage) (workflowOrderState, error) {
	c.decodes++
	var state workflowOrderState
	if err := json.Unmarshal(payload, &state); err != nil {
		return workflowOrderState{}, err
	}
	return state, nil
}

type workflowOrderState struct {
	Version int    `json:"version"`
	Stage   string `json:"stage"`
}

type checkpointCapture struct {
	checkpoint core.Checkpoint
}

func (s *checkpointCapture) SaveCheckpoint(_ context.Context, checkpoint core.Checkpoint) error {
	s.checkpoint = checkpoint
	return nil
}

func (*checkpointCapture) ListCheckpoints(context.Context, core.Identity, string) ([]core.Checkpoint, error) {
	return nil, nil
}

func (s *checkpointCapture) LatestCheckpoint(context.Context, core.Identity, string) (core.Checkpoint, error) {
	return s.checkpoint, nil
}

func (*checkpointCapture) LatestSuspendedCheckpoint(context.Context, core.Identity, string) (core.Checkpoint, error) {
	return core.Checkpoint{}, nil
}

func (*checkpointCapture) UpdateCheckpointStatus(context.Context, core.Identity, string, string) error {
	return nil
}

func TestWorkflowCheckpointUsesWorkflowCodecForRestoreAndSave(t *testing.T) {
	codec := &countingOrderCodec{}
	definition := Definition[workflowOrderState]{ID: "orders", Codec: codec}
	runner := &Runner{}

	payload, err := definition.NewStatePayload()
	if err != nil {
		t.Fatal(err)
	}
	state := core.RunState{WorkflowState: core.WorkflowState{WorkflowKind: "orders", WorkflowPayload: payload}}
	if err := normalizeWorkflowCheckpoint(definition, &state); err != nil {
		t.Fatalf("restore codec failed: %v", err)
	}
	if codec.decodes != 1 || codec.encodes != 2 {
		t.Fatalf("restore should decode and re-encode through workflow codec: encodes=%d decodes=%d", codec.encodes, codec.decodes)
	}
	state.WorkflowState.Data.(*workflowOrderState).Stage = "updated"

	capture := &checkpointCapture{}
	runner.Checkpoints = capture
	runner.saveCheckpoint(context.Background(), definition, core.RunContext{
		RunID: "run-orders", TraceID: "trace-orders", SessionID: "session-orders",
		Identity: core.Identity{TenantID: "tenant", UserID: "user"}, Step: 1,
	}, "credential", core.NodeType("orders"), core.NodeOutput{Status: core.NodeStatusContinue}, &state, core.RouteDecision{})
	if capture.checkpoint.State.WorkflowKind != "orders" || len(capture.checkpoint.State.WorkflowPayload) == 0 {
		t.Fatalf("checkpoint did not preserve workflow payload: %+v", capture.checkpoint.State)
	}
	if codec.decodes != 1 || codec.encodes != 3 {
		t.Fatalf("save should encode current workflow state without decoding stale payload: encodes=%d decodes=%d", codec.encodes, codec.decodes)
	}
	stored, err := codec.Decode(capture.checkpoint.State.WorkflowPayload)
	if err != nil || stored.Stage != "updated" {
		t.Fatalf("checkpoint did not encode current workflow state: state=%+v err=%v", stored, err)
	}
}

func TestWorkflowCheckpointRejectsPayloadForAnotherWorkflow(t *testing.T) {
	definition := Definition[workflowOrderState]{ID: "orders", Codec: &countingOrderCodec{}}
	state := core.RunState{WorkflowState: core.WorkflowState{WorkflowKind: "other", WorkflowPayload: json.RawMessage(`{"version":1}`)}}
	if err := normalizeWorkflowCheckpoint(definition, &state); err == nil {
		t.Fatal("checkpoint from another workflow should be rejected")
	}
}

func TestWorkflowCheckpointStoreDecodesPayloadOnRead(t *testing.T) {
	codec := &countingOrderCodec{}
	definition := Definition[workflowOrderState]{ID: "orders", Codec: codec}
	payload, err := definition.NewStatePayload()
	if err != nil {
		t.Fatal(err)
	}
	base := &checkpointCapture{checkpoint: core.Checkpoint{
		ID: "checkpoint-orders", State: core.RunState{WorkflowState: core.WorkflowState{WorkflowKind: "orders", WorkflowPayload: payload}},
	}}
	view := WorkflowCheckpointStore{Store: base, Workflow: definition}
	checkpoint, err := view.LatestCheckpoint(context.Background(), core.Identity{}, "run-orders")
	if err != nil {
		t.Fatalf("workflow checkpoint read failed: %v", err)
	}
	if checkpoint.State.WorkflowKind != "orders" || len(checkpoint.State.WorkflowPayload) == 0 {
		t.Fatalf("decoded checkpoint lost workflow payload: %+v", checkpoint.State)
	}
	if codec.decodes != 1 || codec.encodes != 2 {
		t.Fatalf("read should decode and re-encode through workflow codec: encodes=%d decodes=%d", codec.encodes, codec.decodes)
	}
}

func TestRunnerCheckpointControlPathUsesWorkflowCodec(t *testing.T) {
	codec := &countingOrderCodec{}
	graph := NewGraph[workflowOrderState]().Start(core.NodeType("orders")).End(core.NodeType("orders")).Build()
	nodes := NewNodeRegistry[workflowOrderState]()
	if err := nodes.Register(orderWorkflowTestNode{}); err != nil {
		t.Fatal(err)
	}
	definition := Definition[workflowOrderState]{ID: "orders", WorkflowGraph: graph, NodeGroup: nodes, Codec: codec}
	workflows, err := NewWorkflowRegistry("orders", definition)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := definition.NewStatePayload()
	if err != nil {
		t.Fatal(err)
	}
	capture := &checkpointCapture{checkpoint: core.Checkpoint{
		ID: "control", RunID: "run", State: core.RunState{WorkflowState: core.WorkflowState{WorkflowKind: "orders", WorkflowPayload: payload}},
	}}
	runner := &Runner{Workflows: workflows, Checkpoints: capture}
	checkpoint, err := runner.LatestCheckpoint(context.Background(), core.Identity{}, "run")
	if err != nil || checkpoint.State.WorkflowState.Data == nil || codec.decodes != 1 {
		t.Fatalf("控制面读取未经过 codec：checkpoint=%+v decodes=%d err=%v", checkpoint, codec.decodes, err)
	}
	checkpoint.Status = core.CheckpointStatusResumed
	if err := runner.SaveWorkflowCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	if capture.checkpoint.State.WorkflowState.Data != nil || codec.encodes < 3 {
		t.Fatalf("控制面保存未经过 codec：encodes=%d state=%+v", codec.encodes, capture.checkpoint.State)
	}
}

func TestWorkflowRegistryResolvesExplicitAndDefaultDefinitions(t *testing.T) {
	first := testWorkflowDefinition(t, "first")
	second := testWorkflowDefinition(t, "second")
	registry, err := NewWorkflowRegistry("first", first, second)
	if err != nil {
		t.Fatal(err)
	}
	defaultWorkflow, err := registry.Resolve("")
	if err != nil || defaultWorkflow.Kind() != "first" {
		t.Fatalf("default workflow = %v, %v", defaultWorkflow, err)
	}
	explicit, err := registry.Resolve("second")
	if err != nil || explicit.Kind() != "second" {
		t.Fatalf("explicit workflow = %v, %v", explicit, err)
	}
	if kinds := registry.Kinds(); len(kinds) != 2 || kinds[0] != "first" || kinds[1] != "second" {
		t.Fatalf("workflow kinds = %v", kinds)
	}
}

func TestWorkflowRegistryRejectsInvalidRegistration(t *testing.T) {
	definition := testWorkflowDefinition(t, "same")
	if _, err := NewWorkflowRegistry("missing", definition); err == nil {
		t.Fatal("unregistered default workflow should be rejected")
	}
	if _, err := NewWorkflowRegistry("same", definition, definition); err == nil {
		t.Fatal("duplicate workflow kind should be rejected")
	}
}

func testWorkflowDefinition(t *testing.T, kind string) Definition[struct{}] {
	t.Helper()
	graph := NewGraph[struct{}]().Start(core.NodeType(kind)).End(core.NodeType(kind)).Build()
	nodes := NewNodeRegistry[struct{}]()
	if err := nodes.Register(workflowTestNode{name: kind}); err != nil {
		t.Fatal(err)
	}
	return Definition[struct{}]{ID: kind, WorkflowGraph: graph, NodeGroup: nodes, Codec: JSONStateCodec[struct{}]{}}
}

type workflowTestNode struct{ name string }

type orderWorkflowTestNode struct{}

func (orderWorkflowTestNode) Name() string { return "orders" }
func (orderWorkflowTestNode) Execute(context.Context, NodeInput[workflowOrderState]) (core.NodeOutput, error) {
	return core.NodeOutput{Status: core.NodeStatusComplete}, nil
}

func (n workflowTestNode) Name() string { return n.name }

func (workflowTestNode) Execute(context.Context, NodeInput[struct{}]) (core.NodeOutput, error) {
	return core.NodeOutput{Status: core.NodeStatusComplete}, nil
}
