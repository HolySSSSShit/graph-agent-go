package orderexample

import (
	"context"
	"strings"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"
)

func newOrderNodes() (*orchestrator.NodeRegistry[OrderState], error) {
	nodes := orchestrator.NewNodeRegistry[OrderState]()
	for _, item := range []orchestrator.Node[OrderState]{
		orderStageNode{nodeType: NodeTypeOrderLookup, stage: "lookup"},
		orderStageNode{nodeType: NodeTypeOrderSummary, stage: "completed"},
		orderStageNode{nodeType: NodeTypeOrderClarify, stage: "await_order_id"},
	} {
		if err := nodes.Register(item); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

type orderStageNode struct {
	nodeType core.NodeType
	stage    string
}

func (n orderStageNode) Name() string { return n.nodeType.String() }

func (n orderStageNode) Execute(_ context.Context, input orchestrator.NodeInput[OrderState]) (core.NodeOutput, error) {
	input.State.Stage = n.stage
	if n.nodeType == NodeTypeOrderLookup && input.State.OrderID == "" && input.Runtime != nil {
		input.State.OrderID = strings.TrimSpace(input.Runtime.Input.Content)
	}
	status := core.NodeStatusContinue
	var messages []core.Message
	if n.nodeType == NodeTypeOrderSummary {
		status = core.NodeStatusComplete
		messages = []core.Message{{Role: "assistant", Content: "示例订单查询已完成"}}
	} else if n.nodeType == NodeTypeOrderClarify {
		status = core.NodeStatusComplete
		messages = []core.Message{{Role: "assistant", Content: "请提供订单号"}}
	}
	return core.NodeOutput{Status: status, Messages: messages}, nil
}

var _ orchestrator.Node[OrderState] = orderStageNode{}
