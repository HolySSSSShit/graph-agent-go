package mcpclient

import (
	"context"
	"errors"
	"net/http"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// SourceRegistrar 是 MCP source 接入共享工具目录所需的最小注册边界。
type SourceRegistrar interface {
	Register(core.ToolSource) error
}

// Options 定义 MCP HTTP source 的基础设施依赖。
// 启动层只提供配置和跨包依赖，不参与 source 的构造与注册细节。
type Options struct {
	BaseURL          string
	Token            string
	ProtocolVersion  string
	Client           *http.Client
	MaxResponseBytes int64
	ShadowDirectory  string
	TokenResolver    func(context.Context, core.ToolScope) (string, error)
}

// Register 构造并注册 MCP HTTP source，返回同一个 source 供调用方注入证据复核等通用能力。
func Register(registry SourceRegistrar, options Options) (*HTTPSource, error) {
	if registry == nil {
		return nil, errors.New("MCP source registry is nil")
	}
	source := NewHTTPSource(
		options.BaseURL,
		options.Token,
		options.ProtocolVersion,
		options.Client,
		options.MaxResponseBytes,
		options.ShadowDirectory,
	)
	source.TokenResolver = options.TokenResolver
	if err := registry.Register(source); err != nil {
		return nil, err
	}
	return source, nil
}
