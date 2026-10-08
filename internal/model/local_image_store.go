package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// LocalImageStore 将图片写入共享本地目录，并返回由 API 层暴露的 URL。
type LocalImageStore struct{ Directory, URLPrefix string }

type imageSessionContextKey struct{}

func withImageSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, imageSessionContextKey{}, strings.TrimSpace(sessionID))
}

func (s LocalImageStore) Save(ctx context.Context, mimeType string, data []byte) (string, error) {
	if len(data) == 0 || strings.TrimSpace(s.Directory) == "" || strings.TrimSpace(s.URLPrefix) == "" {
		return "", fmt.Errorf("invalid local image store input")
	}
	sessionID, _ := ctx.Value(imageSessionContextKey{}).(string)
	directory := s.Directory
	urlPrefix := strings.TrimRight(s.URLPrefix, "/")
	if sessionID != "" {
		directory = filepath.Join(directory, sessionID)
		urlPrefix += "/" + url.PathEscape(sessionID)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	ext := ".bin"
	if strings.Contains(strings.ToLower(mimeType), "png") {
		ext = ".png"
	} else if strings.Contains(strings.ToLower(mimeType), "jpeg") || strings.Contains(strings.ToLower(mimeType), "jpg") {
		ext = ".jpg"
	} else if strings.Contains(strings.ToLower(mimeType), "webp") {
		ext = ".webp"
	}
	name := hex.EncodeToString(id[:]) + ext
	if err := os.WriteFile(filepath.Join(directory, name), data, 0o644); err != nil {
		return "", err
	}
	return urlPrefix + "/" + name, nil
}

// ServeHTTP 仅按文件名提供生成目录中的图片，防止路径穿越。
func (s LocalImageStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sessionID := filepath.Base(r.PathValue("session"))
	name := filepath.Base(r.PathValue("name"))
	if name == "." || name == "" || name != r.PathValue("name") || sessionID != r.PathValue("session") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.Directory, sessionID, name))
}
