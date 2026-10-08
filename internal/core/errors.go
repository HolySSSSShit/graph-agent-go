package core

import (
	"errors"
	"fmt"
)

// PublicError 携带可安全展示给客户端的错误提示，同时保留内部错误用于审计。
type PublicError struct {
	Message         string
	Code            string
	Details         map[string]any
	ExternalTraceID string
	Cause           error
}

func (e *PublicError) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Cause)
}

func (e *PublicError) Unwrap() error { return e.Cause }

// PublicErrorInfo 只提取明确标记为可公开的错误信息。
func PublicErrorInfo(err error) (message, code string, details map[string]any, ok bool) {
	var publicError *PublicError
	if !errors.As(err, &publicError) || publicError.Message == "" {
		return "", "", nil, false
	}
	return publicError.Message, publicError.Code, publicError.Details, true
}
