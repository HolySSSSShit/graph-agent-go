package orderexample

import (
	"strings"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"
)

const (
	NodeTypeOrderLookup  core.NodeType = "example_order_lookup"
	NodeTypeOrderSummary core.NodeType = "example_order_summary"
	NodeTypeOrderClarify core.NodeType = "example_order_clarify"
	RouteOrderSummary    core.RouteKey = "summary"
	RouteOrderClarify    core.RouteKey = "clarify"
)

func newOrderGraph() orchestrator.Graph[OrderState] {
	builder := orchestrator.NewGraph[OrderState]()
	builder.Start(NodeTypeOrderLookup)
	builder.From(NodeTypeOrderLookup).Route(decideOrderLookup, map[core.RouteKey]core.NodeType{
		RouteOrderSummary: NodeTypeOrderSummary,
		RouteOrderClarify: NodeTypeOrderClarify,
	})
	builder.End(NodeTypeOrderSummary)
	builder.End(NodeTypeOrderClarify)
	builder.FallbackTarget(NodeTypeOrderClarify)
	return builder.Build()
}

// decideOrderLookup 展示 route decider 如何直接读取本 Workflow 的强类型 State。
func decideOrderLookup(input orchestrator.RouteInput[OrderState]) (core.RouteKey, error) {
	if input.State == nil || strings.TrimSpace(input.State.OrderID) == "" {
		return RouteOrderClarify, nil
	}
	return RouteOrderSummary, nil
}
