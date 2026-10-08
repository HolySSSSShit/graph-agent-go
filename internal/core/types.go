package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
)

type Message struct {
	Role            string          `json:"role"`
	Content         string          `json:"content"`
	Images          []ImageInput    `json:"images,omitempty"`
	ToolName        string          `json:"tool_name,omitempty"`
	ToolArgs        map[string]any  `json:"tool_args,omitempty"`
	Visualizations  []Visualization `json:"visualizations,omitempty"`
	ContextExcluded bool            `json:"context_excluded,omitempty"`
}
type ImageInput struct {
	URL      string `json:"url"`
	MIMEType string `json:"mime_type,omitempty"`
}
type Tool struct {
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	InputSchema      map[string]any `json:"inputSchema"`
	Internal         bool           `json:"internal,omitempty"`
	ApprovalRequired bool           `json:"approval_required,omitempty"`
	// 引导工具在每轮推理中保留完整 Schema，模型无需猜测参数即可开始加载流程。
	Bootstrap bool `json:"bootstrap,omitempty"`
}
type ToolResult struct {
	ToolName       string
	Data           any
	Status         string
	Retryable      bool
	Error          string
	LoadedSchemas  []Tool
	HideFromPrompt bool
	Source         *ResultSource
}
type ResultSource struct{ ToolName, Reference, Path string }
type PresentationLink struct {
	Title string `json:"title"`
	Href  string `json:"href"`
}
type Identity struct {
	TenantID, UserID, Role string
	// Metadata 是身份提供方筛选后的稳定上下文，不包含凭证和原始响应。
	Metadata map[string]any
}
type ToolScope struct{ TenantID, UserID, SessionID, CredentialRef string }

type ScopeAction string

const (
	ScopeActionNone     ScopeAction = "none"
	ScopeActionInherit  ScopeAction = "inherit"
	ScopeActionOverride ScopeAction = "override"
)

type IntentStatus string

const (
	IntentStatusRecognized IntentStatus = "recognized"
	IntentStatusUnknown    IntentStatus = "unknown"
	IntentStatusOutOfScope IntentStatus = "out_of_scope"
)

type Intent struct {
	Domain, Goal string
	Entities     any
	Risk         string
	ScopeAction  ScopeAction
	Status       IntentStatus
}
type RunContext struct {
	RunID, TraceID, SessionID string
	Identity                  Identity
	// Credential 是请求级凭证，仅供需要调用外部存储的 Workflow 使用，不进入业务 State。
	Credential      string
	Step, ToolCalls int
	// AllowedNodes 是当前 Workflow 在本次节点调用中暴露的图节点元数据。
	// 它由编排器从 Graph 生成，不属于可持久化的 RunState。
	AllowedNodes []NodeType
}
type TaskProfile struct{ Kind, Complexity string }
type RouteInfo struct{ Provider, Model, Reason string }
type Usage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
	FinishReason    string
	ThinkingEnabled *bool
	ThinkingBudget  int
}
type ModelRequest struct {
	Model          string
	Messages       []Message
	Tools          []Tool
	Temperature    *float32
	TopP           *float32
	MaxTokens      int
	ResponseFormat *ResponseFormat
}
type ResponseFormatType string

const (
	ResponseFormatJSON ResponseFormatType = "json_object"
)

type ResponseFormat struct {
	Type       ResponseFormatType `json:"type"`
	JSONSchema map[string]any     `json:"json_schema,omitempty"`
}
type ModelEvent struct {
	Type, Text string
	ToolName   string
	ToolArgs   map[string]any
	Usage      *Usage
}
type NodeOutput struct {
	Status          string
	Messages        []Message
	Events          []NodeEvent
	Visualizations  []Visualization
	Clarification   bool
	ContextExcluded bool
	ResponseStage   NodeType
	// PendingAction 是节点向 orchestrator 提交的审批请求，节点不得直接修改审批控制状态。
	PendingAction *Action
	// Approval 是审批节点创建的控制面记录，由 orchestrator 统一写入 RuntimeState。
	Approval *Approval
	// Streamed 表示节点已经通过 NodeInput.Events 发送了增量内容。
	// 运行器据此避免在完成时重复发送一次完整 message.delta。
	Streamed bool
}

// RouteKey 是条件路由决策函数返回的语义结果。
// 它与 NodeType 有意区分：RouteKey 表示选择原因，NodeType 表示执行目标。
type RouteKey string

// RunState 是 checkpoint 的通用外壳。各分组通过匿名嵌入保留便捷的字段访问，
// 但 JSON 持久化时按 workflow/runtime/context/tools 分组，避免形成新的上帝对象。
// 外部 MCP 的动态 JSON 只允许出现在工具结果、工具参数、Schema 或业务 payload 中。
type RunState struct {
	WorkflowState `json:"workflow"`
	RuntimeState  `json:"runtime"`
}

// WorkflowState 是 checkpoint 外壳中的业务 Workflow 标识和不透明 payload。
// payload 的编码和解码必须由 Workflow 自己提供的 StateCodec 完成。
type WorkflowState struct {
	Version         int             `json:"version"`
	WorkflowKind    string          `json:"kind"`
	WorkflowPayload json.RawMessage `json:"payload"`
	// Data 是当前进程中由 Workflow Codec 解码出的强类型业务状态。
	// 它只在运行期间存在，checkpoint 只持久化 WorkflowPayload。
	Data any `json:"-"`
}

// RuntimeState 保存所有 Workflow 共用的运行时信息，不包含具体业务字段。
type RuntimeState struct {
	Session Session      `json:"session"`
	Input   Message      `json:"input"`
	Scope   ToolScope    `json:"scope"`
	Route   RouteState   `json:"route"`
	Metrics RunMetrics   `json:"metrics"`
	Control ControlState `json:"control"`
}

// ControlState 是编排器和通用 route resolver 共同维护的控制面状态。
// 业务节点只能通过 NodeOutput.PendingAction 提交审批请求，不能直接修改这里。
type ControlState struct {
	PendingAction        *Action             `json:"pending_action,omitempty"`
	ApprovedActionHashes map[string]struct{} `json:"approved_action_hashes,omitempty"`
	Approval             *Approval           `json:"approval,omitempty"`
	Failure              string              `json:"failure,omitempty"`
}

// RunMetrics 保存编排层可用于路由准入和循环保护的运行指标。
type RunMetrics struct {
	Steps              int
	MaxSteps           int
	ToolCalls          int
	NodeVisits         map[NodeType]int
	NodeDurationsMS    map[NodeType]int64
	ElapsedMS          int64
	LastNodeDurationMS int64
	BudgetExhausted    bool
	LastNode           NodeType
}

// ExecutedTool 保存执行器的原始结果及其计划步骤，供状态检查点和证据归并使用。
type ExecutedTool struct {
	Step          ToolPlanStep
	Result        ToolResult
	ToolAttempts  []ToolAttempt
	RetryAttempts []RetryAttempt
}

// PendingInspection 表示一个已成功执行但当前只向模型展示预览的分组结果。
type PendingInspection struct {
	HandleID string
	ToolName string
	JSONPath string
}

// RouteState 保存当前运行的路由历史和最后一次路由决策。
type RouteState struct {
	CurrentNode NodeType
	History     []RouteDecision
	Last        *RouteDecision
}

// RouteDecision 是一次可审计的状态转移决定。RequestedNext 是路由解析得到的候选目标，
// SelectedNext 是 Resolver 校验准入条件后实际选择的目标节点。
type RouteDecision struct {
	SourceNode       NodeType
	RequestedNext    NodeType
	SelectedNext     NodeType
	TransitionID     string
	AdmissionPassed  bool
	AdmissionReason  string
	Reason           string
	RequiresApproval bool
	At               time.Time
}

// Checkpoint 是运行状态的可回放快照。TraceEvents 保存该时刻之前的完整审计事件，
// 其中包含模型请求、模型原始响应和工具入参；Credential 只供受保护的恢复/审计边界使用。
type Checkpoint struct {
	ID          string
	RunID       string
	TraceID     string
	SessionID   string
	Identity    Identity
	Credential  string
	Node        NodeType
	Status      string
	Sequence    int
	State       RunState
	Route       RouteDecision
	TraceEvents []TraceEvent
	CreatedAt   time.Time
}

const (
	CheckpointStatusRunning   = "running"
	CheckpointStatusSuspended = "suspended"
	CheckpointStatusCompleted = "completed"
	CheckpointStatusFailed    = "failed"
	CheckpointStatusResumed   = "resumed"
	CheckpointStatusRejected  = "rejected"
)

type CheckpointStore interface {
	SaveCheckpoint(context.Context, Checkpoint) error
	ListCheckpoints(context.Context, Identity, string) ([]Checkpoint, error)
	LatestCheckpoint(context.Context, Identity, string) (Checkpoint, error)
	LatestSuspendedCheckpoint(context.Context, Identity, string) (Checkpoint, error)
	UpdateCheckpointStatus(context.Context, Identity, string, string) error
}

// RunLease 描述一次运行在某个 worker 上的短期执行租约。
// 租约只保护运行所有权，不应跨越模型或工具调用事务。
type RunLease struct {
	RunID      string
	WorkerID   string
	Version    int64
	LeaseUntil time.Time
}

type RunLeaseRequest struct {
	RunID         string
	TenantID      string
	UserID        string
	SessionID     string
	WorkerID      string
	LeaseDuration time.Duration
	MaxTenantRuns int
}

// RunLeaseStore 是跨进程运行协调的持久化边界。
// 实现必须保证获取、续租和释放操作可重试且不会重复占用或释放并发额度。
type RunLeaseStore interface {
	AcquireRunLease(context.Context, RunLeaseRequest) (RunLease, error)
	RenewRunLease(context.Context, RunLease, time.Duration) (RunLease, error)
	ReleaseRunLease(context.Context, RunLease, string) error
}

// NodeEvent 是节点产生的面向客户端的受控状态事件，不承载原始推理内容。
type NodeEvent struct {
	Type     NodeEventType
	Status   string
	Message  string
	Text     string
	ToolName string
	// Internal 事件仅记录到 trace，不发送给外部客户端。
	Internal bool
}

const (
	NodeStatusContinue = "continue"
	NodeStatusWaitUser = "wait_user"
	NodeStatusComplete = "complete"
)

// ToolResultStatus 表示单个工具步骤的执行结果。步骤失败不会自动等同于整次运行失败。
const (
	ToolStatusSuccess          = "success"
	ToolStatusBusinessError    = "business_error"
	ToolStatusFailed           = "failed"
	ToolStatusPermissionDenied = "permission_denied"
	ToolStatusSkipped          = "skipped"
)

const (
	DefaultAgentMaxSteps        = 32
	DefaultMaxToolCalls         = 5
	DefaultContextMaxTokens     = 81920
	DefaultContextReserveTokens = 10240
	DefaultLocale               = "zh-CN"
	DefaultTimezone             = "Asia/Shanghai"
	// 后续推理只使用最新一组证据；旧证据既不进入模型上下文，也不是持久业务记录。
	DefaultEvidenceFactRuns = 1
)

type NodeEventType string

const (
	// NodeEventProgress 是节点执行期间面向用户的阶段进度，不包含内部执行细节。
	NodeEventProgress        NodeEventType = "progress"
	NodeEventReasoning       NodeEventType = "reasoning"
	NodeEventToolStarted     NodeEventType = "tool.started"
	NodeEventToolResult      NodeEventType = "tool.result"
	NodeEventMessageDelta    NodeEventType = "message.delta"
	NodeEventStatusStreaming               = "streaming"
)

// TraceEvent 是一次 Agent 运行中的统一可审计事件。
// Message 只允许保存用户原始输入、最终输出或脱敏后的步骤摘要，不保存原始思维链和凭证。
type TraceEvent struct {
	TraceID         string         `json:"trace_id"`
	RunID           string         `json:"run_id"`
	SessionID       string         `json:"session_id"`
	TenantID        string         `json:"tenant_id,omitempty"`
	UserID          string         `json:"user_id,omitempty"`
	Sequence        int            `json:"sequence"`
	Type            string         `json:"type"`
	Stage           NodeType       `json:"stage,omitempty"`
	Status          string         `json:"status,omitempty"`
	Message         string         `json:"message,omitempty"`
	Prompt          []Message      `json:"prompt,omitempty"`
	Output          string         `json:"output,omitempty"`
	ToolName        string         `json:"tool_name,omitempty"`
	ToolArguments   map[string]any `json:"tool_arguments,omitempty"`
	StepID          string         `json:"step_id,omitempty"`
	ResultBytes     int64          `json:"result_bytes,omitempty"`
	ResultHandleID  string         `json:"result_handle_id,omitempty"`
	EvidenceCount   int            `json:"evidence_count,omitempty"`
	ModelName       string         `json:"model_name,omitempty"`
	Attempt         int            `json:"attempt,omitempty"`
	InputTokens     int            `json:"input_tokens,omitempty"`
	OutputTokens    int            `json:"output_tokens,omitempty"`
	ReasoningTokens int            `json:"reasoning_tokens,omitempty"`
	FinishReason    string         `json:"finish_reason,omitempty"`
	ThinkingEnabled *bool          `json:"thinking_enabled,omitempty"`
	ThinkingBudget  int            `json:"thinking_budget,omitempty"`
	DurationMS      int64          `json:"duration_ms,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	At              time.Time      `json:"at"`
}

// NodeEdge 描述节点图中的一条有向边。Condition 只表达通用运行准入条件，
// 不承载业务字段或业务判断。
type NodeEdge struct {
	From             NodeType
	To               NodeType
	Condition        string
	MaxVisits        int
	Reentrant        bool
	Requires         []NodeType
	ApprovalRequired bool
}

// NodeType 是节点图和运行时协议使用的稳定节点类型标识。
type NodeType string

func (n NodeType) String() string { return string(n) }

const (
	NodeTypeSkillContext         NodeType = "skill_context"
	NodeTypeImageUnderstanding   NodeType = "image_understanding"
	NodeTypeToolCatalog          NodeType = "tool_catalog"
	NodeTypeContextCompile       NodeType = "context_compile"
	NodeTypeIntent               NodeType = "intent"
	NodeTypeReason               NodeType = "reason"
	NodeTypeApproval             NodeType = "approval"
	NodeTypeClarify              NodeType = "clarify"
	NodeTypeToolExecute          NodeType = "tool_execute"
	NodeTypeResultReduce         NodeType = "result_reduce"
	NodeTypeReview               NodeType = "review"
	NodeTypeOutputSkill          NodeType = "output_skill"
	NodeTypeFinalize             NodeType = "finalize"
	NodeTypePresentationValidate NodeType = "presentation_validate"
)

type ModelRole string

const (
	ModelRoleDefault          ModelRole = "default"
	ModelRoleIntent           ModelRole = "intent"
	ModelRoleSkill            ModelRole = "skill"
	ModelRoleReason           ModelRole = "reason"
	ModelRoleReasonEscalation ModelRole = "reason_escalation"
	ModelRoleReview           ModelRole = "review"
	ModelRoleFinalize         ModelRole = "finalize"
	ModelRoleCompact          ModelRole = "compact"
	ModelRoleImage            ModelRole = "image"
)

const (
	ModelOutputKindToolPlan               = "tool_plan"
	ModelOutputKindFinal                  = "final"
	ModelOutputKindClarify                = "clarify"
	ReasoningModeSingleStep ReasoningMode = "single_step"
	ReasoningModeReasoning  ReasoningMode = "reasoning"
)

type PromptTemplate struct {
	ID       string
	Version  string
	Sections []PromptSection
	Protocol ResponseProtocol
}

const (
	PromptSystem            = "system"
	PromptIntent            = "intent"
	PromptSkillSelection    = "skill_selection"
	PromptReasoning         = "reasoning"
	PromptExecutionRecovery = "execution_recovery"
	PromptFinalResponse     = "final_response"
	PromptSessionCompact    = "session_compact"
	PromptResultReview      = "result_review"
)

// PromptSection 是一个可单独版本化和替换的系统提示词片段。
type PromptSection struct {
	Name    string
	Purpose string
	Content string
}

// ResponseProtocol 定义模型内部返回的结构化语义，不依赖自然语言约定。
type ResponseProtocol struct {
	Name    string
	Version string
	Format  string
	Fields  []ProtocolField
	Rules   []string
	// Schema 可选地指向实际用于编解码的 Go 结构体样例。
	// PromptTemplate.Render 会从该结构体的 json 标签生成字段形状，避免协议文本与代码定义漂移。
	Schema any
}

type ProtocolField struct {
	Name        string
	Type        string
	Required    bool
	Description string
	OneOf       []string
}

// Render 只负责将结构化协议转换为模型可读的系统指令。
func (p PromptTemplate) Render() string {
	parts := make([]string, 0, len(p.Sections)+1)
	for _, section := range p.Sections {
		if section.Content == "" {
			continue
		}
		parts = append(parts, "【"+section.Name+"】\n"+section.Content)
	}
	if p.Protocol.Name != "" {
		protocolFields := p.Protocol.Fields
		if p.Protocol.Schema != nil {
			protocolFields = reflectedProtocolFields(reflect.TypeOf(p.Protocol.Schema), p.Protocol.Fields)
		}
		fields := make([]string, 0, len(protocolFields))
		for _, field := range protocolFields {
			required := "可选"
			if field.Required {
				required = "必填"
			}
			description := field.Description
			if len(field.OneOf) > 0 {
				description += "；OneOf（只能从以下值中选择一个）=" + strings.Join(field.OneOf, "、")
			}
			fields = append(fields, field.Name+"("+field.Type+"，"+required+")："+description)
		}
		protocol := "【输出协议】\n名称：" + p.Protocol.Name + "\n版本：" + p.Protocol.Version + "\n格式：" + p.Protocol.Format
		if len(fields) > 0 {
			protocol += "\n字段：\n" + strings.Join(fields, "\n")
		}
		if len(p.Protocol.Rules) > 0 {
			protocol += "\n规则：\n" + strings.Join(p.Protocol.Rules, "\n")
		}
		if p.Protocol.Schema != nil {
			protocol += "\n结构示例：\n```json\n" + protocolShape(reflect.TypeOf(p.Protocol.Schema)) + "\n```"
		}
		parts = append(parts, protocol)
	}
	return strings.Join(parts, "\n\n")
}

func reflectedProtocolFields(schema reflect.Type, declared []ProtocolField) []ProtocolField {
	for schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	if schema.Kind() != reflect.Struct {
		return declared
	}
	descriptions := make(map[string]ProtocolField, len(declared))
	for _, field := range declared {
		descriptions[field.Name] = field
	}
	fields := make([]ProtocolField, 0, schema.NumField())
	for field := range schema.Fields() {
		if field.PkgPath != "" {
			continue
		}
		name, options := protocolJSONName(field)
		if name == "-" {
			continue
		}
		declaredField, hasDescription := descriptions[name]
		if !hasDescription {
			declaredField = ProtocolField{Name: name, Description: "协议字段"}
		}
		declaredField.Name = name
		declaredField.Type = protocolTypeName(field.Type)
		declaredField.Required = !options["omitempty"]
		if values := protocolOneOfValues(field.Tag.Get("oneof")); len(values) > 0 {
			declaredField.OneOf = values
		}
		fields = append(fields, declaredField)
	}
	return fields
}

// protocolOneOfValues 从协议结构体标签读取固定枚举，避免在 Prompt 文本中重复维护值域。
func protocolOneOfValues(tag string) []string {
	parts := strings.Split(tag, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func protocolJSONName(field reflect.StructField) (string, map[string]bool) {
	parts := strings.Split(field.Tag.Get("json"), ",")
	name := parts[0]
	if name == "" {
		name = field.Name
	}
	options := make(map[string]bool, len(parts)-1)
	for _, option := range parts[1:] {
		options[option] = true
	}
	return name, options
}

func protocolTypeName(value reflect.Type) string {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct, reflect.Interface:
		return "object"
	default:
		return value.Kind().String()
	}
}

func protocolShape(value reflect.Type) string {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		parts := make([]string, 0, value.NumField())
		for field := range value.Fields() {
			if field.PkgPath != "" {
				continue
			}
			name, _ := protocolJSONName(field)
			if name == "-" {
				continue
			}
			parts = append(parts, "\""+name+"\": "+protocolShape(field.Type))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case reflect.Slice, reflect.Array:
		return "[" + protocolShape(value.Elem()) + "]"
	case reflect.Map:
		return "{...}"
	case reflect.String:
		return "\"string\""
	case reflect.Bool:
		return "true/false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	default:
		return "null"
	}
}

// AgentContext 按来源保存模型可见的上下文，由 ContextManager 统一编译。
type AgentContext struct {
	System       SystemContext
	Skills       SkillContext
	Reasoning    ReasoningContext
	Runtime      RuntimeContext
	Conversation ConversationContext
	Tools        ToolContext
	Memory       MemoryContext
	Business     BusinessContext
	Execution    ExecutionContext
	Budget       ContextBudget
}

// ReasoningContext 记录可恢复的推理决策与执行结果，不保存模型原始思维链。
// MaxCycles 是推理节点自己的循环上限，与 Agent 节点步数上限相互独立。
type ReasoningContext struct {
	Mode  ReasoningMode
	Cycle int
	// EscalationUsed 将昂贵的恢复模型限制为每次运行最多尝试一次。
	EscalationUsed bool
	// AllowedNodes 保留当前运行允许参与推理路由的节点元数据；实际跳转由代码决定。
	AllowedNodes  []NodeType
	Decisions     []ReasoningDecision
	ExecutionRuns []ReasoningExecution
}

type ReasoningDecision struct {
	Cycle             int
	Mode              ReasoningMode
	Summary           string
	ToolPlan          *ToolPlan
	NeedsMoreEvidence bool
}

type ReasoningExecution struct {
	Cycle    int
	StepID   string
	ToolName string
	Status   string
	Result   ToolResult
	Error    string
}

type SystemContext struct {
	Prompts []PromptTemplate
}

// SkillContext 保存经过渐进式披露后允许进入本轮模型上下文的技能说明。
// 它不包含技能仓库的完整内容，也不能覆盖系统提示词或权限规则。
type SkillContext struct {
	Items      []Skill
	Candidates []SkillCandidate
}

type SkillKind string

const (
	SkillKindAnalysis  SkillKind = "analysis"
	SkillKindKnowledge SkillKind = "knowledge"
	SkillKindWorkflow  SkillKind = "workflow"
	SkillKindOutput    SkillKind = "output"
)

type Skill struct {
	ID   string
	Pack string
	Kind SkillKind
	// Dimension 用于同一业务内的正交技能组合，例如 page/style；未声明时保持通用技能行为。
	Dimension   string
	Version     string
	Description string
	// PossibleQuestions 是技能适用问题的元数据，不是独立技能，也不直接作为指令注入模型上下文。
	PossibleQuestions []string
	Instructions      string
	Priority          int
	// Tools 是该技能运行所需的工具名称。
	Tools []string
	// Keywords 保留在目录兼容结构中，但当前技能路由不使用关键词匹配。
	Keywords []string
}

type SkillQuery struct {
	Run     RunContext
	Intent  *Intent
	Input   Message
	History []Message
	// Page 仅在 Dimension=page 时限制技能适用的前端页面路由。
	Page string
	// Packs 限制技能选择范围。通常由已选业务技能的 pack 传给 output 技能选择。
	Packs []string
	// Tools 只用于技能相关性和可行性判断；技能正文不绑定具体工具。
	Tools       []Tool
	TokenBudget int
	// Dimension 限制技能选择维度；空值表示不按维度过滤。
	Dimension string
}

// SkillCandidate 是提供给技能选择模型的最小候选信息。
// 文件路径、技能正文和其他内部元数据不会进入模型输入。
type SkillCandidate struct {
	ID                string   `json:"id"`
	Description       string   `json:"description"`
	Dimension         string   `json:"dimension,omitempty"`
	PossibleQuestions []string `json:"possible_questions,omitempty"`
	Tools             []string `json:"tools,omitempty"`
}

// SkillScore 是模型对候选技能的相关性判断。
type SkillScore struct {
	ID     string  `json:"id"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason,omitempty"`
}

// SkillSelectionResponse 是技能选择模型的严格响应协议。
type SkillSelectionResponse struct {
	Skills []SkillScore `json:"skills"`
}

type RuntimeContext struct {
	Run      RunContext
	Locale   string
	Timezone string
	Now      time.Time
}

type ConversationContext struct {
	Summary      *Message
	History      []Message
	CurrentInput Message
}

type ToolContext struct {
	Available []Tool
	Results   []ToolObservation
}

type ToolObservation struct {
	ToolName string
	Result   ToolResult
	Message  Message
}

// ToolPlan 由模型表达执行顺序，执行器据此构造可并行的批次。
// 依赖结果不通过字符串模板绑定；需要前序结果时由下一轮推理生成完整参数。
type ToolPlan struct {
	Steps []ToolPlanStep `json:"steps"`
}

type ToolPlanStep struct {
	ID        string         `json:"id"`
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments"`
	DependsOn []string       `json:"depends_on"`
}
type ModelOutput struct {
	Kind          string
	Mode          ReasoningMode
	Summary       string
	Raw           string
	ToolPlan      *ToolPlan
	Final         *FinalResponse
	Clarification string
}

type ReasoningMode string

type FinalResponse struct {
	Answer            string          `json:"answer"`
	EvidenceIDs       []string        `json:"evidence_ids,omitempty"`
	Facts             []Fact          `json:"facts"`
	Assumptions       []string        `json:"assumptions"`
	Recommendations   []string        `json:"recommendations"`
	Confidence        float64         `json:"confidence"`
	NeedsConfirmation bool            `json:"needs_confirmation"`
	Visualizations    []Visualization `json:"visualizations,omitempty"`
}

// Visualization 是前端可直接交给 ECharts 的纯数据配置，不允许包含 JavaScript 函数。
type Visualization struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Title  string         `json:"title,omitempty"`
	Option map[string]any `json:"option"`
}
type Fact struct {
	Text   string `json:"text"`
	Source string `json:"source"`
}

// EvidenceFact 是从授权工具结果字段提取并由机器验证的指标。
// 其标识符和来源路径仅保留在服务端。
type EvidenceFact struct {
	ID                string         `json:"id"`
	MetricID          string         `json:"metric_id"`
	Label             string         `json:"label"`
	Value             any            `json:"value"`
	Unit              string         `json:"unit"`
	Dimensions        map[string]any `json:"dimensions,omitempty"`
	StepID            string         `json:"step_id"`
	SourceTool        string         `json:"source_tool"`
	SourceRef         string         `json:"source_ref"`
	SourcePath        string         `json:"source_path"`
	Chartable         *bool          `json:"chartable,omitempty"`
	Kind              string         `json:"kind,omitempty"`
	PresentationTitle string         `json:"presentation_title,omitempty"`
}

type MemoryContext struct {
	Items []MemoryItem
}

type MemoryItem struct {
	ID      string
	Kind    string
	Content string
}

type BusinessContext struct {
	MetricDefinitions []MetricDefinition
	Knowledge         []KnowledgeItem
	Metadata          map[string]any
}

type MetricDefinition struct {
	Name       string
	Definition string
	Version    string
}

type KnowledgeItem struct {
	ID      string
	Kind    string
	Content string
}

// ExecutionContext 是可恢复、可审计的运行状态，不保存模型原始思维链。
type ExecutionContext struct {
	Intent        *Intent
	ToolAttempts  []ToolAttempt
	RetryAttempts []RetryAttempt
	// PendingAction 只用于构造模型上下文，不是审批控制状态的 owner。
	PendingAction *Action `json:"-"`
}

func ActionFingerprint(action Action) string {
	data, err := json.Marshal(action)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type ToolAttempt struct {
	ToolName string
	Attempt  int
	Status   string
	Error    string
}

type RetryAttempt struct {
	Operation  string
	Attempt    int
	ErrorClass string
}

type ContextBudget struct {
	MaxTokens           int
	ReserveTokens       int
	ToolResultMaxTokens int
}

type ContextWindow struct {
	Messages        []Message
	EstimatedTokens int
}
type Action struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Payload    map[string]any `json:"payload"`
	Reason     string         `json:"reason"`
	ResumeNode NodeType       `json:"resume_node"`
}
type ApprovalRequest struct {
	ID, RunID, SessionID string
	Identity             Identity
	Action               Action
	ArgsHash             string
	Summary              string
}
type Approval struct {
	ID, RunID, SessionID string
	Identity             Identity
	Action               Action
	ArgsHash             string
	Status               string
	Reason               string
	Summary              string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ResolvedAt           *time.Time
}
type ApprovalDecision struct{ Approved bool }
type RetryDecision struct {
	Retry        bool
	DelaySeconds int
}
type Operation struct {
	Name       string
	Idempotent bool
}
type Evaluation struct {
	Score float64
	Notes string
}
type Feedback struct{ RunID, Value, Comment string }
type RunRecord struct {
	RunID    string
	Messages []Message
}
type Session struct {
	ID       string          `json:"id"`
	Identity Identity        `json:"-"`
	Messages []Message       `json:"messages,omitempty"`
	State    string          `json:"state"`
	Compact  *SessionCompact `json:"compact,omitempty"`
}

// SessionCompact 是当前会话中已离开原始消息窗口的滚动事实摘要。
// 覆盖边界按消息顺序计数，原始消息仍由 SessionStore 保留。
type SessionCompact struct {
	Version         int64
	CoveredMessages int
	Content         string
}

var ErrPermissionDenied = errors.New("permission denied")
var ErrRunLeaseLost = errors.New("run lease lost")
var ErrApprovalNotFound = errors.New("approval not found")
var ErrApprovalAlreadyResolved = errors.New("approval already resolved")

type Model interface {
	Generate(context.Context, ModelRequest) (ModelEvent, error)
	Stream(context.Context, ModelRequest) (<-chan ModelEvent, error)
}

// ImageGenerationRequest 是公共生图服务的最小请求，不携带具体 Workflow 状态。
type ImageGenerationRequest struct {
	Prompt         string
	Size           string
	SessionID      string
	ReferenceImage string
}

type ImageGenerator interface {
	GenerateImage(context.Context, ImageGenerationRequest) (string, error)
}
type ModelRouter interface {
	Select(context.Context, string) (Model, RouteInfo, error)
}
type MCPClient interface {
	Initialize(context.Context) error
	ListTools(context.Context) ([]Tool, error)
	CallTool(context.Context, string, map[string]any) (ToolResult, error)
}
type ToolSource interface {
	Name() string
	ListTools(context.Context, ToolScope) ([]Tool, error)
	CallTool(context.Context, ToolScope, string, map[string]any) (ToolResult, error)
}
type ToolRegistry interface {
	Register(ToolSource) error
	RegisterInternal(Tool, InternalToolHandler) error
	LoadSession(context.Context, ToolScope) error
	Get(ToolScope) ([]Tool, bool)
	Resolve(ToolScope, string) (ToolSource, Tool, bool)
	Refresh(context.Context, string, ToolScope) error
	Invalidate(ToolScope)
}

// InternalToolHandler 供 Harness 内部工具使用；这类工具不发起 MCP 业务调用。
type InternalToolHandler func(context.Context, ToolScope, map[string]any) (ToolResult, error)

// InternalToolRegistration 描述共享 Registry 中一个内部工具的定义和处理器。
// 批量注册由 Registry 保证原子性，具体工具定义仍由所属包维护。
type InternalToolRegistration struct {
	Definition Tool
	Handler    InternalToolHandler
}

// TraceRecorder 统一记录运行事件，并支持按 trace_id 查询当前进程内的事件。
type TraceRecorder interface {
	Record(context.Context, TraceEvent) error
	List(string) []TraceEvent
}
type PolicyGuard interface {
	AuthorizeAPI(context.Context, Identity, string) error
}
type IdentityProvider interface {
	Resolve(context.Context, any) (Identity, error)
}
type PromptManager interface {
	Get(context.Context, string, string) (PromptTemplate, error)
}
type SkillProvider interface {
	Find(context.Context, SkillQuery) ([]Skill, error)
}
type SkillSelector interface {
	Select(context.Context, SkillQuery, []SkillCandidate) ([]SkillScore, error)
}
type ContextManager interface {
	Build(context.Context, AgentContext) (ContextWindow, error)
	Compress(context.Context, AgentContext) (ContextWindow, error)
}
type ToolResultReducer interface {
	Reduce(context.Context, ToolResult, ContextBudget) (ToolResult, error)
}
type ModelOutputExtractor interface {
	Extract(context.Context, string, ResponseProtocol) (ModelOutput, error)
}
type MarkdownRenderer interface {
	Render(context.Context, FinalResponse) (string, error)
}
type ToolPlanExecutor interface {
	Execute(context.Context, ToolScope, ToolPlan) ([]ToolResult, error)
}
type EvidenceVerifier interface {
	VerifyEvidence(context.Context, ResultSource, string) (any, error)
}
type RetryPolicy interface {
	Next(int, error, Operation) (RetryDecision, error)
}
type ApprovalService interface {
	Request(context.Context, ApprovalRequest) (Approval, error)
	Resolve(context.Context, Identity, string, ApprovalDecision) (Approval, error)
}

// ApprovalRollbacker 将已解析审批恢复为 pending，用于控制面后续持久化失败时的最终一致性补偿。
type ApprovalRollbacker interface {
	RestorePending(context.Context, Identity, string) error
}
type SessionStore interface {
	Get(context.Context, Identity, string) (Session, error)
	List(context.Context, Identity) ([]Session, error)
	Delete(context.Context, Identity, string) error
	AppendMessage(context.Context, Identity, string, Message) error
	UpdateState(context.Context, Identity, string, string) error
	GetCompact(context.Context, Identity, string) (SessionCompact, error)
	CompareAndSwapCompact(context.Context, Identity, string, int64, SessionCompact) (bool, error)
}

// EvidenceFactStore 与会话消息刻意分离。
// 实现只保存按契约编译的事实和来源，不保存原始工具 payload 或模型文本。
type EvidenceFactStore interface {
	SaveEvidenceFacts(context.Context, Identity, string, string, []EvidenceFact) error
	ListEvidenceFacts(context.Context, Identity, string, string) ([]EvidenceFact, error)
}

// EvidenceFactPruner 限制会话保留的历史事实数量。
// 事实是执行辅助信息，不是只增不减的业务账本。
type EvidenceFactPruner interface {
	PruneEvidenceFacts(context.Context, Identity, string, int) (int, error)
}

// LatestEvidenceFactStore 暴露会话最近的结构化证据，后续轮次可复用已验证事实，
// 无需重新传递原始工具 payload。
type LatestEvidenceFactStore interface {
	ListLatestEvidenceFacts(context.Context, Identity, string) ([]EvidenceFact, error)
}

type SessionCompactor interface {
	Compact(context.Context, SessionCompact, []Message) (SessionCompact, error)
}
