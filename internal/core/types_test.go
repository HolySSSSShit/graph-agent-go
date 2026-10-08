package core

import (
	"strings"
	"testing"
)

func TestPromptProtocolRendersSchemaFromGoStruct(t *testing.T) {
	template := PromptTemplate{
		Protocol: ResponseProtocol{
			Name:   "test.protocol",
			Fields: []ProtocolField{{Name: "old_field", Type: "string", Required: true}},
			Schema: struct {
				NewField []string `json:"new_field"`
				Optional bool     `json:"optional,omitempty"`
			}{},
		},
	}
	rendered := template.Render()
	if !strings.Contains(rendered, "new_field(array，必填)") {
		t.Fatalf("协议没有从结构体生成新字段：%s", rendered)
	}
	if !strings.Contains(rendered, "optional(boolean，可选)") {
		t.Fatalf("协议没有识别 omitempty：%s", rendered)
	}
	if strings.Contains(rendered, "old_field") {
		t.Fatalf("协议仍然使用已过期的手写字段：%s", rendered)
	}
	if !strings.Contains(rendered, "结构示例：\n```json") || !strings.Contains(rendered, "\n```") || !strings.Contains(rendered, "\"new_field\": [\"string\"]") {
		t.Fatalf("协议没有生成 JSON 结构示例：%s", rendered)
	}
}
