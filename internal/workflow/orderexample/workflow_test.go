package orderexample

import (
	"context"
	"strings"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"
)

func TestOrderWorkflowBindsStateGraphAndCapabilities(t *testing.T) {
	workflow, err := New(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if workflow.Kind() != "order_lookup" {
		t.Fatalf("workflow kind = %q", workflow.Kind())
	}
	payload, err := workflow.NewStatePayload()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := workflow.DecodeStatePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := decoded.(*OrderState)
	if !ok || state.Version != 1 || state.Stage != "lookup" {
		t.Fatalf("unexpected workflow state: %#v", decoded)
	}
	if workflow.NodeTypes()[0] != NodeTypeOrderLookup {
		t.Fatalf("workflow graph has wrong entry: %#v", workflow.NodeTypes())
	}
	if len(workflow.Skills()) != 1 || len(workflow.Tools()) != 1 || workflow.Tools()[0].Source != "order-api" {
		t.Fatalf("workflow capabilities not bound: skills=%v tools=%v", workflow.Skills(), workflow.Tools())
	}
	template, err := workflow.Prompts().Get(context.Background(), core.PromptSystem, "")
	if err != nil || !strings.Contains(template.Render(), "示例订单号查询") {
		t.Fatalf("workflow prompt manager not bound: template=%+v err=%v", template, err)
	}
	state.OrderID = "1001"
	key, err := decideOrderLookup(orchestrator.RouteInput[OrderState]{State: state})
	if err != nil || key != RouteOrderSummary {
		t.Fatalf("state-based route = %q, err=%v", key, err)
	}
}
