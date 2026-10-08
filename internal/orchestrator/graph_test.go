package orchestrator

import (
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestGraphRequiresDeclaredEndNode(t *testing.T) {
	graph := NewGraph[struct{}]()
	graph.Start(core.NodeTypeReason).From(core.NodeTypeReason).To(core.NodeTypeFinalize)
	if err := ValidateGraphDefinition(graph.Build()); err == nil {
		t.Fatal("graph without End declaration must be rejected")
	}
}

func TestGraphRejectsMultipleDefinitionsForOneSource(t *testing.T) {
	graph := NewGraph[struct{}]()
	graph.From(core.NodeTypeReason).To(core.NodeTypeFinalize)
	graph.From(core.NodeTypeReason).Route(func(RouteInput[struct{}]) (core.RouteKey, error) {
		return "done", nil
	}, map[core.RouteKey]core.NodeType{"done": core.NodeTypeFinalize})

	if err := ValidateGraphDefinition(graph.Build()); err == nil {
		t.Fatal("expected duplicate source definition to be rejected")
	}
}

func TestValidateGraphDefinitionRejectsConditionalTargetWithoutEdge(t *testing.T) {
	graph := Graph[struct{}]{
		routes: []ConditionalRoute[struct{}]{{
			From:    core.NodeTypeReason,
			Decide:  func(RouteInput[struct{}]) (core.RouteKey, error) { return "done", nil },
			Targets: map[core.RouteKey]core.NodeType{"done": core.NodeTypeFinalize},
		}},
	}
	if err := ValidateGraphDefinition(graph); err == nil {
		t.Fatal("expected conditional target without graph edge to be rejected")
	}
}

func TestValidateGraphRejectsNonReentrantCycle(t *testing.T) {
	routes := []core.NodeType{core.NodeTypeReason, core.NodeTypeFinalize}
	edges := []core.NodeEdge{
		{From: core.NodeTypeReason, To: core.NodeTypeFinalize},
		{From: core.NodeTypeFinalize, To: core.NodeTypeReason},
	}
	if err := ValidateGraph(routes, edges); err == nil {
		t.Fatal("expected non-reentrant cycle to be rejected")
	}
}

func TestValidateGraphAllowsDeclaredReentrantCycle(t *testing.T) {
	routes := []core.NodeType{core.NodeTypeReason, core.NodeTypeFinalize}
	edges := []core.NodeEdge{
		{From: core.NodeTypeReason, To: core.NodeTypeFinalize, Reentrant: true},
		{From: core.NodeTypeFinalize, To: core.NodeTypeReason, Reentrant: true},
	}
	if err := ValidateGraph(routes, edges); err != nil {
		t.Fatalf("declared reentrant cycle should be allowed: %v", err)
	}
}

func TestValidateGraphRejectsPartiallyReentrantCycle(t *testing.T) {
	routes := []core.NodeType{core.NodeTypeReason, core.NodeTypeFinalize}
	edges := []core.NodeEdge{
		{From: core.NodeTypeReason, To: core.NodeTypeFinalize, Reentrant: true},
		{From: core.NodeTypeFinalize, To: core.NodeTypeReason},
	}
	if err := ValidateGraph(routes, edges); err == nil {
		t.Fatal("环中的每条回访边都必须声明为可重入")
	}
}

func TestValidateGraphRejectsUnmarkedShortcutInsideCycle(t *testing.T) {
	a := core.NodeType("a")
	b := core.NodeType("b")
	c := core.NodeType("c")
	edges := []core.NodeEdge{
		{From: a, To: b, Reentrant: true},
		{From: b, To: c, Reentrant: true},
		{From: c, To: a, Reentrant: true},
		{From: a, To: c},
	}
	if err := ValidateGraph([]core.NodeType{a, b, c}, edges); err == nil {
		t.Fatal("强连通区域中的每条环内边都必须声明为可重入")
	}
}

func TestGraphStartIsReachabilityRootRegardlessOfCallOrder(t *testing.T) {
	start := core.NodeType("start")
	middle := core.NodeType("middle")
	end := core.NodeType("end")
	builder := NewGraph[struct{}]()
	builder.From(start).To(middle)
	builder.From(middle).To(end)
	builder.Start(start).End(end)
	graph := builder.Build()
	if nodes := graph.NodeTypes(); len(nodes) == 0 || nodes[0] != start {
		t.Fatalf("入口节点不是可达性根节点：%v", nodes)
	}
	if err := ValidateGraphDefinition(graph); err != nil {
		t.Fatalf("后声明入口的合法图未通过校验：%v", err)
	}
}

func TestGraphRequiresTerminalControlTargets(t *testing.T) {
	start := core.NodeType("start")
	control := core.NodeType("control")
	builder := NewGraph[struct{}]().Start(start)
	builder.From(start).To(control)
	builder.End(start).ApprovalTarget(control)
	if err := ValidateGraphDefinition(builder.Build()); err == nil {
		t.Fatal("approval target 不是 End 时应拒绝启动")
	}

	builder = NewGraph[struct{}]().Start(start)
	builder.From(start).To(control)
	builder.End(start).BudgetTarget(control)
	if err := ValidateGraphDefinition(builder.Build()); err == nil {
		t.Fatal("budget target 不是 End 时应拒绝启动")
	}
}

func TestGraphRequiresVisitLimitEdge(t *testing.T) {
	start := core.NodeType("start")
	end := core.NodeType("end")
	builder := NewGraph[struct{}]().Start(start).End(end)
	builder.LimitVisits(start, 1, end)
	if err := ValidateGraphDefinition(builder.Build()); err == nil {
		t.Fatal("访问上限目标没有对应图边时应拒绝启动")
	}
}

func TestBuiltGraphDoesNotChangeWithBuilder(t *testing.T) {
	start := core.NodeType("start")
	end := core.NodeType("end")
	builder := NewGraph[struct{}]().Start(start)
	builder.From(start).To(end)
	builder.End(end)
	graph := builder.Build()
	builder.Reentrant(start, end)
	if graph.edges[0].Reentrant {
		t.Fatal("Build 后的图不应被 builder 后续修改")
	}
}

func TestValidateGraphRejectsUnreachableNode(t *testing.T) {
	routes := []core.NodeType{core.NodeTypeReason, core.NodeTypeFinalize, core.NodeTypeClarify}
	edges := []core.NodeEdge{{From: core.NodeTypeReason, To: core.NodeTypeFinalize}}
	if err := ValidateGraph(routes, edges); err == nil {
		t.Fatal("expected unreachable node to be rejected")
	}
}
