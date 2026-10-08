package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"
)

type ImageFailureKind string

const (
	ImageFailureTimeout ImageFailureKind = "timeout"
	ImageFailureUpload  ImageFailureKind = "upload"
	ImageFailureService ImageFailureKind = "service"
)

type ImageError struct {
	Kind ImageFailureKind
	Err  error
}

func (e *ImageError) Error() string { return fmt.Sprintf("image %s failed: %v", e.Kind, e.Err) }
func (e *ImageError) Unwrap() error { return e.Err }

// ClassifyImageFailure 将底层错误归并为可对外统计的稳定类别。
func ClassifyImageFailure(err error) ImageFailureKind {
	var imageError *ImageError
	if errors.As(err, &imageError) && imageError.Kind != "" {
		return imageError.Kind
	}
	if IsImageTimeout(err) {
		return ImageFailureTimeout
	}
	return ImageFailureService
}

func saveGeneratedImage(store ImageStore, ctx context.Context, mimeType string, data []byte) (string, error) {
	compressedType, compressed := compressGeneratedImage(mimeType, data)
	value, err := store.Save(ctx, compressedType, compressed)
	if err != nil {
		return "", &ImageError{Kind: ImageFailureUpload, Err: err}
	}
	return value, nil
}

const (
	imageCompressionMinWidth  = 256
	imageCompressionMinHeight = 256
	imageJPEGQuality          = 82
)

// compressGeneratedImage 将大尺寸且不含透明通道的 PNG 转为 JPEG，保留小图标和透明图。
// 如果压缩后的结果没有变小，则继续使用原始数据。
func compressGeneratedImage(mimeType string, data []byte) (string, []byte) {
	if len(data) == 0 {
		return mimeType, data
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = http.DetectContentType(data)
	}
	if !strings.Contains(strings.ToLower(mimeType), "png") {
		return mimeType, data
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return mimeType, data
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	opaque := imageIsOpaque(decoded)
	if width < imageCompressionMinWidth || height < imageCompressionMinHeight {
		return mimeType, data
	}
	if !opaque {
		return mimeType, data
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, decoded, &jpeg.Options{Quality: imageJPEGQuality}); err != nil || output.Len() >= len(data) {
		return mimeType, data
	}
	return "image/jpeg", output.Bytes()
}

func imageIsOpaque(value image.Image) bool {
	for y := value.Bounds().Min.Y; y < value.Bounds().Max.Y; y++ {
		for x := value.Bounds().Min.X; x < value.Bounds().Max.X; x++ {
			_, _, _, alpha := value.At(x, y).RGBA()
			if alpha != 0xffff {
				return false
			}
		}
	}
	return true
}
