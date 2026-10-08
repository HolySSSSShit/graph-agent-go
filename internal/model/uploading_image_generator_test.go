package model

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

type fixedImageGenerator struct{ value string }

func (g fixedImageGenerator) GenerateImage(context.Context, core.ImageGenerationRequest) (string, error) {
	return g.value, nil
}

type recordingImageStore struct {
	mime string
	data []byte
}

func (s *recordingImageStore) Save(_ context.Context, mime string, data []byte) (string, error) {
	s.mime = mime
	s.data = append([]byte(nil), data...)
	return "https://oss.example/image.png", nil
}

func TestUploadingImageGeneratorUploadsBareBase64(t *testing.T) {
	store := &recordingImageStore{}
	encoded := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	generator := UploadingImageGenerator{Generator: fixedImageGenerator{value: encoded}, Store: store}
	got, err := generator.GenerateImage(context.Background(), core.ImageGenerationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://oss.example/image.png" || store.mime != "image/png" || string(store.data) != "png-bytes" {
		t.Fatalf("unexpected upload: url=%q mime=%q data=%q", got, store.mime, store.data)
	}
}
