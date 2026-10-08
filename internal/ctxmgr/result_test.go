package ctxmgr

import (
	"context"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestJSONToolResultReducerExtractsFencedJSON(t *testing.T) {
	reducer := JSONToolResultReducer{}
	result, err := reducer.Reduce(context.Background(), core.ToolResult{
		ToolName: "get_order",
		Status:   "success",
		Data:     "```json\n{\"id\":\"order-1001\",\"status\":\"paid\"}\n```",
	}, core.ContextBudget{})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["id"] != "order-1001" {
		t.Fatalf("fenced JSON was not extracted: %#v", result.Data)
	}
}

func TestJSONToolResultReducerTruncatesOversizedResult(t *testing.T) {
	reducer := JSONToolResultReducer{Limit: 8}
	result, err := reducer.Reduce(context.Background(), core.ToolResult{
		ToolName: "get_order",
		Status:   "success",
		Data:     map[string]any{"payload": "这是一段很长的工具返回内容，需要受到上下文预算限制"},
	}, core.ContextBudget{})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["truncated"] != true {
		t.Fatalf("oversized result was not reduced: %#v", result.Data)
	}
}

func TestJSONToolResultReducerDropsVerboseFieldsBeforeTruncating(t *testing.T) {
	reducer := JSONToolResultReducer{Limit: 128}
	result, err := reducer.Reduce(context.Background(), core.ToolResult{
		ToolName: "get_product",
		Status:   "success",
		Data: map[string]any{
			"id":        10561626,
			"body_html": "should not enter context",
			"variants": []any{map[string]any{
				"sku":                "185TM-10",
				"price":              "45.90",
				"inventory_quantity": 0,
				"image_url":          "https://example.invalid/large.jpg",
			}},
		},
	}, core.ContextBudget{})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("结果类型错误：%T", result.Data)
	}
	if _, exists := data["body_html"]; exists {
		t.Fatalf("富文本字段不应进入模型上下文：%+v", data)
	}
	variants, ok := data["variants"].([]any)
	if !ok || len(variants) != 1 {
		t.Fatalf("结构化变体数据不应被删除：%+v", data["variants"])
	}
	variant := variants[0].(map[string]any)
	if variant["sku"] != "185TM-10" || variant["inventory_quantity"] != 0 {
		t.Fatalf("SKU 和库存字段应保留：%+v", variant)
	}
	if _, exists := variant["image_url"]; exists {
		t.Fatalf("媒体地址不应进入模型上下文：%+v", variant)
	}
}

func TestJSONToolResultReducerCompactsNestedJSONString(t *testing.T) {
	reducer := JSONToolResultReducer{Limit: 256}
	result, err := reducer.Reduce(context.Background(), core.ToolResult{
		ToolName: "get_product",
		Status:   "success",
		Data: map[string]any{
			"payload": `{"current_data":[{"id":10561626,"body_html":"<p>very large html</p>","image_url":"https://example.invalid/image.jpg","sku":"SKU-1"}]}`,
		},
	}, core.ContextBudget{})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("结果类型错误：%T", result.Data)
	}
	payload, ok := data["payload"].(map[string]any)
	if !ok {
		t.Fatalf("嵌套 JSON 未展开：%#v", data["payload"])
	}
	items, ok := payload["current_data"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("结构化数组未保留：%#v", payload["current_data"])
	}
	item := items[0].(map[string]any)
	if _, exists := item["body_html"]; exists {
		t.Fatalf("嵌套富文本字段不应进入上下文：%+v", item)
	}
	if _, exists := item["image_url"]; exists {
		t.Fatalf("嵌套媒体地址不应进入上下文：%+v", item)
	}
	if item["sku"] != "SKU-1" {
		t.Fatalf("结构化业务字段应保留：%+v", item)
	}
}
