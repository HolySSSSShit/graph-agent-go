// Package testkit 提供不依赖业务包的运行时测试夹具。
package testkit

import (
	"context"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"
)

const WorkflowID = "test"
const Answer = "测试运行完成"

type node struct{ name string }

func (n node) Name() string { return n.name }

func (n node) ProgressMessage(orchestrator.NodeInput[struct{}]) string { return n.name }

func (n node) Execute(context.Context, orchestrator.NodeInput[struct{}]) (core.NodeOutput, error) {
	if n.name == "test_end" {
		return core.NodeOutput{Status: core.NodeStatusComplete, Messages: []core.Message{{Role: "assistant", Content: Answer}}}, nil
	}
	return core.NodeOutput{Status: core.NodeStatusContinue}, nil
}

func Workflow() (orchestrator.WorkflowDefinition, error) {
	nodes := orchestrator.NewNodeRegistry[struct{}]()
	for _, name := range []string{"test_start", "test_work", "test_end"} {
		if err := nodes.Register(node{name: name}); err != nil {
			return nil, err
		}
	}
	graph := orchestrator.NewGraph[struct{}]().Start("test_start")
	graph.From("test_start").To("test_work")
	graph.From("test_work").To("test_end")
	graph.End("test_end").BudgetTarget("test_end").FallbackTarget("test_end")
	definition := orchestrator.Definition[struct{}]{ID: WorkflowID, WorkflowGraph: graph.Build(), NodeGroup: nodes, Codec: orchestrator.JSONStateCodec[struct{}]{}}
	return definition, definition.Validate()
}
