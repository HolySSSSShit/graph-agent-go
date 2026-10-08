package model

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

type failingImageStore struct{ err error }

func (s failingImageStore) Save(context.Context, string, []byte) (string, error) {
	return "", s.err
}

type compressionImageStore struct {
	mime string
	data []byte
}

func (s *compressionImageStore) Save(_ context.Context, mime string, data []byte) (string, error) {
	s.mime, s.data = mime, append([]byte(nil), data...)
	return "ok", nil
}

func TestSaveGeneratedImageClassifiesUploadFailure(t *testing.T) {
	storeErr := errors.New("sign rejected")
	_, err := saveGeneratedImage(failingImageStore{err: storeErr}, context.Background(), "image/png", []byte("image"))
	if !errors.Is(err, storeErr) || ClassifyImageFailure(err) != ImageFailureUpload {
		t.Fatalf("err=%v kind=%s", err, ClassifyImageFailure(err))
	}
}

func TestUploadTimeoutRemainsUploadFailure(t *testing.T) {
	_, err := saveGeneratedImage(failingImageStore{err: context.DeadlineExceeded}, context.Background(), "image/png", []byte("image"))
	if ClassifyImageFailure(err) != ImageFailureUpload {
		t.Fatalf("kind=%s, want upload", ClassifyImageFailure(err))
	}
}

func TestSaveGeneratedImageCompressesOpaqueLargePNG(t *testing.T) {
	imageValue := image.NewRGBA(image.Rect(0, 0, 512, 512))
	seed := uint32(0x12345678)
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			seed = seed*1664525 + 1013904223
			imageValue.Set(x, y, color.RGBA{R: uint8(seed >> 24), G: uint8(seed >> 16), B: uint8(seed >> 8), A: 255})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, imageValue); err != nil {
		t.Fatal(err)
	}
	store := &compressionImageStore{}
	if _, err := saveGeneratedImage(store, context.Background(), "image/png", input.Bytes()); err != nil {
		t.Fatal(err)
	}
	if store.mime != "image/jpeg" || len(store.data) >= input.Len() {
		t.Fatalf("mime=%q compressed=%d original=%d", store.mime, len(store.data), input.Len())
	}
}

func TestSaveGeneratedImageDetectsMissingMIME(t *testing.T) {
	imageValue := image.NewRGBA(image.Rect(0, 0, 512, 512))
	seed := uint32(0x87654321)
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			seed = seed*1664525 + 1013904223
			imageValue.Set(x, y, color.RGBA{R: uint8(seed >> 24), G: uint8(seed >> 16), B: uint8(seed >> 8), A: 255})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, imageValue); err != nil {
		t.Fatal(err)
	}
	store := &compressionImageStore{}
	if _, err := saveGeneratedImage(store, context.Background(), "", input.Bytes()); err != nil {
		t.Fatal(err)
	}
	if store.mime != "image/jpeg" {
		t.Fatalf("mime=%q, want image/jpeg", store.mime)
	}
}

func TestSaveGeneratedImageKeepsSmallPNG(t *testing.T) {
	imageValue := image.NewRGBA(image.Rect(0, 0, 48, 48))
	var input bytes.Buffer
	if err := png.Encode(&input, imageValue); err != nil {
		t.Fatal(err)
	}
	store := &compressionImageStore{}
	if _, err := saveGeneratedImage(store, context.Background(), "image/png", input.Bytes()); err != nil {
		t.Fatal(err)
	}
	if store.mime != "image/png" {
		t.Fatalf("mime=%q, want image/png", store.mime)
	}
}
