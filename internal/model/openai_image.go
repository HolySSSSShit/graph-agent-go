package model

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// OpenAIImageGenerator 适配 NewAPI 的两种 OpenAI 兼容图片协议。
// chat 模式用于 Gemini 图片模型，images 模式用于 GPT 图片模型。
type OpenAIImageGenerator struct {
	BaseURL string
	APIKey  string
	Model   string
	Mode    string
	Quality string
	Store   ImageStore
	Client  *http.Client
}

func (g OpenAIImageGenerator) GenerateImage(ctx context.Context, request core.ImageGenerationRequest) (string, error) {
	ctx = withImageSession(ctx, request.SessionID)
	if strings.TrimSpace(g.BaseURL) == "" || strings.TrimSpace(g.APIKey) == "" || strings.TrimSpace(g.Model) == "" || g.Store == nil {
		return "", fmt.Errorf("openai image generator is not configured")
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	if strings.EqualFold(g.Mode, "chat") || strings.TrimSpace(request.ReferenceImage) != "" {
		return g.generateChat(ctx, client, request)
	}
	return g.generateImages(ctx, client, request)
}

func (g OpenAIImageGenerator) generateChat(ctx context.Context, client *http.Client, request core.ImageGenerationRequest) (string, error) {
	content := []any{map[string]any{"type": "text", "text": request.Prompt}}
	if request.ReferenceImage != "" {
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": request.ReferenceImage}})
	}
	body := map[string]any{
		"model":      g.Model,
		"messages":   []map[string]any{{"role": "user", "content": content}},
		"modalities": []string{"text", "image"},
		"size":       normalizeOpenAIImageSize(request.Size),
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := g.post(ctx, client, "/chat/completions", body, &response); err != nil {
		return "", err
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("image chat response has no choices")
	}
	mimeType, data, err := decodeImageContent(response.Choices[0].Message.Content)
	if err != nil {
		return "", err
	}
	return saveGeneratedImage(g.Store, ctx, mimeType, data)
}

// decodeImageContent 兼容字符串、内容数组和嵌套 image_url 中的 data URI。
func decodeImageContent(raw json.RawMessage) (string, []byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", nil, fmt.Errorf("decode image chat content: %w", err)
	}
	var find func(any) (string, []byte, bool)
	find = func(current any) (string, []byte, bool) {
		switch item := current.(type) {
		case string:
			mimeType, data, err := decodeDataURI(item)
			if err == nil {
				return mimeType, data, true
			}
		case []any:
			for _, child := range item {
				if mimeType, data, ok := find(child); ok {
					return mimeType, data, true
				}
			}
		case map[string]any:
			for _, child := range item {
				if mimeType, data, ok := find(child); ok {
					return mimeType, data, true
				}
			}
		}
		return "", nil, false
	}
	if mimeType, data, ok := find(value); ok {
		return mimeType, data, nil
	}
	preview := strings.TrimSpace(string(raw))
	if len(preview) > 240 {
		preview = preview[:240] + "..."
	}
	return "", nil, fmt.Errorf("image chat response has no data URI (content=%s)", preview)
}

func (g OpenAIImageGenerator) generateImages(ctx context.Context, client *http.Client, request core.ImageGenerationRequest) (string, error) {
	body := map[string]any{"model": g.Model, "prompt": request.Prompt, "size": normalizeOpenAIImageSize(request.Size)}
	if quality := strings.TrimSpace(g.Quality); quality != "" {
		body["quality"] = quality
	}
	var response struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := g.post(ctx, client, "/images/generations", body, &response); err != nil {
		return "", err
	}
	if len(response.Data) == 0 || strings.TrimSpace(response.Data[0].B64JSON) == "" {
		return "", fmt.Errorf("image generation response has no b64_json")
	}
	data, err := base64.StdEncoding.DecodeString(response.Data[0].B64JSON)
	if err != nil {
		return "", fmt.Errorf("decode generated image: %w", err)
	}
	return saveGeneratedImage(g.Store, ctx, "image/png", data)
}

func (g OpenAIImageGenerator) post(ctx context.Context, client *http.Client, path string, payload any, output any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode image request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send image request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("image API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode image response: %w", err)
	}
	return nil
}

func decodeDataURI(content string) (string, []byte, error) {
	start := strings.Index(content, "data:image/")
	if start < 0 {
		return "", nil, fmt.Errorf("image chat response has no data URI")
	}
	end := strings.IndexAny(content[start:], ") \t\r\n")
	if end < 0 {
		end = len(content) - start
	}
	uri := content[start : start+end]
	comma := strings.IndexByte(uri, ',')
	if comma < 0 {
		return "", nil, fmt.Errorf("invalid image data URI")
	}
	header, encoded := uri[:comma], uri[comma+1:]
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("decode image data URI: %w", err)
	}
	mimeType := strings.TrimPrefix(strings.SplitN(header, ";", 2)[0], "data:")
	return mimeType, data, nil
}

func normalizeOpenAIImageSize(size string) string {
	size = strings.TrimSpace(size)
	if size == "" {
		return "1920x800"
	}
	return strings.ReplaceAll(size, "*", "x")
}

var _ core.ImageGenerator = OpenAIImageGenerator{}
