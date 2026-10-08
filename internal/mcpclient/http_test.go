package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestNewHTTPSourceDefaultsToTwentySecondTimeout(t *testing.T) {
	source := NewHTTPSource("http://unused", "", defaultProtocolVersion, nil, 1024)
	if source.Client == nil || source.Client.Timeout != 20*time.Second {
		t.Fatalf("MCP client timeout = %v, want 20s", source.Client)
	}
}

func TestHTTPSourceInitializesListsAndCallsTools(t *testing.T) {
	var mu sync.Mutex
	methods := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Mcp-token"); got != "token-a" {
			t.Errorf("unexpected token header: %q", got)
		}
		var body struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		methods = append(methods, body.Method)
		mu.Unlock()
		if body.Method == "notifications/initialized" {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": body.ID}
		switch body.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": defaultProtocolVersion}
		case "tools/list":
			response["result"] = map[string]any{"tools": []map[string]any{{
				"name": "get_order", "description": "查询订单", "inputSchema": map[string]any{"type": "object"},
			}}}
		case "tools/call":
			response["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": `{"id":"order-1001","status":"paid"}`}}}
		default:
			t.Fatalf("unexpected method %q", body.Method)
		}
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()

	source := NewHTTPSource(server.URL, "token-a", defaultProtocolVersion, server.Client(), 1024)
	tools, err := source.ListTools(context.Background(), core.ToolScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "get_order" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	if tools[0].InputSchema["type"] != "object" {
		t.Fatalf("tool input schema was not decoded: %+v", tools[0].InputSchema)
	}
	result, err := source.CallTool(context.Background(), core.ToolScope{}, "get_order", map[string]any{"path": map[string]any{"id": "order-1001"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected result: %+v", result)
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["id"] != "order-1001" {
		t.Fatalf("tool result was not decoded: %#v", result.Data)
	}
	mu.Lock()
	defer mu.Unlock()
	if got, want := methods, []string{"initialize", "notifications/initialized", "tools/list", "tools/call"}; !sameStrings(got, want) {
		t.Fatalf("unexpected MCP sequence: got %v want %v", got, want)
	}
}

func TestInspectHandleExecutesOnlyJSONPath(t *testing.T) {
	source := NewHTTPSource("http://unused", "", defaultProtocolVersion, nil, 1024)
	handle := source.shadow.put(map[string]any{"rows": []any{
		map[string]any{"id": float64(1), "meta": map[string]any{"name": "a"}},
		map[string]any{"id": float64(2), "meta": map[string]any{"name": "b"}},
	}})
	result := source.inspectHandle(map[string]any{"handle_id": handle, "json_path": "$.rows[*].meta.name"})
	if result.Status != core.ToolStatusSuccess {
		t.Fatalf("inspect failed: %+v", result)
	}
	names, ok := result.Data.([]any)
	if !ok || len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("inspect should return the direct multi-value query result: %#v", result.Data)
	}
}

func TestInspectHandlePreservesOriginalResultSource(t *testing.T) {
	source := &HTTPSource{shadow: newShadowStorage()}
	handle := source.shadow.put(map[string]any{"field": 8}, core.ResultSource{ToolName: "source_a", Reference: "origin"})
	result := source.inspectHandle(map[string]any{"handle_id": handle, "json_path": "$.field"})
	if result.Source == nil || result.Source.ToolName != "source_a" || result.Source.Reference != "origin" {
		t.Fatalf("inspect source = %#v", result.Source)
	}
}

func TestInspectWildcardKeepsSingleMatchAsCollection(t *testing.T) {
	source := &HTTPSource{shadow: newShadowStorage()}
	handle := source.shadow.put(map[string]any{
		"current_data": []any{map[string]any{"values": []any{map[string]any{"uv": "8"}}}},
	}, core.ResultSource{ToolName: "get_sales_trend"})
	result := source.inspectHandle(map[string]any{"handle_id": handle, "json_path": "$.current_data[*]"})
	if result.Status != core.ToolStatusSuccess {
		t.Fatalf("inspect failed: %+v", result)
	}
	rows, ok := result.Data.([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("single wildcard match must remain a collection: %#v", result.Data)
	}
	value, err := source.VerifyEvidence(context.Background(), core.ResultSource{ToolName: "get_sales_trend", Reference: handle}, "$.current_data[0].values[0].uv")
	if err != nil || value != "8" {
		t.Fatalf("original path should remain verifiable: value=%#v err=%v", value, err)
	}
}

func TestVerifyEvidenceUnwrapsPreviewPath(t *testing.T) {
	source := &HTTPSource{shadow: newShadowStorage()}
	handle := source.shadow.put(map[string]any{
		"current_data": []any{map[string]any{"values": []any{map[string]any{"order_total_price": 42.5}}}},
	}, core.ResultSource{ToolName: "get_sales_trend"})
	value, err := source.VerifyEvidence(context.Background(), core.ResultSource{ToolName: "get_sales_trend", Reference: handle}, "$.preview.current_data[0].values[0].order_total_price")
	if err != nil || value != 42.5 {
		t.Fatalf("preview path should unwrap to original path: value=%#v err=%v", value, err)
	}
}

func TestHTTPSourceClassifiesPermissionDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error":   map[string]any{"code": -32000, "message": "missing Mcp-token"},
		})
	}))
	defer server.Close()

	source := NewHTTPSource(server.URL, "", defaultProtocolVersion, server.Client(), 1024)
	_, err := source.ListTools(context.Background(), core.ToolScope{})
	if !errors.Is(err, core.ErrPermissionDenied) {
		t.Fatalf("expected permission denied error, got %v", err)
	}
}

func TestHTTPSourceClassifiesUpstreamUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error":   map[string]any{"code": -32000, "message": "upstream unauthorized"},
		})
	}))
	defer server.Close()

	source := NewHTTPSource(server.URL, "token-a", defaultProtocolVersion, server.Client(), 1024)
	_, err := source.ListTools(context.Background(), core.ToolScope{})
	if !errors.Is(err, core.ErrPermissionDenied) {
		t.Fatalf("expected upstream unauthorized to be permission denied, got %v", err)
	}
}

func TestHTTPSourceClassifiesBusinessCodeInSuccessfulContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": body.ID}
		switch body.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": defaultProtocolVersion}
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			response["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": `{"code":400,"msg":"invalid query","data":null}`}}}
		default:
			response["result"] = map[string]any{"tools": []any{}}
		}
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()

	source := NewHTTPSource(server.URL, "token-a", defaultProtocolVersion, server.Client(), 1024)
	result, err := source.CallTool(context.Background(), core.ToolScope{}, "get_sales_summary", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != core.ToolStatusBusinessError || result.Error != "invalid query" {
		t.Fatalf("成功内容中的业务码应被识别为业务错误：%+v", result)
	}
}

func TestHTTPSourcePrefersStructuredToolContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": body.ID}
		switch body.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": defaultProtocolVersion}
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			response["result"] = map[string]any{
				"content":           []map[string]any{{"type": "text", "text": "not the structured value"}},
				"structuredContent": map[string]any{"current_data": []any{map[string]any{"uv": 12}}},
			}
		default:
			response["result"] = map[string]any{"tools": []any{}}
		}
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()

	source := NewHTTPSource(server.URL, "token-a", defaultProtocolVersion, server.Client(), 1024)
	result, err := source.CallTool(context.Background(), core.ToolScope{}, "get_sales_summary", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	object, ok := result.Data.(map[string]any)
	if !ok || object["current_data"] == nil {
		t.Fatalf("expected structured tool content, got %#v", result.Data)
	}
}

func TestHTTPSourceClassifiesPermissionCodeInSuccessfulContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": body.ID}
		switch body.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": defaultProtocolVersion}
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			response["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": `{"code":401,"msg":"Token-Error，请重新获取授权","data":null}`}}}
		default:
			response["result"] = map[string]any{"tools": []any{}}
		}
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()

	source := NewHTTPSource(server.URL, "token-a", defaultProtocolVersion, server.Client(), 1024)
	result, err := source.CallTool(context.Background(), core.ToolScope{}, "get_sales_summary", map[string]any{})
	if !errors.Is(err, core.ErrPermissionDenied) || result.Status != core.ToolStatusPermissionDenied {
		t.Fatalf("成功内容中的 401 应被识别为权限错误：result=%+v err=%v", result, err)
	}
}

func TestHTTPSourceAcceptsNegotiatedProtocolVersion(t *testing.T) {
	var listedProtocol string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Method == "notifications/initialized" {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if body.Method == "initialize" {
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"jsonrpc": "2.0", "id": body.ID, "result": map[string]any{"protocolVersion": "2024-11-05"},
			})
			return
		}
		listedProtocol = request.Header.Get("MCP-Protocol-Version")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0", "id": body.ID, "result": map[string]any{"tools": []any{}},
		})
	}))
	defer server.Close()

	source := NewHTTPSource(server.URL, "token-a", defaultProtocolVersion, server.Client(), 1024)
	if _, err := source.ListTools(context.Background(), core.ToolScope{}); err != nil {
		t.Fatal(err)
	}
	if listedProtocol != "2024-11-05" {
		t.Fatalf("expected negotiated protocol header, got %q", listedProtocol)
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
