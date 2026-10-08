package harness

import (
	"testing"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

type staticCatalog struct {
	tools  []core.Tool
	loaded bool
}

func (c staticCatalog) Get(core.ToolScope) ([]core.Tool, bool) {
	return append([]core.Tool(nil), c.tools...), c.loaded
}

func TestLoadToolSchemasUsesProvidedCatalog(t *testing.T) {
	catalog := staticCatalog{loaded: true, tools: []core.Tool{{
		Name: "workflow_tool", InputSchema: map[string]any{"type": "object"},
	}}}
	result, err := LoadToolSchemas(catalog, core.ToolScope{SessionID: "session"}, map[string]any{
		"tool_names": []any{"workflow_tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != core.ToolStatusSuccess || len(result.LoadedSchemas) != 1 || result.LoadedSchemas[0].Name != "workflow_tool" {
		t.Fatalf("Schema 加载结果 = %#v", result)
	}
}

func TestInspectDefinitionDoesNotExposeProjection(t *testing.T) {
	properties, _ := InspectDefinition().InputSchema["properties"].(map[string]any)
	if _, exists := properties["select"]; exists {
		t.Fatal("inspect Schema 不得暴露字段投影")
	}
	if _, exists := properties["json_path"]; !exists {
		t.Fatal("inspect Schema 缺少 json_path")
	}
}
