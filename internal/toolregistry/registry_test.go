package toolregistry

import (
	"context"
	"testing"

	"github.com/HolySSSSShit/go-agent/internal/core"
	"github.com/HolySSSSShit/go-agent/internal/harness"
	"github.com/HolySSSSShit/go-agent/internal/mcpclient"
)

type staticToolSource struct {
	name  string
	tools []core.Tool
}

type scopedToolSource struct{}

func (scopedToolSource) Name() string { return "mcp" }
func (scopedToolSource) ListTools(_ context.Context, scope core.ToolScope) ([]core.Tool, error) {
	return []core.Tool{{Name: scope.TenantID + "_" + scope.UserID}}, nil
}
func (scopedToolSource) CallTool(context.Context, core.ToolScope, string, map[string]any) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}

func (s staticToolSource) Name() string { return s.name }

func (s staticToolSource) ListTools(context.Context, core.ToolScope) ([]core.Tool, error) {
	return append([]core.Tool(nil), s.tools...), nil
}

func (s staticToolSource) CallTool(context.Context, core.ToolScope, string, map[string]any) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}

func TestSessionRegistrationAndResolve(t *testing.T) {
	registry := New()
	if err := registry.Register(mcpclient.Source{}); err != nil {
		t.Fatal(err)
	}
	scope := core.ToolScope{TenantID: "tenant-1", SessionID: "session-1"}
	if err := registry.LoadSession(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	tools, ok := registry.Get(scope)
	if !ok || len(tools) != 1 {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	if _, _, ok := registry.Resolve(scope, "echo"); !ok {
		t.Fatal("expected registered tool")
	}
}

func TestToolSchemaLoaderReturnsOnlySessionRegisteredTools(t *testing.T) {
	registry := New()
	source := mcpclient.Source{}
	if err := registry.Register(source); err != nil {
		t.Fatal(err)
	}
	if err := harness.Register(registry, source); err != nil {
		t.Fatal(err)
	}
	scope := core.ToolScope{TenantID: "tenant-1", SessionID: "session-1"}
	if err := registry.LoadSession(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	result, err := harness.LoadToolSchemas(registry, scope, map[string]any{"tool_names": []any{"get_tool_schema"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HideFromPrompt || len(result.LoadedSchemas) != 1 || result.LoadedSchemas[0].Name != "get_tool_schema" || result.LoadedSchemas[0].InputSchema == nil {
		t.Fatalf("unexpected schema loader result: %#v", result)
	}
	result, err = harness.LoadToolSchemas(registry, scope, map[string]any{"tool_names": []any{"unknown"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != core.ToolStatusFailed {
		t.Fatalf("unknown tools must be rejected: %#v", result)
	}
}

func TestHarnessRegistrationIsAtomicAndRejectsDuplicates(t *testing.T) {
	registry := New()
	source := staticToolSource{name: "mcp"}
	if err := harness.Register(registry, source); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"inspect_handle_data", "get_tool_schema"} {
		if _, definition, ok := registry.Resolve(core.ToolScope{SessionID: "session"}, name); !ok || !definition.Bootstrap {
			t.Fatalf("Harness 工具 %q 未注册：%#v, %t", name, definition, ok)
		}
	}
	if err := harness.Register(registry, source); err == nil {
		t.Fatal("重复注册 Harness 工具应失败")
	}
	if len(registry.internal) != 2 {
		t.Fatalf("重复批次不应产生部分写入：%d", len(registry.internal))
	}
}

func TestRegistryKeepsToolSourcePerSession(t *testing.T) {
	registry := New()
	for _, source := range []staticToolSource{
		{name: "catalog", tools: []core.Tool{{Name: "list_products"}}},
		{name: "orders", tools: []core.Tool{{Name: "get_order"}}},
	} {
		if err := registry.Register(source); err != nil {
			t.Fatal(err)
		}
	}
	scope := core.ToolScope{SessionID: "multi-source"}
	if err := registry.Refresh(context.Background(), "catalog", scope); err != nil {
		t.Fatal(err)
	}
	if err := registry.Refresh(context.Background(), "orders", scope); err != nil {
		t.Fatal(err)
	}
	resolved, _, ok := registry.Resolve(scope, "get_order")
	if !ok || resolved.Name() != "orders" {
		t.Fatalf("resolved source = %v, ok=%t", resolved, ok)
	}
	tools, ok := registry.Get(scope)
	if !ok || len(tools) != 2 {
		t.Fatalf("multi-source tools = %#v, ok=%t", tools, ok)
	}
}

func TestRegistryRejectsCrossSourceToolNameCollision(t *testing.T) {
	registry := New()
	for _, source := range []staticToolSource{
		{name: "first", tools: []core.Tool{{Name: "same"}}},
		{name: "second", tools: []core.Tool{{Name: "same"}}},
	} {
		if err := registry.Register(source); err != nil {
			t.Fatal(err)
		}
	}
	scope := core.ToolScope{SessionID: "collision"}
	if err := registry.Refresh(context.Background(), "first", scope); err != nil {
		t.Fatal(err)
	}
	if err := registry.Refresh(context.Background(), "second", scope); err == nil {
		t.Fatal("cross-source tool collision should be rejected")
	}
}

func TestRegistryIsolatesSameSessionIDByOwner(t *testing.T) {
	registry := New()
	if err := registry.Register(scopedToolSource{}); err != nil {
		t.Fatal(err)
	}
	left := core.ToolScope{TenantID: "tenant-a", UserID: "user-a", SessionID: "shared"}
	right := core.ToolScope{TenantID: "tenant-b", UserID: "user-b", SessionID: "shared"}
	for _, scope := range []core.ToolScope{left, right} {
		if err := registry.LoadSession(context.Background(), scope); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, ok := registry.Resolve(left, "tenant-a_user-a"); !ok {
		t.Fatal("左侧身份的工具目录丢失")
	}
	if _, _, ok := registry.Resolve(left, "tenant-b_user-b"); ok {
		t.Fatal("相同 session_id 不得跨身份共享工具目录")
	}
}

func TestRegistryEvictsOldestSessionCatalogAtCapacity(t *testing.T) {
	registry := New()
	registry.maxSessions = 1
	if err := registry.Register(scopedToolSource{}); err != nil {
		t.Fatal(err)
	}
	first := core.ToolScope{TenantID: "tenant", UserID: "first", SessionID: "one"}
	second := core.ToolScope{TenantID: "tenant", UserID: "second", SessionID: "two"}
	if err := registry.LoadSession(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := registry.LoadSession(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get(first); ok {
		t.Fatal("达到容量后应淘汰最旧工具目录")
	}
	if _, ok := registry.Get(second); !ok {
		t.Fatal("最新工具目录不应被淘汰")
	}
}
