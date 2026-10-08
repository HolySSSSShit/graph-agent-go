package model

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

// FallbackImageGenerator 主图片生成失败后按次数重试，仍失败时切换备用生成器。
type FallbackImageGenerator struct {
	Primary  core.ImageGenerator
	Fallback core.ImageGenerator
	Retries  int
}

func (g FallbackImageGenerator) GenerateImage(ctx context.Context, request core.ImageGenerationRequest) (string, error) {
	if g.Primary == nil {
		return "", fmt.Errorf("primary image generator is not configured")
	}
	var err error
	for attempt := 0; attempt <= max(g.Retries, 0); attempt++ {
		value, generateErr := g.Primary.GenerateImage(ctx, request)
		if generateErr == nil {
			return value, nil
		}
		err = generateErr
		if ctx.Err() != nil {
			return "", err
		}
	}
	if g.Fallback == nil {
		return "", err
	}
	value, fallbackErr := g.Fallback.GenerateImage(ctx, request)
	if fallbackErr != nil {
		return "", fmt.Errorf("fallback image generation failed after primary retries: %w", fallbackErr)
	}
	return value, nil
}

// IsImageTimeout 判断图片请求是否因截止时间或网络超时失败。
func IsImageTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netError net.Error
	return errors.As(err, &netError) && netError.Timeout()
}

var _ core.ImageGenerator = FallbackImageGenerator{}
