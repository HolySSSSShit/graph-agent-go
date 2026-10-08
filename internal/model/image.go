package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// QwenImageGenerator 是公共 OpenAI 兼容生图服务适配器，供各 Workflow 复用。
type QwenImageGenerator struct {
	BaseURL          string
	APIKey           string
	Model            string
	DashScope        bool
	Client           *http.Client
	MaxResponseBytes int64
}

func (g QwenImageGenerator) GenerateImage(ctx context.Context, request core.ImageGenerationRequest) (string, error) {
	if strings.TrimSpace(g.BaseURL) == "" || strings.TrimSpace(g.APIKey) == "" || strings.TrimSpace(g.Model) == "" {
		return "", fmt.Errorf("qwen image generator is not configured")
	}
	var payload any = map[string]any{"model": g.Model, "prompt": request.Prompt, "size": request.Size, "n": 1}
	if g.DashScope {
		payload = map[string]any{
			"model": g.Model,
			"input": map[string]any{"messages": []any{map[string]any{
				"role":    "user",
				"content": []any{map[string]string{"text": request.Prompt}},
			}}},
			"parameters": map[string]any{"prompt_extend": true, "size": request.Size},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode image request: %w", err)
	}
	endpoint := strings.TrimRight(g.BaseURL, "/") + "/images/generations"
	if g.DashScope {
		endpoint = g.BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.APIKey)
	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	limit := g.MaxResponseBytes
	if limit <= 0 {
		limit = 4 << 20
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > limit {
		return "", fmt.Errorf("image response exceeds %d bytes", limit)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("image API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var result struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if g.DashScope {
		var dash struct {
			Output struct {
				Choices []struct {
					Message struct {
						Content []struct {
							Image string `json:"image"`
							URL   string `json:"url"`
						} `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			} `json:"output"`
		}
		if err := json.Unmarshal(data, &dash); err != nil {
			return "", err
		}
		if len(dash.Output.Choices) > 0 && len(dash.Output.Choices[0].Message.Content) > 0 {
			item := dash.Output.Choices[0].Message.Content[0]
			if item.Image != "" {
				return item.Image, nil
			}
			if item.URL != "" {
				return item.URL, nil
			}
		}
		return "", fmt.Errorf("image API returned no URL")
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if len(result.Data) == 0 {
		return "", fmt.Errorf("image API returned no URL")
	}
	if strings.TrimSpace(result.Data[0].URL) == "" && strings.TrimSpace(result.Data[0].B64JSON) != "" {
		return "data:image/png;base64," + strings.TrimSpace(result.Data[0].B64JSON), nil
	}
	if strings.TrimSpace(result.Data[0].URL) == "" {
		return "", fmt.Errorf("image API returned no URL")
	}
	return result.Data[0].URL, nil
}

var _ core.ImageGenerator = QwenImageGenerator{}

// StaticImageGenerator 用于本地联调，按调用顺序循环返回预置图片 URL。
// 它不访问网络，确认图片节点、JSON 替换和后续输出流程时使用。
type StaticImageGenerator struct {
	URLs  []string
	Index int
}

func (g *StaticImageGenerator) GenerateImage(_ context.Context, _ core.ImageGenerationRequest) (string, error) {
	if g == nil || len(g.URLs) == 0 {
		return "", fmt.Errorf("static image generator has no URLs")
	}
	url := g.URLs[g.Index%len(g.URLs)]
	g.Index++
	return url, nil
}

var _ core.ImageGenerator = (*StaticImageGenerator)(nil)
