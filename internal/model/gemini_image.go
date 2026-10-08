package model

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

// ImageStore 保存模型返回的图片并返回可访问地址。
type ImageStore interface {
	Save(context.Context, string, []byte) (string, error)
}

// GeminiImageGenerator 适配 Gemini 图片模型（Nano Banana 系列）的 inlineData 输出。
// 图片字节交由 ImageStore 保存，Workflow 只接收最终 URL。
type GeminiImageGenerator struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
	Store   ImageStore
}

func (g GeminiImageGenerator) GenerateImage(ctx context.Context, request core.ImageGenerationRequest) (string, error) {
	ctx = withImageSession(ctx, request.SessionID)
	if g.Store == nil {
		return "", errors.New("Gemini image generator storage is not configured")
	}
	payload := geminiRequest{Contents: []geminiContent{{Role: "user", Parts: []geminiPart{{Text: request.Prompt}}}}}
	var response geminiResponse
	client := Gemini{BaseURL: g.BaseURL, APIKey: g.APIKey, Client: g.Client}
	if err := client.post(ctx, g.Model, payload, &response); err != nil {
		return "", err
	}
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || strings.TrimSpace(part.InlineData.Data) == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				return "", fmt.Errorf("decode Gemini image: %w", err)
			}
			mimeType := part.InlineData.MIMEType
			if mimeType == "" {
				mimeType = "image/png"
			}
			return saveGeneratedImage(g.Store, ctx, mimeType, data)
		}
	}
	return "", errors.New("Gemini image response has no inline image data")
}

var _ core.ImageGenerator = GeminiImageGenerator{}
