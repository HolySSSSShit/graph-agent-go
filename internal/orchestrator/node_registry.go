package orchestrator

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

// NodeInput 是 Workflow 节点唯一可见的执行输入。State 是当前 Workflow 的
// 强类型业务状态，Runtime 是所有 Workflow 共用的只读运行时视图。
type NodeInput[T any] struct {
	Run      core.RunContext
	Messages []core.Message
	Runtime  *core.RunState
	State    *T
	Events   chan<- core.NodeEvent
}

// Node 通过类型参数把节点实现绑定到所属 Workflow 的 State。
type Node[T any] interface {
	Name() string
	Execute(context.Context, NodeInput[T]) (core.NodeOutput, error)
}

// NodeProgressProvider 是强类型节点可选的进度消息能力。
type NodeProgressProvider[T any] interface {
	ProgressMessage(NodeInput[T]) string
}

// NodeRegistry 保存某个 Workflow 已实例化的节点。
// 节点类型和业务依赖由 Workflow 自己负责，编排层只按 NodeType 查找。
type NodeRegistry[T any] struct {
	mu    sync.RWMutex
	nodes map[core.NodeType]Node[T]
}

func NewNodeRegistry[T any]() *NodeRegistry[T] {
	return &NodeRegistry[T]{nodes: make(map[core.NodeType]Node[T])}
}

func (r *NodeRegistry[T]) Register(node Node[T]) error {
	if node == nil || node.Name() == "" {
		return errors.New("invalid node")
	}
	name := core.NodeType(node.Name())
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.nodes[name]; exists {
		return errors.New("duplicate node: " + node.Name())
	}
	r.nodes[name] = node
	return nil
}

// Replace 替换同类型节点实现；正在执行的节点不受影响。
func (r *NodeRegistry[T]) Replace(node Node[T]) error {
	if node == nil || node.Name() == "" {
		return errors.New("invalid node")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[core.NodeType(node.Name())] = node
	return nil
}

func (r *NodeRegistry[T]) Resolve(name core.NodeType) (Node[T], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	node, exists := r.nodes[name]
	if !exists {
		return nil, errors.New("node not found: " + string(name))
	}
	return node, nil
}

func (r *NodeRegistry[T]) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.nodes))
	for name := range r.nodes {
		names = append(names, string(name))
	}
	sort.Strings(names)
	return names
}
