package core

import (
	"context"
	"strings"
)

type requestHostContextKey struct{}

func WithRequestHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, requestHostContextKey{}, NormalizeRequestHost(host))
}

func RequestHost(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	host, _ := ctx.Value(requestHostContextKey{}).(string)
	return host
}

func NormalizeRequestHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(host, "[") {
		if end := strings.LastIndex(host, "]"); end >= 0 {
			host = host[1:end] + host[end+1:]
		}
	}
	return strings.TrimSuffix(host, ".")
}
