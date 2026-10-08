package harness

import (
	"context"
	"errors"
	"fmt"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

const ToolSchemaLoaderName = "get_tool_schema"

// Registry 是 Harness 全局工具装配所需的最小共享目录边界。
type Registry interface {
	Get(core.ToolScope) ([]core.Tool, bool)
	RegisterInternalBatch([]core.InternalToolRegistration) error
}

// ToolCatalog 是 Schema 加载器读取当前有效工具视图所需的最小边界。
// Workflow 可以传入经过能力绑定后的目录，避免共享目录看不到私有工具。
type ToolCatalog interface {
	Get(core.ToolScope) ([]core.Tool, bool)
}

// Register 注册跨 Workflow 共用的句柄查询和 Schema 加载工具。
// inspector 是当前句柄存储的读取实现；它可以由 MCP 或其他后端提供。
func Register(registry Registry, inspector core.ToolSource) error {
	if registry == nil {
		return errors.New("Harness tool registry is nil")
	}
	if inspector == nil {
		return errors.New("inspect tool source is nil")
	}
	return registry.RegisterInternalBatch([]core.InternalToolRegistration{
		{Definition: InspectDefinition(), Handler: func(ctx context.Context, scope core.ToolScope, arguments map[string]any) (core.ToolResult, error) {
			result, err := inspector.CallTool(ctx, scope, "inspect_handle_data", arguments)
			if err != nil {
				return core.ToolResult{}, fmt.Errorf("inspect handle data: %w", err)
			}
			return result, nil
		}},
		{Definition: ToolSchemaLoaderDefinition(), Handler: func(_ context.Context, scope core.ToolScope, arguments map[string]any) (core.ToolResult, error) {
			return LoadToolSchemas(registry, scope, arguments)
		}},
	})
}

// LoadToolSchemas 从调用方提供的有效工具目录中加载完整 Schema。
func LoadToolSchemas(catalog ToolCatalog, scope core.ToolScope, arguments map[string]any) (core.ToolResult, error) {
	names, err := toolNames(arguments["tool_names"])
	if err != nil {
		return core.ToolResult{ToolName: ToolSchemaLoaderName, Status: core.ToolStatusFailed, Error: err.Error()}, nil
	}
	tools, ok := catalog.Get(scope)
	if !ok {
		return core.ToolResult{ToolName: ToolSchemaLoaderName, Status: core.ToolStatusFailed, Error: "当前会话工具目录不存在"}, nil
	}
	byName := make(map[string]core.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	loaded := make([]core.Tool, 0, len(names))
	for _, name := range names {
		tool, exists := byName[name]
		if !exists {
			return core.ToolResult{ToolName: ToolSchemaLoaderName, Status: core.ToolStatusFailed, Error: "工具不在当前会话目录中：" + name}, nil
		}
		loaded = append(loaded, tool)
	}
	return core.ToolResult{ToolName: ToolSchemaLoaderName, Status: core.ToolStatusSuccess, LoadedSchemas: loaded, HideFromPrompt: true}, nil
}

func toolNames(value any) ([]string, error) {
	var raw []string
	switch names := value.(type) {
	case []string:
		raw = names
	case []any:
		raw = make([]string, 0, len(names))
		for _, value := range names {
			name, ok := value.(string)
			if !ok {
				return nil, errors.New("tool_names 必须是字符串数组")
			}
			raw = append(raw, name)
		}
	default:
		return nil, errors.New("tool_names 必须是非空字符串数组")
	}
	seen := make(map[string]struct{}, len(raw))
	result := make([]string, 0, len(raw))
	for _, name := range raw {
		if name == "" {
			return nil, errors.New("tool_names 不能包含空名称")
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	if len(result) == 0 {
		return nil, errors.New("tool_names 必须是非空字符串数组")
	}
	return result, nil
}

func ToolSchemaLoaderDefinition() core.Tool {
	return core.Tool{
		Name:        ToolSchemaLoaderName,
		Description: "读取当前会话中指定工具的完整输入 Schema。仅当准备调用某个工具或需要根据其参数修正失败调用时使用；参数固定为 {\"tool_names\":[\"工具名称\"]}，可一次请求多个名称。",
		Internal:    true,
		Bootstrap:   true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool_names": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
			},
			"required": []string{"tool_names"},
		},
	}
}

func InspectDefinition() core.Tool {
	return core.Tool{Name: "inspect_handle_data", Description: "对已保存的工具结果执行 JSONPath 查询，并原样返回命中的值。", Internal: true, Bootstrap: true, InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{
			"handle_id": map[string]any{"type": "string"},
			"json_path": map[string]any{"type": "string", "description": "使用 ojg/jp 支持的 JSONPath 查询原始句柄数据。预览中的数组可能只展示前几项，不代表原始数组只有这些项。先确定要归因的对象维度，再列出支撑比较和解释所需的全部指标；优先查询同一原始记录中的成组指标，不能把多个独立字段数组直接当成已经对应的证据，除非能确认它们来自同一记录集合且长度、顺序和缺失值语义一致。只执行路径并原样返回命中值，不做投影、重建、聚合或结构改写。根节点 $；字段 $.field；数组 [0] 或 [*]；过滤 [?(@.field == value)]。禁止使用对象投影（{...}）、字段别名（:）、逗号联合字段或脚本表达式。示例：$.items[*]、$.items[*].values[0]、$.items[?(@.id == 123)]。多值结果原样返回。后续路径始终针对原始句柄结构；结果无法支持归因、路径未命中或结果缺字段时不要重复相同 inspect，应重新调用业务工具一次补齐依赖指标或结束为证据不足；聚合请调用业务工具。"},
		}, "required": []string{"handle_id", "json_path"},
	}}
}
