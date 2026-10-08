package model

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

type captureImageStore struct {
	mime string
	data []byte
}

func (s *captureImageStore) Save(_ context.Context, mimeType string, data []byte) (string, error) {
	s.mime = mimeType
	s.data = append([]byte(nil), data...)
	return "/api/generated-images/test.png", nil
}

func TestOpenAIImageGeneratorChatExtractsDataURI(t *testing.T) {
	png := []byte("fake-png")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, exists := body["quality"]; exists {
			t.Fatalf("chat image request must not include quality: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"![image](data:image/png;base64,` + base64.StdEncoding.EncodeToString(png) + `)"}}]}`))
	}))
	defer server.Close()
	store := &captureImageStore{}
	generator := OpenAIImageGenerator{BaseURL: server.URL, APIKey: "secret", Model: "gemini-image", Mode: "chat", Quality: "low", Store: store}
	url, err := generator.GenerateImage(context.Background(), core.ImageGenerationRequest{Prompt: "test", Size: "1920x800"})
	if err != nil || url != "/api/generated-images/test.png" || store.mime != "image/png" || string(store.data) != string(png) {
		t.Fatalf("unexpected chat image result: url=%q mime=%q data=%q err=%v", url, store.mime, store.data, err)
	}
}

func TestOpenAIImageGeneratorImagesDecodesBase64(t *testing.T) {
	png := []byte("fake-png")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["quality"] != "low" || body["size"] != "1920x800" {
			t.Fatalf("unexpected image request: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(png) + `"}]}`))
	}))
	defer server.Close()
	store := &captureImageStore{}
	generator := OpenAIImageGenerator{BaseURL: server.URL, APIKey: "secret", Model: "gpt-image-2", Mode: "images", Quality: "low", Store: store}
	url, err := generator.GenerateImage(context.Background(), core.ImageGenerationRequest{Prompt: "test", Size: "1920x800"})
	if err != nil || url == "" || !strings.EqualFold(store.mime, "image/png") || string(store.data) != string(png) {
		t.Fatalf("unexpected images result: url=%q mime=%q data=%q err=%v", url, store.mime, store.data, err)
	}
}

func TestOpenAIImageGeneratorChatExtractsDataURIFromContentParts(t *testing.T) {
	png := []byte("fake-png")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(png) + `"}}]}}]}`))
	}))
	defer server.Close()
	store := &captureImageStore{}
	generator := OpenAIImageGenerator{BaseURL: server.URL, APIKey: "secret", Model: "gemini-image", Mode: "chat", Store: store}
	if _, err := generator.GenerateImage(context.Background(), core.ImageGenerationRequest{Prompt: "test"}); err != nil {
		t.Fatalf("expected content parts to decode: %v", err)
	}
	if string(store.data) != string(png) {
		t.Fatalf("unexpected image data: %q", store.data)
	}
}

func TestNormalizeOpenAIImageSize(t *testing.T) {
	if got := normalizeOpenAIImageSize("1920*800"); got != "1920x800" {
		t.Fatalf("unexpected size: %s", got)
	}
	if got := normalizeOpenAIImageSize(""); got != "1920x800" {
		t.Fatalf("unexpected default size: %s", got)
	}
}
