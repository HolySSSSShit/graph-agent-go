package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

const (
	openAICompatibleChatPath = "/chat/completions"
	defaultModelTimeout      = 120 * time.Second
	maxModelResponseBytes    = 8 << 20
)

// OpenAICompatible 是 DeepSeek、Qwen 等 OpenAI 兼容 Chat Completions API 的标准库适配器。
// 厂商差异必须限制在本适配器内，节点和编排器只依赖 core.Model。
type OpenAICompatible struct {
	BaseURL        string
	APIKey         string
	Client         *http.Client
	EnableThinking *bool
	ThinkingBudget int
}

func (c OpenAICompatible) Generate(ctx context.Context, request core.ModelRequest) (core.ModelEvent, error) {
	// 单次模型请求使用独立超时；超时只结束当前调用，不取消编排器的运行上下文，
	// 这样 reason 可以在 Flash 超时后继续尝试 Plus 或恢复逻辑。
	requestCtx, cancel := context.WithTimeout(ctx, defaultModelTimeout)
	defer cancel()
	payload := completionRequest{
		Model:          request.Model,
		Messages:       toCompletionMessages(request.Messages),
		Temperature:    request.Temperature,
		TopP:           request.TopP,
		MaxTokens:      request.MaxTokens,
		ResponseFormat: request.ResponseFormat,
		EnableThinking: c.EnableThinking,
		ThinkingBudget: c.ThinkingBudget,
	}
	if len(request.Tools) > 0 {
		payload.Tools = toCompletionTools(request.Tools)
	}
	var response completionResponse
	if err := c.post(requestCtx, payload, &response); err != nil {
		return core.ModelEvent{}, err
	}
	if len(response.Choices) == 0 {
		return core.ModelEvent{}, errors.New("model response has no choices")
	}
	choice := response.Choices[0].Message
	usage := toUsage(response.Usage)
	if usage != nil {
		usage.FinishReason = response.Choices[0].FinishReason
		usage.ThinkingEnabled = c.EnableThinking
		usage.ThinkingBudget = c.ThinkingBudget
	}
	if len(choice.ToolCalls) > 0 {
		call := choice.ToolCalls[0]
		arguments := map[string]any{}
		if call.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
				return core.ModelEvent{}, fmt.Errorf("decode model tool arguments: %w", err)
			}
		}
		return core.ModelEvent{Type: "tool_call", ToolName: call.Function.Name, ToolArgs: arguments, Usage: usage}, nil
	}
	text, contentTool, err := parseRecognizedContent(choice.Content)
	if err != nil {
		return core.ModelEvent{}, err
	}
	if contentTool != nil {
		return core.ModelEvent{Type: "tool_call", ToolName: contentTool.Name, ToolArgs: contentTool.Arguments, Usage: usage}, nil
	}
	if text != "" {
		return core.ModelEvent{Type: "final", Text: text, Usage: usage}, nil
	}
	return core.ModelEvent{}, errors.New("model response has no content or tool call")
}

type recognizedToolCall struct {
	Name      string
	Arguments map[string]any
}

// parseRecognizedContent 只保留内部协议认可的 text/tool_call，忽略供应商私有的 thinking 等片段。
func parseRecognizedContent(raw json.RawMessage) (string, *recognizedToolCall, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil, nil
	}
	var parts []struct {
		Type      string         `json:"type"`
		Text      string         `json:"text"`
		Name      string         `json:"name"`
		ToolName  string         `json:"tool_name"`
		Input     map[string]any `json:"input"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, fmt.Errorf("decode recognized model content: %w", err)
	}
	for _, part := range parts {
		switch part.Type {
		case "text":
			text += part.Text
		case "tool_call":
			name := part.Name
			if name == "" {
				name = part.ToolName
			}
			if name == "" {
				return "", nil, errors.New("recognized tool call has no tool name")
			}
			args := part.Arguments
			if args == nil {
				args = part.Input
			}
			if args == nil {
				args = map[string]any{}
			}
			return text, &recognizedToolCall{Name: name, Arguments: args}, nil
		}
	}
	return text, nil, nil
}

// Stream 保持统一流式接口。当前使用单次完成请求并转换为一个受控增量；真实 SSE 解码可在不影响节点接口的前提下替换。
func (c OpenAICompatible) Stream(ctx context.Context, request core.ModelRequest) (<-chan core.ModelEvent, error) {
	channel := make(chan core.ModelEvent, 2)
	go func() {
		defer close(channel)
		response, err := c.Generate(ctx, request)
		if err != nil {
			select {
			case channel <- core.ModelEvent{Type: "error", Text: err.Error()}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case channel <- core.ModelEvent{Type: "text_delta", Text: response.Text, Usage: response.Usage}:
		case <-ctx.Done():
			return
		}
		select {
		case channel <- core.ModelEvent{Type: "done", Usage: response.Usage}:
		case <-ctx.Done():
		}
	}()
	return channel, nil
}

func (c OpenAICompatible) post(ctx context.Context, payload completionRequest, output *completionResponse) error {
	if c.BaseURL == "" {
		return errors.New("model base URL is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode model request: %w", err)
	}
	url := strings.TrimRight(c.BaseURL, "/") + openAICompatibleChatPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: defaultModelTimeout}
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send model request: %w", err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxModelResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read model response: %w", err)
	}
	if len(content) > maxModelResponseBytes {
		return errors.New("model response exceeds size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(content))
		if len([]rune(message)) > 512 {
			message = string([]rune(message)[:512]) + "..."
		}
		if message == "" {
			return fmt.Errorf("model HTTP status %d", response.StatusCode)
		}
		return fmt.Errorf("model HTTP status %d: %s", response.StatusCode, message)
	}
	if err := json.Unmarshal(content, output); err != nil {
		return fmt.Errorf("decode model response: %w", err)
	}
	return nil
}

type completionRequest struct {
	Model          string               `json:"model"`
	Messages       []completionMessage  `json:"messages"`
	Tools          []completionTool     `json:"tools,omitempty"`
	Temperature    *float32             `json:"temperature,omitempty"`
	TopP           *float32             `json:"top_p,omitempty"`
	MaxTokens      int                  `json:"max_tokens,omitempty"`
	ResponseFormat *core.ResponseFormat `json:"response_format,omitempty"`
	EnableThinking *bool                `json:"enable_thinking,omitempty"`
	ThinkingBudget int                  `json:"thinking_budget,omitempty"`
}

type completionMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type completionTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type completionResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage completionUsage `json:"usage"`
}

type completionUsage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func toCompletionMessages(messages []core.Message) []completionMessage {
	result := make([]completionMessage, 0, len(messages))
	for _, message := range messages {
		role := message.Role
		if role == "tool" {
			role = "tool"
		}
		var content any = message.Content
		if len(message.Images) > 0 {
			parts := make([]map[string]any, 0, len(message.Images)+1)
			if message.Content != "" {
				parts = append(parts, map[string]any{"type": "text", "text": message.Content})
			}
			for _, image := range message.Images {
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image.URL}})
			}
			content = parts
		}
		result = append(result, completionMessage{Role: role, Content: content, Name: message.ToolName})
	}
	return result
}

func toCompletionTools(tools []core.Tool) []completionTool {
	result := make([]completionTool, 0, len(tools))
	for _, tool := range tools {
		item := completionTool{Type: "function"}
		item.Function.Name = tool.Name
		item.Function.Description = tool.Description
		item.Function.Parameters = tool.InputSchema
		result = append(result, item)
	}
	return result
}

func toUsage(usage completionUsage) *core.Usage {
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		return nil
	}
	return &core.Usage{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens, ReasoningTokens: usage.CompletionTokensDetails.ReasoningTokens}
}
