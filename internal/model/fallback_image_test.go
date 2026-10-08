package model

import (
	"context"
	"errors"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

type recordingImageGenerator struct {
	value string
	err   error
	errs  []error
	calls int
}

func (g *recordingImageGenerator) GenerateImage(context.Context, core.ImageGenerationRequest) (string, error) {
	g.calls++
	if len(g.errs) > 0 {
		index := g.calls - 1
		if index >= len(g.errs) {
			index = len(g.errs) - 1
		}
		if g.errs[index] != nil {
			return "", g.errs[index]
		}
		return g.value, nil
	}
	return g.value, g.err
}

func TestFallbackImageGeneratorUsesPrimaryResult(t *testing.T) {
	primary := &recordingImageGenerator{value: "primary"}
	fallback := &recordingImageGenerator{value: "fallback"}
	value, err := (FallbackImageGenerator{Primary: primary, Fallback: fallback}).GenerateImage(context.Background(), core.ImageGenerationRequest{})
	if err != nil || value != "primary" || primary.calls != 1 || fallback.calls != 0 {
		t.Fatalf("value=%q err=%v primary_calls=%d fallback_calls=%d", value, err, primary.calls, fallback.calls)
	}
}

func TestFallbackImageGeneratorFallsBackOnTimeout(t *testing.T) {
	primary := &recordingImageGenerator{err: context.DeadlineExceeded}
	fallback := &recordingImageGenerator{value: "fallback"}
	value, err := (FallbackImageGenerator{Primary: primary, Fallback: fallback}).GenerateImage(context.Background(), core.ImageGenerationRequest{})
	if err != nil || value != "fallback" || fallback.calls != 1 {
		t.Fatalf("value=%q err=%v fallback_calls=%d", value, err, fallback.calls)
	}
}

func TestFallbackImageGeneratorRetriesPrimaryBeforeFallback(t *testing.T) {
	primary := &recordingImageGenerator{errs: []error{errors.New("first"), errors.New("second")}}
	fallback := &recordingImageGenerator{value: "fallback"}
	value, err := (FallbackImageGenerator{Primary: primary, Fallback: fallback, Retries: 1}).GenerateImage(context.Background(), core.ImageGenerationRequest{})
	if err != nil || value != "fallback" || primary.calls != 2 || fallback.calls != 1 {
		t.Fatalf("value=%q err=%v primary_calls=%d fallback_calls=%d", value, err, primary.calls, fallback.calls)
	}
}

func TestFallbackImageGeneratorFallsBackOnOtherErrors(t *testing.T) {
	primaryErr := errors.New("provider rejected request")
	primary := &recordingImageGenerator{err: primaryErr}
	fallback := &recordingImageGenerator{value: "fallback"}
	value, err := (FallbackImageGenerator{Primary: primary, Fallback: fallback}).GenerateImage(context.Background(), core.ImageGenerationRequest{})
	if err != nil || value != "fallback" || fallback.calls != 1 {
		t.Fatalf("err=%v fallback_calls=%d", err, fallback.calls)
	}
}

func TestFallbackImageGeneratorFallsBackOnUploadError(t *testing.T) {
	primaryErr := &ImageError{Kind: ImageFailureUpload, Err: context.DeadlineExceeded}
	primary := &recordingImageGenerator{err: primaryErr}
	fallback := &recordingImageGenerator{value: "fallback"}
	value, err := (FallbackImageGenerator{Primary: primary, Fallback: fallback}).GenerateImage(context.Background(), core.ImageGenerationRequest{})
	if err != nil || value != "fallback" || fallback.calls != 1 {
		t.Fatalf("err=%v fallback_calls=%d", err, fallback.calls)
	}
}

func TestFallbackImageGeneratorReturnsFallbackError(t *testing.T) {
	fallbackErr := errors.New("gpt failed")
	primary := &recordingImageGenerator{err: context.DeadlineExceeded}
	fallback := &recordingImageGenerator{err: fallbackErr}
	_, err := (FallbackImageGenerator{Primary: primary, Fallback: fallback}).GenerateImage(context.Background(), core.ImageGenerationRequest{})
	if !errors.Is(err, fallbackErr) || fallback.calls != 1 {
		t.Fatalf("err=%v fallback_calls=%d", err, fallback.calls)
	}
}

func TestFallbackImageGeneratorHonorsCancelledParent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	primary := &recordingImageGenerator{err: context.DeadlineExceeded}
	fallback := &recordingImageGenerator{value: "fallback"}
	_, _ = (FallbackImageGenerator{Primary: primary, Fallback: fallback}).GenerateImage(ctx, core.ImageGenerationRequest{})
	if fallback.calls != 0 {
		t.Fatalf("fallback_calls=%d, want 0", fallback.calls)
	}
}
