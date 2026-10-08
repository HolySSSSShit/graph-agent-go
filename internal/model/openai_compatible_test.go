package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestOpenAICompatibleSendsJSONResponseFormat(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &received); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer server.Close()
	enableThinking := false
	client := OpenAICompatible{BaseURL: server.URL, Client: server.Client(), EnableThinking: &enableThinking, ThinkingBudget: 2048}
	_, err := client.Generate(context.Background(), core.ModelRequest{
		Model:          "qwen-plus",
		Messages:       []core.Message{{Role: "user", Content: "返回 JSON"}},
		ResponseFormat: &core.ResponseFormat{Type: core.ResponseFormatJSON},
	})
	if err != nil {
		t.Fatal(err)
	}
	format, ok := received["response_format"].(map[string]any)
	if !ok || format["type"] != string(core.ResponseFormatJSON) {
		t.Fatalf("response_format was not sent: %#v", received["response_format"])
	}
	if value, ok := received["enable_thinking"].(bool); !ok || value {
		t.Fatalf("enable_thinking was not sent as false: %#v", received["enable_thinking"])
	}
	if value, ok := received["thinking_budget"].(float64); !ok || value != 2048 {
		t.Fatalf("thinking_budget was not sent: %#v", received["thinking_budget"])
	}
}

func TestParseRecognizedContentIgnoresThinking(t *testing.T) {
	text, call, err := parseRecognizedContent(json.RawMessage(`[{"type":"thinking","text":"internal"},{"type":"text","text":"visible"}]`))
	if err != nil || call != nil || text != "visible" {
		t.Fatalf("unexpected recognized content: text=%q call=%+v err=%v", text, call, err)
	}
}

func TestParseRecognizedContentExtractsToolCall(t *testing.T) {
	text, call, err := parseRecognizedContent(json.RawMessage(`[{"type":"thinking","text":"internal"},{"type":"tool_call","name":"get_sales_summary","input":{"start_date":"2026-09-01"}}]`))
	if err != nil || text != "" || call == nil || call.Name != "get_sales_summary" || call.Arguments["start_date"] != "2026-09-01" {
		t.Fatalf("unexpected recognized tool call: text=%q call=%+v err=%v", text, call, err)
	}
}
