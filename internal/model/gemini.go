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

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

const geminiGenerateContentPath = ":generateContent"

// Gemini 是 Google Gemini generateContent REST API 的适配器。
// 它将内部多轮消息映射为 contents，并把系统提示词放入 systemInstruction。
type Gemini struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func (g Gemini) Generate(ctx context.Context, request core.ModelRequest) (core.ModelEvent, error) {
	requestCtx, cancel := context.WithTimeout(ctx, defaultModelTimeout)
	defer cancel()
	payload := geminiRequest{Contents: toGeminiContents(request.Messages)}
	if system := geminiSystemInstruction(request.Messages); system != "" {
		payload.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: system}}}
	}
	if request.Temperature != nil || request.TopP != nil || request.MaxTokens > 0 {
		payload.GenerationConfig = &geminiGenerationConfig{
			Temperature:      request.Temperature,
			TopP:             request.TopP,
			MaxOutputTokens:  request.MaxTokens,
			ResponseMimeType: responseMimeType(request.ResponseFormat),
		}
	}
	var response geminiResponse
	if err := g.post(requestCtx, request.Model, payload, &response); err != nil {
		return core.ModelEvent{}, err
	}
	if len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
		return core.ModelEvent{}, errors.New("Gemini response has no content")
	}
	parts := response.Candidates[0].Content.Parts
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	if len(texts) == 0 {
		return core.ModelEvent{}, errors.New("Gemini response has no text content")
	}
	return core.ModelEvent{Type: "final", Text: strings.Join(texts, ""), Usage: &core.Usage{
		InputTokens: response.UsageMetadata.PromptTokenCount, OutputTokens: response.UsageMetadata.CandidatesTokenCount,
		ReasoningTokens: response.UsageMetadata.ThoughtsTokenCount, FinishReason: response.Candidates[0].FinishReason,
	}}, nil
}

func (g Gemini) Stream(ctx context.Context, request core.ModelRequest) (<-chan core.ModelEvent, error) {
	channel := make(chan core.ModelEvent, 2)
	go func() {
		defer close(channel)
		response, err := g.Generate(ctx, request)
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

func (g Gemini) post(ctx context.Context, modelName string, payload geminiRequest, output *geminiResponse) error {
	if g.BaseURL == "" || g.APIKey == "" {
		return errors.New("Gemini base URL or API key is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Gemini request: %w", err)
	}
	url := strings.TrimRight(g.BaseURL, "/") + "/" + modelName + geminiGenerateContentPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.APIKey)
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: defaultModelTimeout}
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send Gemini request: %w", err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxModelResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Gemini response: %w", err)
	}
	if len(content) > maxModelResponseBytes {
		return errors.New("Gemini response exceeds size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Gemini HTTP status %d", response.StatusCode)
	}
	if err := json.Unmarshal(content, output); err != nil {
		return fmt.Errorf("decode Gemini response: %w", err)
	}
	return nil
}

type geminiRequest struct {
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Contents          []geminiContent         `json:"contents"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature      *float32 `json:"temperature,omitempty"`
	TopP             *float32 `json:"topP,omitempty"`
	MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
	ResponseMimeType string   `json:"responseMimeType,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	FileData   *geminiFileData   `json:"fileData,omitempty"`
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
}
type geminiInlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}
type geminiFileData struct {
	MIMEType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
}

func responseMimeType(format *core.ResponseFormat) string {
	if format != nil && format.Type == core.ResponseFormatJSON {
		return "application/json"
	}
	return ""
}
func geminiSystemInstruction(messages []core.Message) string {
	parts := make([]string, 0)
	for _, message := range messages {
		if message.Role == "system" && message.Content != "" {
			parts = append(parts, message.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

func toGeminiContents(messages []core.Message) []geminiContent {
	result := make([]geminiContent, 0, len(messages))
	for _, message := range messages {
		if message.Role == "system" || (message.Content == "" && len(message.Images) == 0) {
			continue
		}
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		parts := make([]geminiPart, 0, len(message.Images)+1)
		if message.Content != "" {
			parts = append(parts, geminiPart{Text: message.Content})
		}
		for _, image := range message.Images {
			parts = append(parts, geminiPart{FileData: &geminiFileData{MIMEType: image.MIMEType, FileURI: image.URL}})
		}
		result = append(result, geminiContent{Role: role, Parts: parts})
	}
	return result
}
