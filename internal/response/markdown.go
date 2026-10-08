package response

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

// MarkdownRenderer 只渲染面向用户的最终正文，不暴露内部结构化字段和工具来源。
type MarkdownRenderer struct{}

func (MarkdownRenderer) Render(_ context.Context, response core.FinalResponse) (string, error) {
	answer := strings.TrimSpace(response.Answer)
	if answer == "" {
		return "", errors.New("final answer is empty")
	}
	answer = normalizeMarkdownEscapes(answer)
	answer = normalizeMarkdownStructure(answer)
	if containsInternalReference(answer) {
		return "", errors.New("final answer contains internal reference")
	}
	if response.Confidence < 0 || response.Confidence > 1 {
		return "", fmt.Errorf("invalid final response confidence: %f", response.Confidence)
	}
	// 完整报告会在叙述中融入证据。仅对简短的回退回答追加紧凑数据段。
	if len(response.Facts) > 0 && len([]rune(answer)) < 80 {
		answer += "\n\n"
		for _, fact := range response.Facts {
			if containsInternalReference(fact.Text) {
				return "", errors.New("fact contains internal reference")
			}
			answer += "- " + fact.Text + "\n"
		}
	}
	if len(response.Recommendations) > 0 && len([]rune(answer)) < 80 {
		answer += "\n\n"
		for _, recommendation := range response.Recommendations {
			if containsInternalReference(recommendation) {
				return "", errors.New("recommendation contains internal reference")
			}
			answer += "- " + recommendation + "\n"
		}
	}
	// 确认措辞必须来自模型明确的澄清；绝不追加掩盖用户需要回答内容的含糊结尾。
	return answer, nil
}

// 模型偶尔会把 Markdown 换行编码成 JSON 答案中的字面转义序列。
// 这里只解码呈现转义，确保标题、表格和列表可读，同时不改变事实内容。
func normalizeMarkdownEscapes(value string) string {
	value = strings.ReplaceAll(value, `\n`, "\n")
	value = strings.ReplaceAll(value, `\/`, "/")
	lines := strings.Split(value, "\n")
	cleaned := lines[:0]
	for _, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "text") {
			continue
		}
		line = strings.ReplaceAll(line, "diagnosis conclusion", "诊断结论")
		line = strings.ReplaceAll(line, "Diagnosis Conclusion", "诊断结论")
		line = strings.ReplaceAll(line, "Action Items", "行动建议")
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

// normalizeMarkdownStructure 修复模型偶发的标题、表头或分隔线粘连问题。
// 只调整 Markdown 的行边界，不修改正文、数字或事实内容。
func normalizeMarkdownStructure(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			if pipe := strings.Index(trimmed, "|"); pipe > 0 {
				prefix := strings.TrimRight(trimmed[:pipe], " \t")
				remainder := strings.TrimLeft(trimmed[pipe:], " \t")
				lines[i] = prefix + "\n" + remainder
			}
		}
	}
	return strings.Join(lines, "\n")
}

func containsInternalReference(value string) bool {
	lower := strings.ToLower(value)
	for _, token := range []string{"mcp", "jsonpath", "schema", "source_ref", "source_tool", "tool_name", "input_schema", "tool_call", "handle_id", "工具调用", "调用参数"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}
