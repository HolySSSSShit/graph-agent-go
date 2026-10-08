package model

import (
	"context"
	"fmt"
	"strings"

	"github.com/HolySSSSShit/graph-agent-go/internal/config"
	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

const mockURLPrefix = "http://mock-model.local"

// Catalog 根据模型名称选择配置好的厂商适配器。OpenAI 兼容厂商复用同一 HTTP 实现。
type Catalog struct {
	models map[string]core.Model
}

func NewCatalog(modelConfig config.ModelConfig) Catalog {
	models := make(map[string]core.Model)
	for provider := range modelConfig.Providers {
		for _, route := range append([]string{modelConfig.Default}, values(modelConfig.Roles)...) {
			nameProvider, modelName := splitModelRef(route)
			if nameProvider != provider || modelName == "" {
				continue
			}
			if _, exists := models[route]; exists {
				continue
			}
			if option, ok := modelConfig.Find(route); ok {
				models[route] = modelFor(option, modelName)
			}
		}
	}
	return Catalog{models: models}
}

func (c Catalog) Select(_ context.Context, name string) (core.Model, core.RouteInfo, error) {
	model, ok := c.models[name]
	if !ok {
		return nil, core.RouteInfo{}, fmt.Errorf("model is not configured: %s", name)
	}
	provider, modelName := splitModelRef(name)
	return model, core.RouteInfo{Provider: provider, Model: modelName, Reason: "configured model catalog"}, nil
}

func modelFor(option config.ModelOption, modelName string) core.Model {
	var client core.Model
	if option.APIKey == "" || strings.HasPrefix(option.BaseURL, mockURLPrefix) {
		client = Mock{}
	} else if strings.EqualFold(option.Provider, "gemini") {
		client = Gemini{BaseURL: option.BaseURL, APIKey: option.APIKey}
	} else {
		client = OpenAICompatible{
			BaseURL:        option.BaseURL,
			APIKey:         option.APIKey,
			EnableThinking: option.EnableThinking,
			ThinkingBudget: option.ThinkingBudget,
		}
	}
	return namedModel{Name: modelName, Model: WithDefaults{Model: client, Temperature: option.Temperature, TopP: option.TopP, MaxTokens: option.MaxTokens, StructuredOutputs: option.StructuredOutputs}}
}

type namedModel struct {
	Name  string
	Model core.Model
}

func (m namedModel) Generate(ctx context.Context, request core.ModelRequest) (core.ModelEvent, error) {
	request.Model = m.Name
	return m.Model.Generate(ctx, request)
}

func (m namedModel) Stream(ctx context.Context, request core.ModelRequest) (<-chan core.ModelEvent, error) {
	request.Model = m.Name
	return m.Model.Stream(ctx, request)
}

func splitModelRef(ref string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(ref), ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func values(routes map[core.ModelRole]string) []string {
	result := make([]string, 0, len(routes))
	for _, route := range routes {
		result = append(result, route)
	}
	return result
}

// WithDefaults 将模型目录的默认采样参数注入请求；调用方显式设置时优先使用调用方值。
type WithDefaults struct {
	Model             core.Model
	Temperature       float32
	TopP              float32
	MaxTokens         int
	StructuredOutputs bool
}

func (m WithDefaults) Generate(ctx context.Context, request core.ModelRequest) (core.ModelEvent, error) {
	return m.Model.Generate(ctx, m.apply(request))
}

func (m WithDefaults) Stream(ctx context.Context, request core.ModelRequest) (<-chan core.ModelEvent, error) {
	return m.Model.Stream(ctx, m.apply(request))
}

func (m WithDefaults) apply(request core.ModelRequest) core.ModelRequest {
	if request.Temperature == nil {
		value := m.Temperature
		request.Temperature = &value
	}
	if request.TopP == nil {
		value := m.TopP
		request.TopP = &value
	}
	if request.MaxTokens == 0 {
		request.MaxTokens = m.MaxTokens
	}
	if m.StructuredOutputs && request.ResponseFormat == nil && len(request.Tools) == 0 {
		request.ResponseFormat = &core.ResponseFormat{Type: core.ResponseFormatJSON}
	}
	return request
}
