package mcpclient

import (
	"context"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

type Source struct{ Deny bool }

func (Source) Name() string { return "mcp" }

func (source Source) ListTools(context.Context, core.ToolScope) ([]core.Tool, error) {
	if source.Deny {
		return nil, ErrDenied
	}
	return []core.Tool{
		{Name: "echo", Description: "返回输入"},
	}, nil
}

func (source Source) CallTool(_ context.Context, _ core.ToolScope, name string, args map[string]any) (core.ToolResult, error) {
	if source.Deny {
		return core.ToolResult{Status: "permission_denied"}, ErrDenied
	}
	return core.ToolResult{
		ToolName: name,
		Status:   "success",
		Data:     args,
	}, nil
}

type Client struct{ Source }

func (Client) Initialize(context.Context) error { return nil }

func (client Client) ListTools(ctx context.Context) ([]core.Tool, error) {
	return client.Source.ListTools(ctx, core.ToolScope{})
}

func (client Client) CallTool(ctx context.Context, name string, args map[string]any) (core.ToolResult, error) {
	return client.Source.CallTool(ctx, core.ToolScope{}, name, args)
}

var ErrDenied = core.ErrPermissionDenied
