package orchestrator

import (
	"fmt"
	"github.com/HolySSSSShit/go-agent/internal/core"
	"maps"
	"slices"
)

// RouteInput 同时提供通用运行时和 Workflow 的强类型业务状态。
type RouteInput[T any] struct {
	Run     core.RunContext
	Current core.NodeType
	Output  core.NodeOutput
	Runtime *core.RunState
	State   *T
}

// RouteDecider 根据当前 Workflow State 选择语义路由 key。
// resolver 会把 key 映射为 NodeType，再执行边准入检查。
type RouteDecider[T any] func(RouteInput[T]) (core.RouteKey, error)

type ConditionalRoute[T any] struct {
	From    core.NodeType
	Decide  RouteDecider[T]
	Targets map[core.RouteKey]core.NodeType
}

// NodeVisitLimit 描述某个节点执行完指定次数后的图内降级目标。
// 这是 Workflow 的图策略，不属于全局运行配置。
type NodeVisitLimit struct {
	MaxVisits int
	Target    core.NodeType
}

type Graph[T any] struct {
	edges    []core.NodeEdge
	routes   []ConditionalRoute[T]
	nodes    []core.NodeType
	nodeSet  map[core.NodeType]struct{}
	entry    core.NodeType
	ends     map[core.NodeType]struct{}
	limits   map[core.NodeType]NodeVisitLimit
	budget   core.NodeType
	approval core.NodeType
	fallback core.NodeType
	err      error
}

type GraphBuilder[T any] struct {
	graph         Graph[T]
	sources       map[core.NodeType]*EdgeBuilder[T]
	staticSources map[core.NodeType]bool
	staticCounts  map[core.NodeType]int
	routeSources  map[core.NodeType]bool
	nodeSet       map[core.NodeType]struct{}
	nodes         []core.NodeType
	entry         core.NodeType
	ends          map[core.NodeType]struct{}
	limits        map[core.NodeType]NodeVisitLimit
	budget        core.NodeType
	approval      core.NodeType
	fallback      core.NodeType
	err           error
}

func NewGraph[T any]() *GraphBuilder[T] {
	return &GraphBuilder[T]{
		sources:       make(map[core.NodeType]*EdgeBuilder[T]),
		staticSources: make(map[core.NodeType]bool),
		staticCounts:  make(map[core.NodeType]int),
		routeSources:  make(map[core.NodeType]bool),
		ends:          make(map[core.NodeType]struct{}),
		limits:        make(map[core.NodeType]NodeVisitLimit),
		nodeSet:       make(map[core.NodeType]struct{}),
	}
}

// End 声明一个合法终点。只有终点节点返回 complete 或 wait_user 时，运行才可对外结束。
func (g *GraphBuilder[T]) End(node core.NodeType) *GraphBuilder[T] {
	if node == "" {
		g.err = fmt.Errorf("graph end node is required")
		return g
	}
	g.addNode(node)
	g.ends[node] = struct{}{}
	return g
}

// Start 声明图的唯一入口。入口不依赖配置节点顺序。
func (g *GraphBuilder[T]) Start(node core.NodeType) *GraphBuilder[T] {
	if node == "" {
		g.err = fmt.Errorf("graph entry node is required")
		return g
	}
	if g.entry != "" && g.entry != node {
		g.err = fmt.Errorf("graph has multiple entry nodes: %s and %s", g.entry, node)
		return g
	}
	g.entry = node
	g.addNode(node)
	if index := slices.Index(g.nodes, node); index > 0 {
		copy(g.nodes[1:index+1], g.nodes[0:index])
		g.nodes[0] = node
	}
	return g
}

// LimitVisits 声明节点执行次数达到上限后的目标节点。目标仍须是图中的合法节点，
// 具体是否为终点由 Workflow 自己决定。
func (g *GraphBuilder[T]) LimitVisits(node core.NodeType, maxVisits int, target core.NodeType) *GraphBuilder[T] {
	if node == "" || target == "" {
		g.err = fmt.Errorf("graph visit limit node and target are required")
		return g
	}
	if maxVisits < 1 {
		g.err = fmt.Errorf("graph visit limit must be positive: %s", node)
		return g
	}
	g.addNode(node)
	g.addNode(target)
	g.limits[node] = NodeVisitLimit{MaxVisits: maxVisits, Target: target}
	return g
}

// BudgetTarget 声明总运行预算耗尽后的图内目标节点。
func (g *GraphBuilder[T]) BudgetTarget(target core.NodeType) *GraphBuilder[T] {
	if target == "" {
		g.err = fmt.Errorf("graph budget target is required")
		return g
	}
	g.addNode(target)
	g.budget = target
	return g
}

// ApprovalTarget 声明待确认动作统一进入的控制节点。未声明时，Workflow
// 不能产生 PendingAction，也不会由通用层猜测审批节点名称。
func (g *GraphBuilder[T]) ApprovalTarget(target core.NodeType) *GraphBuilder[T] {
	if target == "" {
		g.err = fmt.Errorf("graph approval target is required")
		return g
	}
	g.addNode(target)
	g.approval = target
	return g
}

// FallbackTarget 声明节点违反终止协议时进入的安全终点。
func (g *GraphBuilder[T]) FallbackTarget(target core.NodeType) *GraphBuilder[T] {
	if target == "" {
		g.err = fmt.Errorf("graph fallback target is required")
		return g
	}
	g.addNode(target)
	g.fallback = target
	return g
}

// Reentrant 将已声明的边标记为允许重入。重入策略由 Workflow 在构图时声明，
// 不通过全局配置或运行器字段补充。
func (g *GraphBuilder[T]) Reentrant(from, to core.NodeType) *GraphBuilder[T] {
	for index := range g.graph.edges {
		if g.graph.edges[index].From == from && g.graph.edges[index].To == to {
			g.graph.edges[index].Reentrant = true
			return g
		}
	}
	g.err = fmt.Errorf("reentrant edge is not defined: %s -> %s", from, to)
	return g
}

func (g *GraphBuilder[T]) addNode(node core.NodeType) {
	if _, exists := g.nodeSet[node]; exists {
		return
	}
	g.nodeSet[node] = struct{}{}
	g.nodes = append(g.nodes, node)
}

func (g *GraphBuilder[T]) From(node core.NodeType) *EdgeBuilder[T] {
	if node == "" {
		g.err = fmt.Errorf("graph source node is required")
		return &EdgeBuilder[T]{graph: g, from: node}
	}
	g.addNode(node)
	if existing, ok := g.sources[node]; ok {
		return existing
	}
	builder := &EdgeBuilder[T]{graph: g, from: node}
	g.sources[node] = builder
	return builder
}

type EdgeBuilder[T any] struct {
	graph *GraphBuilder[T]
	from  core.NodeType
}

// To 声明一条静态边。参数使用 NodeType，避免拓扑依赖无类型字符串或配置文件字符串。
func (e *EdgeBuilder[T]) To(target core.NodeType) *GraphBuilder[T] {
	if target == "" {
		e.graph.err = fmt.Errorf("graph target node is required")
		return e.graph
	}
	e.graph.addNode(target)
	if e.graph.routeSources[e.from] {
		e.graph.err = fmt.Errorf("node %s already has a conditional route", e.from)
		return e.graph
	}
	e.graph.staticSources[e.from] = true
	e.graph.staticCounts[e.from]++
	if e.graph.staticCounts[e.from] > 1 {
		e.graph.err = fmt.Errorf("node %s has multiple static targets; use Route", e.from)
		return e.graph
	}
	e.graph.graph.edges = append(e.graph.graph.edges, core.NodeEdge{From: e.from, To: target})
	return e.graph
}

// Route 声明一组条件边。决策函数返回 RouteKey；Targets 是唯一的 key 到节点映射，
// 注册时会复制该映射，避免调用方后续修改图定义。
func (e *EdgeBuilder[T]) Route(decide RouteDecider[T], targets map[core.RouteKey]core.NodeType) *GraphBuilder[T] {
	e.graph.addNode(e.from)
	if e.graph.staticSources[e.from] || e.graph.routeSources[e.from] {
		e.graph.err = fmt.Errorf("node %s already has an edge definition", e.from)
		return e.graph
	}
	e.graph.routeSources[e.from] = true
	e.graph.graph.routes = append(e.graph.graph.routes, ConditionalRoute[T]{From: e.from, Decide: decide, Targets: maps.Clone(targets)})
	seenTargets := make(map[core.NodeType]struct{}, len(targets))
	for _, target := range targets {
		e.graph.addNode(target)
		if _, seen := seenTargets[target]; seen {
			continue
		}
		seenTargets[target] = struct{}{}
		e.graph.graph.edges = append(e.graph.graph.edges, core.NodeEdge{From: e.from, To: target})
	}
	return e.graph
}

func (g *GraphBuilder[T]) Build() Graph[T] {
	result := g.graph
	result.edges = slices.Clone(g.graph.edges)
	result.routes = make([]ConditionalRoute[T], len(g.graph.routes))
	for index, route := range g.graph.routes {
		result.routes[index] = ConditionalRoute[T]{From: route.From, Decide: route.Decide, Targets: maps.Clone(route.Targets)}
	}
	result.err = g.err
	result.nodes = slices.Clone(g.nodes)
	result.nodeSet = maps.Clone(g.nodeSet)
	result.entry = g.entry
	result.ends = maps.Clone(g.ends)
	result.limits = maps.Clone(g.limits)
	result.budget = g.budget
	result.approval = g.approval
	result.fallback = g.fallback
	return result
}

func (g Graph[T]) IsEnd(node core.NodeType) bool {
	_, ok := g.ends[node]
	return ok
}

// NodeTypes 返回图定义中的节点顺序；入口节点始终位于第一项。
func (g Graph[T]) NodeTypes() []core.NodeType {
	return slices.Clone(g.nodes)
}

func (g Graph[T]) Entry() (core.NodeType, bool) {
	return g.entry, g.entry != ""
}

func (g Graph[T]) BudgetTarget() (core.NodeType, bool) {
	return g.budget, g.budget != ""
}

func (g Graph[T]) VisitLimit(node core.NodeType) (NodeVisitLimit, bool) {
	limit, ok := g.limits[node]
	return limit, ok
}

func (g Graph[T]) contains(node core.NodeType) bool {
	_, ok := g.nodeSet[node]
	return ok
}

func containsNode(nodes []core.NodeType, target core.NodeType) bool {
	return slices.Contains(nodes, target)
}

func ValidateGraphDefinition[T any](graph Graph[T]) error {
	if graph.err != nil {
		return graph.err
	}
	if graph.entry == "" {
		return fmt.Errorf("graph entry node is required")
	}
	if len(graph.ends) == 0 {
		return fmt.Errorf("graph has no end nodes")
	}
	for end := range graph.ends {
		if _, ok := graph.nodeSet[end]; !ok {
			return fmt.Errorf("graph end node is not registered: %s", end)
		}
	}
	if _, ok := graph.nodeSet[graph.entry]; !ok {
		return fmt.Errorf("graph entry node is not registered: %s", graph.entry)
	}
	if graph.budget != "" {
		if _, ok := graph.nodeSet[graph.budget]; !ok {
			return fmt.Errorf("graph budget target is not registered: %s", graph.budget)
		}
		if _, ok := graph.ends[graph.budget]; !ok {
			return fmt.Errorf("graph budget target is not an end node: %s", graph.budget)
		}
	}
	if graph.approval != "" {
		if _, ok := graph.nodeSet[graph.approval]; !ok {
			return fmt.Errorf("graph approval target is not registered: %s", graph.approval)
		}
		if _, ok := graph.ends[graph.approval]; !ok {
			return fmt.Errorf("graph approval target is not an end node: %s", graph.approval)
		}
	}
	if graph.fallback != "" {
		if _, ok := graph.ends[graph.fallback]; !ok {
			return fmt.Errorf("graph fallback target is not an end node: %s", graph.fallback)
		}
	}
	for node, limit := range graph.limits {
		if _, ok := graph.nodeSet[node]; !ok {
			return fmt.Errorf("graph visit limit node is not registered: %s", node)
		}
		if limit.MaxVisits < 1 {
			return fmt.Errorf("graph visit limit must be positive: %s", node)
		}
		if _, ok := graph.nodeSet[limit.Target]; !ok {
			return fmt.Errorf("graph visit limit target is not registered: %s", limit.Target)
		}
		if !graphHasEdge(graph.edges, node, limit.Target) {
			return fmt.Errorf("graph visit limit target is missing graph edge: %s -> %s", node, limit.Target)
		}
	}
	if len(graph.nodes) > 1 && len(graph.edges) == 0 && len(graph.routes) == 0 {
		return fmt.Errorf("graph has no transitions")
	}
	ignoredIsolated := []core.NodeType{graph.approval, graph.budget, graph.fallback}
	if err := ValidateGraph(graph.nodes, graph.edges, ignoredIsolated...); err != nil {
		return err
	}
	nodes := graph.nodeSet
	conditionalSources := make(map[core.NodeType]struct{}, len(graph.routes))
	conditionalTargets := make(map[core.NodeType]map[core.NodeType]struct{}, len(graph.routes))
	for _, route := range graph.routes {
		if route.From == "" {
			return fmt.Errorf("conditional route source is required")
		}
		if _, ok := nodes[route.From]; !ok {
			return fmt.Errorf("conditional route source node is not enabled: %s", route.From)
		}
		if _, exists := conditionalSources[route.From]; exists {
			return fmt.Errorf("duplicate conditional route source: %s", route.From)
		}
		conditionalSources[route.From] = struct{}{}
		if _, exists := conditionalTargets[route.From]; !exists {
			conditionalTargets[route.From] = make(map[core.NodeType]struct{}, len(route.Targets))
		}
		if route.Decide == nil {
			return fmt.Errorf("conditional route from %s has no decider", route.From)
		}
		if len(route.Targets) == 0 {
			return fmt.Errorf("conditional route from %s has no targets", route.From)
		}
		for key, target := range route.Targets {
			if key == "" {
				return fmt.Errorf("conditional route from %s contains an empty key", route.From)
			}
			if target == "" {
				return fmt.Errorf("conditional route %s contains an empty target", route.From)
			}
			if _, ok := nodes[target]; !ok {
				return fmt.Errorf("conditional route target node is not enabled: %s", target)
			}
			conditionalTargets[route.From][target] = struct{}{}
			if !graphHasEdge(graph.edges, route.From, target) {
				return fmt.Errorf("conditional route target is missing graph edge: %s -> %s", route.From, target)
			}
		}
	}
	for source, targets := range conditionalTargets {
		for _, edge := range graph.edges {
			if edge.From == source {
				if _, ok := targets[edge.To]; !ok {
					return fmt.Errorf("conditional route source has undeclared graph edge: %s -> %s", source, edge.To)
				}
			}
		}
	}
	staticTargets := make(map[core.NodeType]map[core.NodeType]struct{})
	for _, edge := range graph.edges {
		if _, conditional := conditionalSources[edge.From]; conditional {
			continue
		}
		if _, exists := staticTargets[edge.From]; !exists {
			staticTargets[edge.From] = make(map[core.NodeType]struct{})
		}
		staticTargets[edge.From][edge.To] = struct{}{}
	}
	for source, targets := range staticTargets {
		if len(targets) > 1 {
			return fmt.Errorf("node %s has multiple static targets; use Route", source)
		}
	}
	return nil
}

func graphHasEdge(edges []core.NodeEdge, from, to core.NodeType) bool {
	for _, edge := range edges {
		if edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

// ValidateGraph 在服务启动时校验节点图的结构，避免运行到一半才发现非法边。
func ValidateGraph(nodesList []core.NodeType, edges []core.NodeEdge, ignoredIsolated ...core.NodeType) error {
	if len(nodesList) == 0 {
		return fmt.Errorf("graph has no nodes")
	}
	nodes := make(map[core.NodeType]struct{}, len(nodesList))
	for _, node := range nodesList {
		if node == "" {
			return fmt.Errorf("graph contains empty node")
		}
		if _, exists := nodes[node]; exists {
			return fmt.Errorf("duplicate graph node: %s", node)
		}
		nodes[node] = struct{}{}
	}
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if _, ok := nodes[edge.From]; !ok {
			return fmt.Errorf("edge source node is not enabled: %s", edge.From)
		}
		if _, ok := nodes[edge.To]; !ok {
			return fmt.Errorf("edge target node is not enabled: %s", edge.To)
		}
		if edge.From == "" || edge.To == "" {
			return fmt.Errorf("graph edge endpoints are required")
		}
		if edge.MaxVisits < 0 {
			return fmt.Errorf("edge max_visits cannot be negative: %s -> %s", edge.From, edge.To)
		}
		for _, required := range edge.Requires {
			if _, ok := nodes[required]; !ok {
				return fmt.Errorf("edge prerequisite node is not enabled: %s", required)
			}
		}
		key := string(edge.From) + "\x00" + string(edge.To)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate graph edge: %s -> %s", edge.From, edge.To)
		}
		seen[key] = struct{}{}
	}
	if len(edges) > 0 {
		reachable := reachableNodes(nodesList[0], edges)
		ignored := make(map[core.NodeType]struct{}, len(ignoredIsolated))
		for _, node := range ignoredIsolated {
			if node != "" {
				ignored[node] = struct{}{}
			}
		}
		for _, node := range nodesList {
			if _, skip := ignored[node]; skip {
				continue
			}
			if _, ok := reachable[node]; !ok {
				return fmt.Errorf("isolated graph node without path from %s: %s", nodesList[0], node)
			}
		}
	}
	return validateCycles(edges)
}

func reachableNodes(root core.NodeType, edges []core.NodeEdge) map[core.NodeType]struct{} {
	adjacency := make(map[core.NodeType][]core.NodeType)
	for _, edge := range edges {
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
	}
	reachable := map[core.NodeType]struct{}{root: {}}
	queue := []core.NodeType{root}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[current] {
			if _, seen := reachable[next]; seen {
				continue
			}
			reachable[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return reachable
}

func validateCycles(edges []core.NodeEdge) error {
	adjacency := make(map[core.NodeType][]core.NodeType)
	for _, edge := range edges {
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
	}
	for _, edge := range edges {
		if !pathExists(adjacency, edge.To, edge.From) {
			continue
		}
		if !edge.Reentrant {
			return fmt.Errorf("graph cycle edge is not reentrant: %s -> %s", edge.From, edge.To)
		}
	}
	return nil
}

func pathExists(adjacency map[core.NodeType][]core.NodeType, start, target core.NodeType) bool {
	if start == target {
		return true
	}
	seen := map[core.NodeType]struct{}{start: {}}
	queue := []core.NodeType{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[current] {
			if next == target {
				return true
			}
			if _, exists := seen[next]; exists {
				continue
			}
			seen[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return false
}
