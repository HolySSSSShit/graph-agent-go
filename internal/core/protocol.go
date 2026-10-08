package core

import (
	"encoding/json"
	"sort"
)

// IntentResponse 是意图节点返回给模型协议层的结构定义。
type IntentResponse struct {
	Domain      string      `json:"domain"`
	Goal        string      `json:"goal"`
	Entities    any         `json:"entities"`
	ScopeAction ScopeAction `json:"scope_action" oneof:"none,inherit,override"`
	Risk        string      `json:"risk,omitempty"`
}

// ReasoningResponse 是推理节点的统一输出协议结构。
type ReasoningResponse struct {
	Kind          string         `json:"kind" oneof:"tool_plan,final,clarify"`
	Mode          ReasoningMode  `json:"mode" oneof:"single_step,reasoning"`
	Summary       string         `json:"summary"`
	ToolPlan      *ToolPlan      `json:"tool_plan,omitempty"`
	Final         *FinalResponse `json:"final,omitempty"`
	Clarification string         `json:"clarification,omitempty"`
}

// DecisionResponse 是兼容决策节点的统一输出协议结构。
type DecisionResponse struct {
	Kind          string         `json:"kind" oneof:"tool_plan,final,clarify"`
	ToolPlan      *ToolPlan      `json:"tool_plan,omitempty"`
	Final         *FinalResponse `json:"final,omitempty"`
	Clarification string         `json:"clarification,omitempty"`
}

// ContextSummaryResponse 是上下文压缩节点的输出协议结构。
type ContextSummaryResponse struct {
	Summary string `json:"summary"`
}

// ToolPlan 和 ToolPlanStep 使用与模型协议一致的 JSON 字段名，供执行器和提示词共享。
// 内部执行仍使用 Go 字段名，避免在业务代码中散落 JSON 字符串。

// UnmarshalJSON 兼容旧模型返回的“步骤编号对象”，并统一归一化为 steps 数组。
// 新协议仍只在提示词中声明 steps 数组，兼容逻辑只属于协议适配层。
func (p *ToolPlan) UnmarshalJSON(data []byte) error {
	type plain ToolPlan
	var value plain
	if err := json.Unmarshal(data, &value); err == nil && len(value.Steps) > 0 {
		*p = ToolPlan(value)
		return nil
	}
	var keyed map[string]struct {
		ToolName   string         `json:"tool_name"`
		Arguments  map[string]any `json:"arguments"`
		Parameters map[string]any `json:"parameters"`
		DependsOn  []string       `json:"depends_on"`
	}
	if err := json.Unmarshal(data, &keyed); err != nil {
		return err
	}
	steps := make([]ToolPlanStep, 0, len(keyed))
	for id, item := range keyed {
		arguments := item.Arguments
		if arguments == nil {
			arguments = item.Parameters
		}
		steps = append(steps, ToolPlanStep{ID: id, ToolName: item.ToolName, Arguments: arguments, DependsOn: item.DependsOn})
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].ID < steps[j].ID })
	p.Steps = steps
	return nil
}
