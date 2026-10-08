package model

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

// UploadingImageGenerator 将模型返回的图片交给通用存储接口，返回持久化地址。
type UploadingImageGenerator struct {
	Generator core.ImageGenerator
	Store     ImageStore
	Client    *http.Client
}

func (g UploadingImageGenerator) GenerateImage(ctx context.Context, request core.ImageGenerationRequest) (string, error) {
	if g.Generator == nil || g.Store == nil {
		return "", fmt.Errorf("uploading image generator is not configured")
	}
	imageURL, err := g.Generator.GenerateImage(ctx, request)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(imageURL, "data:image/") {
		mimeType, data, decodeErr := decodeDataURI(imageURL)
		if decodeErr != nil {
			return "", decodeErr
		}
		return saveGeneratedImage(g.Store, ctx, mimeType, data)
	}
	// 某些图片模型只返回裸 base64；按 PNG/JPEG/WebP 常见头部推断 MIME 后上传。
	if data, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(imageURL)); decodeErr == nil && len(data) > 0 {
		mimeType := "image/png"
		if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
			mimeType = "image/jpeg"
		} else if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
			mimeType = "image/webp"
		}
		return saveGeneratedImage(g.Store, ctx, mimeType, data)
	}
	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("download generated image: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("download generated image returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return "", err
	}
	mimeType := response.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/png"
	}
	return saveGeneratedImage(g.Store, ctx, mimeType, data)
}

var _ core.ImageGenerator = UploadingImageGenerator{}
