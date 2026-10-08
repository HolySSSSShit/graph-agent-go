package mcpclient

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

type captureSourceRegistrar struct {
	source core.ToolSource
}

func (r *captureSourceRegistrar) Register(source core.ToolSource) error {
	r.source = source
	return nil
}

func TestRegisterOwnsMCPSourceRegistration(t *testing.T) {
	registry := &captureSourceRegistrar{}
	client := &http.Client{Timeout: 7 * time.Second}
	resolver := func(context.Context, core.ToolScope) (string, error) { return "dynamic-token", nil }
	source, err := Register(registry, Options{
		BaseURL:          "https://mcp.example.test",
		Token:            "fixed-token",
		ProtocolVersion:  "test-version",
		Client:           client,
		MaxResponseBytes: 2048,
		ShadowDirectory:  "test-shadow",
		TokenResolver:    resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	if source == nil || registry.source != source || registry.source.Name() != "mcp" {
		t.Fatalf("注册的 MCP source = %#v", registry.source)
	}
	if source.BaseURL != "https://mcp.example.test" || source.Token != "fixed-token" || source.ProtocolVersion != "test-version" {
		t.Fatalf("MCP source 配置未完整传递：%#v", source)
	}
	if source.Client != client || source.MaxResponseBytes != 2048 || source.ShadowDirectory != "test-shadow" || source.TokenResolver == nil {
		t.Fatalf("MCP source 依赖未完整传递：%#v", source)
	}
	if _, err := Register(nil, Options{}); err == nil {
		t.Fatal("空 registry 应被拒绝")
	}
}
