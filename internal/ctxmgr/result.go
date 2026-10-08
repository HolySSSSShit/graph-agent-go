package ctxmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

const (
	defaultToolResultMaxRunes = 2048
	toolResultTokenRuneRatio  = 2

	// 这些字段通常是富文本、媒体或内部关联信息，对运营分析价值低但体积很大。
	// 结果归并器通过接口可替换；默认实现只做通用降噪，不绑定具体 MCP 工具。
	noisyToolFieldBodyHTML        = "body_html"
	noisyToolFieldHTML            = "html"
	noisyToolFieldDescriptionHTML = "description_html"
	noisyToolFieldSource          = "src"
	noisyToolFieldImageURL        = "image_url"
	noisyToolFieldImageID         = "image_id"
	noisyToolFieldImages          = "images"
	noisyToolFieldMedia           = "media"
)

// JSONToolResultReducer 规范化 MCP 文本结果，并限制其进入模型上下文的大小。
type JSONToolResultReducer struct {
	Limit int
}

func (r JSONToolResultReducer) Reduce(_ context.Context, result core.ToolResult, budget core.ContextBudget) (core.ToolResult, error) {
	result.Data = compactToolData(parseToolData(result.Data))
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("encode tool result: %w", err)
	}
	limit := r.Limit
	if budget.ToolResultMaxTokens > 0 {
		limit = budget.ToolResultMaxTokens
	}
	if limit <= 0 || estimate([]core.Message{{Content: string(encoded)}}) <= limit {
		return result, nil
	}
	return core.ToolResult{
		ToolName:  result.ToolName,
		Status:    result.Status,
		Retryable: result.Retryable,
		Error:     result.Error,
		Source:    result.Source,
		Data: map[string]any{
			"truncated": true,
			"preview":   truncateRunes(string(encoded), limit*toolResultTokenRuneRatio),
		},
	}, nil
}

// compactToolData 在按 token 截断前去除通用的高体积字段，尽量保留结构化业务数据。
func compactToolData(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, child := range current {
			if noisyToolField(key) {
				continue
			}
			result[key] = compactToolData(child)
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, child := range current {
			result[index] = compactToolData(child)
		}
		return result
	case string:
		// 部分 MCP 会把完整 JSON 再放进对象字段的字符串中；递归展开后才能清理其中的富文本字段。
		if parsed, ok := parseEmbeddedJSON(current); ok {
			return compactToolData(parsed)
		}
		return truncateRunes(current, defaultToolResultMaxRunes)
	default:
		return value
	}
}

func noisyToolField(name string) bool {
	switch strings.ToLower(name) {
	case noisyToolFieldBodyHTML, noisyToolFieldHTML, noisyToolFieldDescriptionHTML,
		noisyToolFieldSource, noisyToolFieldImageURL, noisyToolFieldImageID,
		noisyToolFieldImages, noisyToolFieldMedia:
		return true
	default:
		return false
	}
}

func parseToolData(data any) any {
	text, ok := data.(string)
	if !ok {
		return data
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```json") && strings.HasSuffix(text, "```") {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "```json"), "```"))
	}
	if value, ok := parseEmbeddedJSON(text); ok {
		return value
	}
	return data
}

// parseEmbeddedJSON 只展开对象或数组，避免把业务字段中的普通 JSON 文本误判为结构化结果。
func parseEmbeddedJSON(value string) (any, bool) {
	value = strings.TrimSpace(value)
	if value == "" || (value[0] != '{' && value[0] != '[') {
		return nil, false
	}
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return nil, false
	}
	switch parsed.(type) {
	case map[string]any, []any:
		return parsed, true
	default:
		return nil, false
	}
}

func truncateRunes(value string, limit int) string {
	if limit < 1 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
