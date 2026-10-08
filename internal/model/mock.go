package model

import (
	"context"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

// Mock 返回通用文本，仅用于无凭证的本地联调。
type Mock struct{}

func (Mock) Generate(ctx context.Context, _ core.ModelRequest) (core.ModelEvent, error) {
	if err := ctx.Err(); err != nil {
		return core.ModelEvent{}, err
	}
	return core.ModelEvent{Type: "text", Text: "Mock model response"}, nil
}

func (m Mock) Stream(ctx context.Context, request core.ModelRequest) (<-chan core.ModelEvent, error) {
	response, err := m.Generate(ctx, request)
	if err != nil {
		return nil, err
	}
	channel := make(chan core.ModelEvent, 2)
	channel <- core.ModelEvent{Type: "text_delta", Text: response.Text}
	channel <- core.ModelEvent{Type: "done"}
	close(channel)
	return channel, nil
}

type Router struct{ Model core.Model }

func (r Router) Select(_ context.Context, modelName string) (core.Model, core.RouteInfo, error) {
	client := r.Model
	if client == nil {
		client = Mock{}
	}
	return client, core.RouteInfo{Provider: "mock", Model: modelName, Reason: "local mock"}, nil
}
