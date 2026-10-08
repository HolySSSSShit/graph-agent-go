package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// StateCodec 负责把某个 Workflow 的强类型业务状态转换为 checkpoint payload。
// Runner 只依赖擦除类型后的接口，Workflow 内部仍然可以使用具体的 State 类型。
type StateCodec[T any] interface {
	New() T
	Encode(T) (json.RawMessage, error)
	Decode(json.RawMessage) (T, error)
}

// JSONStateCodec 是默认的 StateCodec 实现。业务状态固定时只需提供类型参数，
// 后续如有版本迁移、压缩或其他编码需求，可以替换 Workflow 内部实现。
type JSONStateCodec[T any] struct {
	Factory func() T
}

func (c JSONStateCodec[T]) New() (state T) {
	if c.Factory != nil {
		return c.Factory()
	}
	return state
}

func (JSONStateCodec[T]) Encode(state T) (json.RawMessage, error) {
	return json.Marshal(state)
}

func (c JSONStateCodec[T]) Decode(payload json.RawMessage) (state T, err error) {
	state = c.New()
	err = json.Unmarshal(payload, &state)
	return state, err
}

// ToolBinding 声明 Workflow 可以使用的工具来源和工具名称。
// 它是能力白名单，不替代 Go agent policy 或 PHP 业务权限校验。
type ToolBinding struct {
	Source string
	Tools  []string
	All    bool
}

// WorkflowDefinition 是 Runner 使用的类型擦除边界。具体 Workflow 可以通过
// Definition[T] 获得编译期类型安全，再由本接口接入统一运行器。
type WorkflowDefinition interface {
	Kind() string
	Validate() error
	NodeTypes() []core.NodeType
	Entry() (core.NodeType, bool)
	Contains(core.NodeType) bool
	IsEnd(core.NodeType) bool
	BudgetTarget() (core.NodeType, bool)
	FallbackTarget() (core.NodeType, bool)
	ApprovalTarget() (core.NodeType, bool)
	ExecuteNode(context.Context, core.NodeType, RuntimeNodeInput) (core.NodeOutput, error)
	ProgressMessage(core.NodeType, RuntimeNodeInput) (string, error)
	ResolveRoute(context.Context, core.RunContext, core.NodeType, core.NodeOutput, *core.RunState) (core.RouteDecision, error)
	Skills() []string
	Tools() []ToolBinding
	Prompts() core.PromptManager
	NewStatePayload() (json.RawMessage, error)
	NewStateValue() (any, error)
	DecodeStatePayload(json.RawMessage) (any, error)
	EncodeStateValue(any) (json.RawMessage, error)
	Restore(context.Context, core.SessionStore, core.RunContext, any) ([]core.TraceEvent, error)
	AfterNode(context.Context, core.NodeType, *core.RunState, any) ([]WorkflowDiagnostic, error)
	HandleProtocolViolation(any, string) error
	Persist(context.Context, core.SessionStore, core.RunContext, any) ([]core.TraceEvent, error)
}

// WorkflowRegistry 保存进程启动时注册的 Workflow Definition。
// 新请求可以显式选择 kind；空 kind 只解析为启动时指定的默认 Workflow。
type WorkflowRegistry struct {
	defaultKind string
	definitions map[string]WorkflowDefinition
}

func NewWorkflowRegistry(defaultKind string, definitions ...WorkflowDefinition) (*WorkflowRegistry, error) {
	registry := &WorkflowRegistry{
		defaultKind: strings.TrimSpace(defaultKind),
		definitions: make(map[string]WorkflowDefinition, len(definitions)),
	}
	for _, definition := range definitions {
		if definition == nil {
			return nil, fmt.Errorf("workflow definition is nil")
		}
		if err := definition.Validate(); err != nil {
			return nil, fmt.Errorf("validate workflow %q: %w", definition.Kind(), err)
		}
		kind := strings.TrimSpace(definition.Kind())
		if _, exists := registry.definitions[kind]; exists {
			return nil, fmt.Errorf("workflow kind is registered more than once: %s", kind)
		}
		registry.definitions[kind] = definition
	}
	if len(registry.definitions) == 0 {
		return nil, fmt.Errorf("at least one workflow must be registered")
	}
	if registry.defaultKind == "" {
		return nil, fmt.Errorf("default workflow kind is required")
	}
	if _, ok := registry.definitions[registry.defaultKind]; !ok {
		return nil, fmt.Errorf("default workflow is not registered: %s", registry.defaultKind)
	}
	return registry, nil
}

func (r *WorkflowRegistry) Resolve(kind string) (WorkflowDefinition, error) {
	if r == nil {
		return nil, fmt.Errorf("workflow registry is not configured")
	}
	requested := strings.TrimSpace(kind)
	if requested == "" {
		requested = r.defaultKind
	}
	definition := r.definitions[requested]
	if definition == nil {
		return nil, fmt.Errorf("workflow is not registered: %s", requested)
	}
	return definition, nil
}

func (r *WorkflowRegistry) DefaultKind() string {
	if r == nil {
		return ""
	}
	return r.defaultKind
}

func (r *WorkflowRegistry) Kinds() []string {
	if r == nil {
		return nil
	}
	kinds := make([]string, 0, len(r.definitions))
	for kind := range r.definitions {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}

// RuntimeNodeInput 是 Runner 与类型擦除后的 Workflow 之间的通用执行参数。
// Definition[T] 会在包内把 Data 还原为 *T 后调用强类型节点。
type RuntimeNodeInput struct {
	Run      core.RunContext
	Messages []core.Message
	Runtime  *core.RunState
	Data     any
	Events   chan<- core.NodeEvent
}

// WorkflowDiagnostic 是 Workflow 在节点边界产生的异常诊断。
// 正常节点无需返回诊断，Runner 只负责统一写入审计和控制台。
type WorkflowDiagnostic struct {
	Type     string
	Stage    core.NodeType
	Status   string
	Message  string
	Metadata map[string]any
	Console  string
}

// StateHooks 把业务状态生命周期留在具体 Workflow 内，避免通用 Runner
// 读取 evidence、model output 或其他业务字段。
type StateHooks[T any] struct {
	Restore                 func(context.Context, core.SessionStore, core.RunContext, *T) ([]core.TraceEvent, error)
	AfterNode               func(context.Context, core.NodeType, *core.RunState, *T) ([]WorkflowDiagnostic, error)
	HandleProtocolViolation func(*T, string)
	Persist                 func(context.Context, core.SessionStore, core.RunContext, *T) ([]core.TraceEvent, error)
}

// Definition 将强类型业务 State、Graph、节点组和能力声明绑定为一个 Workflow。
type Definition[T any] struct {
	ID            string
	WorkflowGraph Graph[T]
	NodeGroup     *NodeRegistry[T]
	Codec         StateCodec[T]
	SkillIDs      []string
	ToolBindings  []ToolBinding
	PromptManager core.PromptManager
	Hooks         StateHooks[T]
}

func (d Definition[T]) Kind() string { return d.ID }

func (d Definition[T]) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("workflow kind is required")
	}
	if d.NodeGroup == nil {
		return fmt.Errorf("workflow %s node registry is not configured", d.ID)
	}
	if d.Codec == nil {
		return fmt.Errorf("workflow %s state codec is not configured", d.ID)
	}
	seenSkills := make(map[string]struct{}, len(d.SkillIDs))
	for _, skillID := range d.SkillIDs {
		skillID = strings.TrimSpace(skillID)
		if skillID == "" {
			return fmt.Errorf("workflow %s has an empty skill binding", d.ID)
		}
		if _, exists := seenSkills[skillID]; exists {
			return fmt.Errorf("workflow %s skill is bound more than once: %s", d.ID, skillID)
		}
		seenSkills[skillID] = struct{}{}
	}
	seenSources := make(map[string]struct{}, len(d.ToolBindings))
	for _, binding := range d.ToolBindings {
		source := strings.TrimSpace(binding.Source)
		if source == "" {
			return fmt.Errorf("workflow %s has a tool binding without source", d.ID)
		}
		if _, exists := seenSources[source]; exists {
			return fmt.Errorf("workflow %s tool source is bound more than once: %s", d.ID, source)
		}
		seenSources[source] = struct{}{}
		if binding.All && len(binding.Tools) > 0 {
			return fmt.Errorf("workflow %s tool source %s cannot bind all tools and a name list", d.ID, source)
		}
	}
	if err := ValidateGraphDefinition(d.WorkflowGraph); err != nil {
		return err
	}
	registered := make(map[string]struct{}, len(d.NodeGroup.Names()))
	for _, name := range d.NodeGroup.Names() {
		registered[name] = struct{}{}
	}
	for _, node := range d.WorkflowGraph.NodeTypes() {
		if _, ok := registered[node.String()]; !ok {
			return fmt.Errorf("workflow %s graph node is not registered: %s", d.ID, node)
		}
	}
	return nil
}

func (d Definition[T]) NodeTypes() []core.NodeType { return d.WorkflowGraph.NodeTypes() }

func (d Definition[T]) Entry() (core.NodeType, bool) { return d.WorkflowGraph.Entry() }

func (d Definition[T]) Contains(node core.NodeType) bool { return d.WorkflowGraph.contains(node) }

func (d Definition[T]) IsEnd(node core.NodeType) bool { return d.WorkflowGraph.IsEnd(node) }

func (d Definition[T]) BudgetTarget() (core.NodeType, bool) { return d.WorkflowGraph.BudgetTarget() }

func (d Definition[T]) FallbackTarget() (core.NodeType, bool) {
	return d.WorkflowGraph.fallback, d.WorkflowGraph.fallback != ""
}

func (d Definition[T]) ApprovalTarget() (core.NodeType, bool) {
	return d.WorkflowGraph.approval, d.WorkflowGraph.approval != ""
}

func (d Definition[T]) Skills() []string { return append([]string(nil), d.SkillIDs...) }

func (d Definition[T]) Tools() []ToolBinding {
	result := make([]ToolBinding, len(d.ToolBindings))
	for i, binding := range d.ToolBindings {
		result[i] = ToolBinding{Source: binding.Source, Tools: append([]string(nil), binding.Tools...), All: binding.All}
	}
	return result
}

func (d Definition[T]) Prompts() core.PromptManager { return d.PromptManager }

func (d Definition[T]) NewStatePayload() (json.RawMessage, error) {
	if d.Codec == nil {
		return nil, fmt.Errorf("workflow %s state codec is not configured", d.ID)
	}
	return d.Codec.Encode(d.Codec.New())
}

func (d Definition[T]) NewStateValue() (any, error) {
	if d.Codec == nil {
		return nil, fmt.Errorf("workflow %s state codec is not configured", d.ID)
	}
	state := d.Codec.New()
	return &state, nil
}

func (d Definition[T]) DecodeStatePayload(payload json.RawMessage) (any, error) {
	if d.Codec == nil {
		return nil, fmt.Errorf("workflow %s state codec is not configured", d.ID)
	}
	state, err := d.Codec.Decode(payload)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (d Definition[T]) EncodeStateValue(value any) (json.RawMessage, error) {
	if d.Codec == nil {
		return nil, fmt.Errorf("workflow %s state codec is not configured", d.ID)
	}
	state, ok := value.(*T)
	if !ok || state == nil {
		return nil, fmt.Errorf("workflow %s state value has type %T", d.ID, value)
	}
	return d.Codec.Encode(*state)
}

func (d Definition[T]) statePointer(value any) (*T, error) {
	state, ok := value.(*T)
	if !ok || state == nil {
		return nil, fmt.Errorf("workflow %s state value has type %T", d.ID, value)
	}
	return state, nil
}

func (d Definition[T]) ExecuteNode(ctx context.Context, nodeType core.NodeType, input RuntimeNodeInput) (core.NodeOutput, error) {
	state, err := d.statePointer(input.Data)
	if err != nil {
		return core.NodeOutput{}, err
	}
	node, err := d.NodeGroup.Resolve(nodeType)
	if err != nil {
		return core.NodeOutput{}, err
	}
	return node.Execute(ctx, NodeInput[T]{Run: input.Run, Messages: input.Messages, Runtime: input.Runtime, State: state, Events: input.Events})
}

func (d Definition[T]) ProgressMessage(nodeType core.NodeType, input RuntimeNodeInput) (string, error) {
	state, err := d.statePointer(input.Data)
	if err != nil {
		return "", err
	}
	node, err := d.NodeGroup.Resolve(nodeType)
	if err != nil {
		return "", err
	}
	provider, ok := node.(NodeProgressProvider[T])
	if !ok {
		return "", nil
	}
	return provider.ProgressMessage(NodeInput[T]{Run: input.Run, Messages: input.Messages, Runtime: input.Runtime, State: state, Events: input.Events}), nil
}

func (d Definition[T]) ResolveRoute(ctx context.Context, run core.RunContext, current core.NodeType, output core.NodeOutput, runtime *core.RunState) (core.RouteDecision, error) {
	if runtime == nil {
		return core.RouteDecision{}, fmt.Errorf("workflow %s route runtime is nil", d.ID)
	}
	state, err := d.statePointer(runtime.WorkflowState.Data)
	if err != nil {
		return core.RouteDecision{}, err
	}
	return (ConfiguredRouteResolver[T]{Graph: d.WorkflowGraph}).Resolve(ctx, RouteInput[T]{Run: run, Current: current, Output: output, Runtime: runtime, State: state})
}

func (d Definition[T]) Restore(ctx context.Context, store core.SessionStore, run core.RunContext, value any) ([]core.TraceEvent, error) {
	state, err := d.statePointer(value)
	if err != nil {
		return nil, err
	}
	if d.Hooks.Restore == nil {
		return nil, nil
	}
	return d.Hooks.Restore(ctx, store, run, state)
}

func (d Definition[T]) AfterNode(ctx context.Context, node core.NodeType, runtime *core.RunState, value any) ([]WorkflowDiagnostic, error) {
	state, err := d.statePointer(value)
	if err != nil {
		return nil, err
	}
	if d.Hooks.AfterNode == nil {
		return nil, nil
	}
	return d.Hooks.AfterNode(ctx, node, runtime, state)
}

func (d Definition[T]) HandleProtocolViolation(value any, reason string) error {
	state, err := d.statePointer(value)
	if err != nil {
		return err
	}
	if d.Hooks.HandleProtocolViolation != nil {
		d.Hooks.HandleProtocolViolation(state, reason)
	}
	return nil
}

func (d Definition[T]) Persist(ctx context.Context, store core.SessionStore, run core.RunContext, value any) ([]core.TraceEvent, error) {
	state, err := d.statePointer(value)
	if err != nil {
		return nil, err
	}
	if d.Hooks.Persist == nil {
		return nil, nil
	}
	return d.Hooks.Persist(ctx, store, run, state)
}

var _ WorkflowDefinition = Definition[struct{}]{}
