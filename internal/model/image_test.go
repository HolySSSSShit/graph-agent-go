package model

import (
	"context"
	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQwenImageGenerator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"https://cdn.test/image.png"}]}`))
	}))
	defer server.Close()
	generator := QwenImageGenerator{BaseURL: server.URL, APIKey: "secret", Model: "qwen-image"}
	url, err := generator.GenerateImage(context.Background(), core.ImageGenerationRequest{Prompt: "hero", Size: "1024*1024"})
	if err != nil || url != "https://cdn.test/image.png" {
		t.Fatalf("url=%q err=%v", url, err)
	}
}

func TestQwenImageGeneratorDashScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/generation" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[{"image":"https://cdn.test/dashscope.png"}]}}]}}`))
	}))
	defer server.Close()
	generator := QwenImageGenerator{BaseURL: server.URL + "/generation", APIKey: "secret", Model: "qwen-image-3.0", DashScope: true}
	url, err := generator.GenerateImage(context.Background(), core.ImageGenerationRequest{Prompt: "hero", Size: "1024*1024"})
	if err != nil || url != "https://cdn.test/dashscope.png" {
		t.Fatalf("url=%q err=%v", url, err)
	}
}

func TestStaticImageGeneratorCyclesURLs(t *testing.T) {
	g := &StaticImageGenerator{URLs: []string{"a", "b"}}
	for i, want := range []string{"a", "b", "a"} {
		got, err := g.GenerateImage(context.Background(), core.ImageGenerationRequest{})
		if err != nil || got != want {
			t.Fatalf("call %d: got %q err=%v, want %q", i, got, err, want)
		}
	}
}
