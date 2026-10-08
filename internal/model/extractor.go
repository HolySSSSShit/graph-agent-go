package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// JSONOutputExtractor 将模型返回的 JSON 或 fenced JSON 解析为受控执行结果。
type JSONOutputExtractor struct{}

func (JSONOutputExtractor) Extract(_ context.Context, raw string, protocol core.ResponseProtocol) (core.ModelOutput, error) {
	cleaned := trimJSONFence(raw)
	cleaned = repairEscapedFieldBoundaries(cleaned)
	cleaned = escapeJSONStringControlChars(cleaned)
	// 提供商偶尔会在完整 JSON 对象后追加 Markdown 或生成的 JavaScript。
	// 保持协议严格，但先截取第一个完整对象再反序列化，避免尾部文本使有效响应失效。
	cleaned = firstJSONObject(cleaned)
	var wire outputWire
	if err := json.Unmarshal([]byte(cleaned), &wire); err != nil {
		// 呈现模型偶尔会在完整 answer 后追加一个没有值的字符串键。
		// 仅移除这种明确无效的尾部字段，再按严格 JSON 解析；不对正文做
		// 宽泛的截断或重新推断，避免把不完整响应伪装成有效结果。
		repaired := repairDanglingJSONStringField(cleaned)
		if repaired != cleaned && json.Unmarshal([]byte(repaired), &wire) == nil {
			// 已修复为完整 JSON。
		} else if protocol.Name == "agent.final_response" {
			// 最终呈现模型可能已经生成完整 answer，但在后续 facts 字段
			// 生成中断。answer 本身仍可交给 Finalize 做证据绑定和安全校验，
			// 损坏的 facts/evidence_ids 不得被采信。
			if answer, ok := extractJSONStringProperty(cleaned, "answer"); ok {
				wire = outputWire{finalWire: finalWire{Answer: answer}}
			} else {
				return core.ModelOutput{}, fmt.Errorf("decode model output: %w", err)
			}
		} else {
			return core.ModelOutput{}, fmt.Errorf("decode model output: %w", err)
		}
	}
	if wire.Kind == "" && protocol.Name == "agent.final_response" {
		wire.Kind = "final"
	}
	output := core.ModelOutput{Kind: wire.Kind, Raw: raw, Clarification: wire.Clarification}
	output.Mode = core.ReasoningMode(wire.Mode)
	output.Summary = wire.Summary
	if protocol.Name == "agent.reasoning" && output.Kind == "" {
		output.Kind = string(wire.Mode)
	}
	kind := wire.Kind
	if kind == "" {
		kind = output.Kind
	}
	switch kind {
	case string(core.ReasoningModeSingleStep), string(core.ReasoningModeReasoning):
		output.Kind = wire.Kind
		if wire.ToolPlan != nil {
			plan := *wire.ToolPlan
			if err := validateToolPlan(plan); err != nil {
				return core.ModelOutput{}, err
			}
			output.ToolPlan = &plan
		}
		if wire.Final != nil {
			output.Final = &core.FinalResponse{Answer: wire.Final.Answer, EvidenceIDs: wire.Final.EvidenceIDs, Facts: wire.Final.Facts, Assumptions: wire.Final.Assumptions, Recommendations: wire.Final.Recommendations, Confidence: wire.Final.Confidence, NeedsConfirmation: wire.Final.NeedsConfirmation, Visualizations: wire.Final.Visualizations}
		}
	case core.ModelOutputKindToolPlan:
		if wire.ToolPlan == nil {
			return core.ModelOutput{}, errors.New("model output has no tool_plan")
		}
		plan := *wire.ToolPlan
		if err := validateToolPlan(plan); err != nil {
			return core.ModelOutput{}, err
		}
		output.ToolPlan = &plan
	case core.ModelOutputKindFinal:
		final := wire.Final
		if final == nil {
			final = &core.FinalResponse{
				Answer:            wire.finalWire.Answer,
				EvidenceIDs:       wire.finalWire.EvidenceIDs,
				Facts:             wire.finalWire.Facts,
				Assumptions:       wire.finalWire.Assumptions,
				Recommendations:   wire.finalWire.Recommendations,
				Confidence:        wire.finalWire.Confidence,
				NeedsConfirmation: wire.finalWire.NeedsConfirmation,
				Visualizations:    wire.finalWire.Visualizations,
			}
		} else {
			// 模型偶尔会把 final 的部分字段错误地放到顶层。嵌套对象仍是
			// 主要结构，但缺失字段可以从同一响应的顶层补齐，避免解析器
			// 静默丢失 evidence_ids 造成后续误判为证据不足。
			merged := *final
			if len(merged.EvidenceIDs) == 0 {
				merged.EvidenceIDs = wire.finalWire.EvidenceIDs
			}
			if len(merged.Facts) == 0 {
				merged.Facts = wire.finalWire.Facts
			}
			if len(merged.Assumptions) == 0 {
				merged.Assumptions = wire.finalWire.Assumptions
			}
			if len(merged.Recommendations) == 0 {
				merged.Recommendations = wire.finalWire.Recommendations
			}
			if merged.Confidence == 0 {
				merged.Confidence = wire.finalWire.Confidence
			}
			if !merged.NeedsConfirmation {
				merged.NeedsConfirmation = wire.finalWire.NeedsConfirmation
			}
			if len(merged.Visualizations) == 0 {
				merged.Visualizations = wire.finalWire.Visualizations
			}
			final = &merged
		}
		if final.Answer == "" {
			return core.ModelOutput{}, errors.New("model output has no final answer")
		}
		output.Final = &core.FinalResponse{
			Answer:            final.Answer,
			EvidenceIDs:       final.EvidenceIDs,
			Facts:             final.Facts,
			Assumptions:       final.Assumptions,
			Recommendations:   final.Recommendations,
			Confidence:        final.Confidence,
			NeedsConfirmation: final.NeedsConfirmation,
			Visualizations:    final.Visualizations,
		}
	case core.ModelOutputKindClarify:
		if wire.Clarification == "" {
			return core.ModelOutput{}, errors.New("model output has no clarification")
		}
	default:
		return core.ModelOutput{}, fmt.Errorf("unsupported model output kind %q", wire.Kind)
	}
	return output, nil
}

var danglingJSONStringField = regexp.MustCompile(`,\s*"[^"\\]*(?:\\.[^"\\]*)*"\s*}`)
var escapedFieldBoundary = regexp.MustCompile(`\\",\\\"(evidence_ids|facts|assumptions|recommendations|confidence|needs_confirmation|visualizations)\\\":`)

func repairEscapedFieldBoundaries(raw string) string {
	return escapedFieldBoundary.ReplaceAllString(raw, `","$1":`)
}

func escapeJSONStringControlChars(raw string) string {
	var builder strings.Builder
	builder.Grow(len(raw))
	inString := false
	escaped := false
	for _, char := range raw {
		if inString {
			if escaped {
				builder.WriteRune(char)
				escaped = false
				continue
			}
			if char == '\\' {
				builder.WriteRune(char)
				escaped = true
				continue
			}
			if char == '"' {
				builder.WriteRune(char)
				inString = false
				continue
			}
			switch char {
			case '\n':
				builder.WriteString(`\n`)
			case '\r':
				builder.WriteString(`\r`)
			case '\t':
				builder.WriteString(`\t`)
			default:
				builder.WriteRune(char)
			}
			continue
		}
		builder.WriteRune(char)
		if char == '"' {
			inString = true
		}
	}
	return builder.String()
}

func repairDanglingJSONStringField(raw string) string {
	if repaired := danglingJSONStringField.ReplaceAllString(raw, "}"); repaired != raw {
		return repaired
	}
	// 生成中断时，尾部字段甚至可能只有开引号，没有闭引号。
	// 只有在最后一个逗号后的内容确实没有冒号时才移除，避免影响合法字符串值。
	comma := strings.LastIndexByte(raw, ',')
	if comma >= 0 {
		tail := strings.TrimSpace(raw[comma+1:])
		if strings.HasPrefix(tail, "\"") && !strings.Contains(tail, ":") && strings.HasSuffix(strings.TrimSpace(raw), "}") {
			return raw[:comma] + "}"
		}
	}
	return raw
}

func extractJSONStringProperty(raw, property string) (string, bool) {
	needle := `"` + property + `"`
	start := strings.Index(raw, needle)
	if start < 0 {
		return "", false
	}
	colon := strings.IndexByte(raw[start+len(needle):], ':')
	if colon < 0 {
		return "", false
	}
	valueStart := start + len(needle) + colon + 1
	for valueStart < len(raw) && (raw[valueStart] == ' ' || raw[valueStart] == '\t' || raw[valueStart] == '\r' || raw[valueStart] == '\n') {
		valueStart++
	}
	if valueStart >= len(raw) || raw[valueStart] != '"' {
		return "", false
	}
	escaped := false
	for index := valueStart + 1; index < len(raw); index++ {
		if escaped {
			escaped = false
			continue
		}
		if raw[index] == '\\' {
			escaped = true
			continue
		}
		if raw[index] == '"' {
			var value string
			if err := json.Unmarshal([]byte(raw[valueStart:index+1]), &value); err != nil {
				return "", false
			}
			return value, true
		}
	}
	return "", false
}

type outputWire struct {
	core.ReasoningResponse
	finalWire
}

type finalWire struct {
	Answer            string               `json:"answer"`
	EvidenceIDs       []string             `json:"evidence_ids,omitempty"`
	Facts             []core.Fact          `json:"facts"`
	Assumptions       []string             `json:"assumptions"`
	Recommendations   []string             `json:"recommendations"`
	Confidence        float64              `json:"confidence"`
	NeedsConfirmation bool                 `json:"needs_confirmation"`
	Visualizations    []core.Visualization `json:"visualizations,omitempty"`
}

func validateToolPlan(plan core.ToolPlan) error {
	if len(plan.Steps) == 0 {
		return errors.New("tool plan has no steps")
	}
	steps := make(map[string]core.ToolPlanStep, len(plan.Steps))
	for _, step := range plan.Steps {
		if step.ID == "" || step.ToolName == "" {
			return errors.New("tool plan step requires id and tool_name")
		}
		if _, exists := steps[step.ID]; exists {
			return fmt.Errorf("tool plan has duplicate step %q", step.ID)
		}
		steps[step.ID] = step
	}
	for _, step := range plan.Steps {
		for _, dependency := range step.DependsOn {
			if dependency == step.ID {
				return fmt.Errorf("tool plan step %q depends on itself", step.ID)
			}
			if _, exists := steps[dependency]; !exists {
				return fmt.Errorf("tool plan step %q depends on unknown step %q", step.ID, dependency)
			}
		}
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("tool plan has dependency cycle at %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range steps[id].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, step := range plan.Steps {
		if err := visit(step.ID); err != nil {
			return err
		}
	}
	return nil
}

func trimJSONFence(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "```json") && strings.HasSuffix(value, "```") {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "```json"), "```"))
	}
	return value
}

func firstJSONObject(raw string) string {
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return raw
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(raw); index++ {
		char := raw[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : index+1]
			}
		}
	}
	return raw
}
