package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

const defaultProtocolVersion = "2025-06-18"

// HTTPSource 是 mcp-go-server 的 Streamable HTTP JSON-RPC 适配器。
type HTTPSource struct {
	BaseURL          string
	Token            string
	ProtocolVersion  string
	Client           *http.Client
	MaxResponseBytes int64
	ShadowDirectory  string
	// TokenResolver 允许按请求身份选择当前商户的 MCP Token；为空时使用 Token。
	TokenResolver func(context.Context, core.ToolScope) (string, error)

	initializeOnce sync.Once
	initializeErr  error
	requestID      atomic.Int64
	shadow         *shadowStorage
}

func NewHTTPSource(baseURL, token, protocolVersion string, client *http.Client, maxResponseBytes int64, shadowDirectory ...string) *HTTPSource {
	if protocolVersion == "" {
		protocolVersion = defaultProtocolVersion
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if maxResponseBytes <= 0 {
		maxResponseBytes = 1 << 20
	}
	directory := ""
	if len(shadowDirectory) > 0 {
		directory = shadowDirectory[0]
	}
	return &HTTPSource{
		BaseURL:          baseURL,
		Token:            token,
		ProtocolVersion:  protocolVersion,
		Client:           client,
		MaxResponseBytes: maxResponseBytes,
		ShadowDirectory:  directory,
		shadow:           newShadowStorage(directory),
	}
}

func (*HTTPSource) Name() string { return "mcp" }

func (s *HTTPSource) ListTools(ctx context.Context, scope core.ToolScope) ([]core.Tool, error) {
	token, err := s.resolveToken(ctx, scope)
	if err != nil {
		return nil, err
	}
	if err := s.initialize(ctx, token); err != nil {
		return nil, err
	}
	var result struct {
		Tools []core.Tool `json:"tools"`
	}
	if err := s.request(ctx, token, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	if result.Tools == nil {
		result.Tools = []core.Tool{}
	}
	for index := range result.Tools {
		result.Tools[index] = describeResultHandle(result.Tools[index])
	}
	return result.Tools, nil
}

// describeResultHandle 让规划器明确不透明结果引用。
// 仅当 MCP Schema 声明标准句柄字段时生效，不假设任何业务字段。
func describeResultHandle(tool core.Tool) core.Tool {
	properties, _ := tool.InputSchema["properties"].(map[string]any)
	if properties == nil {
		return tool
	}
	if value, ok := properties["handle_id"].(map[string]any); ok {
		value["description"] = "上游工具返回结果中的原始句柄；必须使用结果里的真实句柄值，不要填写步骤 ID、工具名或自造字符串。"
		if tool.Description != "" {
			tool.Description += " 若参数包含 handle_id，必须传入上游结果返回的真实句柄，不得使用步骤 ID。"
		}
	}
	return tool
}

func (s *HTTPSource) CallTool(ctx context.Context, scope core.ToolScope, name string, arguments map[string]any) (core.ToolResult, error) {
	if name == "inspect_handle_data" {
		return s.inspectHandle(arguments), nil
	}
	token, err := s.resolveToken(ctx, scope)
	if err != nil {
		return core.ToolResult{ToolName: name}, err
	}
	if err := s.initialize(ctx, token); err != nil {
		return core.ToolResult{}, err
	}
	var result toolCallResult
	if err := s.request(ctx, token, "tools/call", map[string]any{"name": name, "arguments": arguments}, &result); err != nil {
		return core.ToolResult{ToolName: name}, err
	}
	data := result.StructuredContent
	if data == nil {
		data = contentData(result.Content)
	}
	if s.shadow == nil {
		s.shadow = newShadowStorage(s.ShadowDirectory)
	}
	handle := s.shadow.put(data, core.ResultSource{ToolName: name})
	previewData := preview(handle, core.ToolStatusSuccess, data)
	if result.IsError {
		message := contentText(result.Content)
		dataPreview := preview(handle, core.ToolStatusBusinessError, data)
		annotateErrorPreview(dataPreview, message)
		return core.ToolResult{
			ToolName: name,
			Data:     dataPreview,
			Status:   "business_error",
			Error:    message,
		}, nil
	}
	if code, message, ok := businessError(data); ok {
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			dataPreview := preview(handle, core.ToolStatusPermissionDenied, data)
			annotateErrorPreview(dataPreview, message)
			return core.ToolResult{ToolName: name, Data: dataPreview, Status: core.ToolStatusPermissionDenied, Error: message}, fmt.Errorf("%w: %s", core.ErrPermissionDenied, message)
		}
		dataPreview := preview(handle, core.ToolStatusBusinessError, data)
		annotateErrorPreview(dataPreview, message)
		return core.ToolResult{ToolName: name, Data: dataPreview, Status: core.ToolStatusBusinessError, Error: message}, nil
	}
	return core.ToolResult{ToolName: name, Data: previewData, Status: core.ToolStatusSuccess, Source: &core.ResultSource{ToolName: name, Reference: handle}}, nil
}

func annotateErrorPreview(preview map[string]any, message string) {
	if message != "" {
		preview["error"] = message
	}
}

func (s *HTTPSource) inspectHandle(arguments map[string]any) core.ToolResult {
	handle, _ := arguments["handle_id"].(string)
	path, _ := arguments["json_path"].(string)
	data, source, ok := s.shadow.get(handle)
	if !ok {
		return core.ToolResult{ToolName: "inspect_handle_data", Status: core.ToolStatusFailed, Error: "句柄不存在或已过期"}
	}
	selected, ok, err := inspectPathChecked(data, path)
	if err != nil {
		return core.ToolResult{ToolName: "inspect_handle_data", Status: core.ToolStatusFailed, Error: err.Error()}
	}
	if !ok {
		return core.ToolResult{ToolName: "inspect_handle_data", Status: core.ToolStatusFailed, Error: "JSONPath 未命中；请依据原始业务结果结构调整路径，不要重复相同或等价查询"}
	}
	// JSONPath 命中通配集合时，即使只有一条记录也保留数组根形状。
	// 证据编译需要据此把字段映射回原始集合下标，否则单条命中会错误生成
	// $.current_data.values[0] 这类无法在原始结果中复核的路径。
	if strings.HasSuffix(path, "[*]") {
		if _, isCollection := selected.([]any); !isCollection {
			selected = []any{selected}
		}
	}
	source.Path = path
	return core.ToolResult{ToolName: "inspect_handle_data", Status: core.ToolStatusSuccess, Data: inspectValue(selected), Source: &source}
}

// VerifyEvidence 重新读取不透明结果引用，但不把 inspect 投影当成新来源。
// 该方法供确定性 review 使用。
func (s *HTTPSource) VerifyEvidence(_ context.Context, source core.ResultSource, path string) (any, error) {
	data, original, ok := s.shadow.get(source.Reference)
	if !ok || original.ToolName != source.ToolName {
		return nil, fmt.Errorf("evidence source is unavailable")
	}
	value, matched, err := inspectPathChecked(data, path)
	// preview 是 MCP 返回的展示包装，不属于原始句柄路径。证据编译可能
	// 暂时记录为 $.preview.<原始路径>，回源复核时只移除这一层包装。
	if (!matched || err != nil) && strings.HasPrefix(path, "$.preview") {
		originalPath := strings.TrimPrefix(path, "$.preview")
		if originalPath == "" {
			originalPath = "$"
		} else if !strings.HasPrefix(originalPath, "$") {
			originalPath = "$" + originalPath
		}
		value, matched, err = inspectPathChecked(data, originalPath)
	}
	if err != nil {
		return nil, err
	}
	if !matched {
		return nil, fmt.Errorf("evidence path did not match")
	}
	return value, nil
}

var _ core.EvidenceVerifier = (*HTTPSource)(nil)

func (s *HTTPSource) resolveToken(ctx context.Context, scope core.ToolScope) (string, error) {
	if s.TokenResolver != nil {
		return s.TokenResolver(ctx, scope)
	}
	return s.Token, nil
}

func (s *HTTPSource) initialize(ctx context.Context, token string) error {
	s.initializeOnce.Do(func() {
		var result struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		params := map[string]any{
			"protocolVersion": s.ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]string{"name": "agent", "version": "v0.1"},
		}
		if err := s.request(ctx, token, "initialize", params, &result); err != nil {
			s.initializeErr = err
			return
		}
		if result.ProtocolVersion != "" {
			// 当前客户端只使用稳定的 tools 子集，接受服务端协商的版本。
			s.ProtocolVersion = result.ProtocolVersion
		}
		s.initializeErr = s.notification(ctx, token, "notifications/initialized", map[string]any{})
	})
	return s.initializeErr
}

func (s *HTTPSource) request(ctx context.Context, token, method string, params any, result any) error {
	id := s.requestID.Add(1)
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode MCP %s request: %w", method, err)
	}
	responseBody, status, err := s.do(ctx, token, body)
	if err != nil {
		log.Printf("MCP request failed method=%s status=%d error_type=%T error=%v", method, status, err, err)
		return err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("%w: MCP HTTP status %d", core.ErrPermissionDenied, status)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("MCP HTTP status %d", status)
	}
	var response rpcResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return fmt.Errorf("decode MCP %s response: %w", method, err)
	}
	if response.JSONRPC != "2.0" {
		log.Printf("MCP response invalid method=%s status=%d body=%s", method, status, compactLogPayload(responseBody))
		return errors.New("invalid MCP JSON-RPC version")
	}
	if response.Error != nil {
		log.Printf("MCP RPC error method=%s status=%d code=%d message=%s body=%s", method, status, response.Error.Code, response.Error.Message, compactLogPayload(responseBody))
		return classifyRPCError(response.Error.Message)
	}
	if result == nil {
		return nil
	}
	if len(response.Result) == 0 {
		log.Printf("MCP response missing result method=%s status=%d body=%s", method, status, compactLogPayload(responseBody))
		return errors.New("MCP response has no result")
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		log.Printf("MCP result decode failed method=%s status=%d error=%v body=%s", method, status, err, compactLogPayload(responseBody))
		return fmt.Errorf("decode MCP %s result: %w", method, err)
	}
	return nil
}

func compactLogPayload(payload []byte) string {
	const maxLogBytes = 4096
	if len(payload) > maxLogBytes {
		payload = append(append([]byte(nil), payload[:maxLogBytes]...), []byte("...[truncated]")...)
	}
	return string(payload)
}

func (s *HTTPSource) notification(ctx context.Context, token, method string, params any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode MCP %s notification: %w", method, err)
	}
	_, status, err := s.do(ctx, token, body)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("%w: MCP HTTP status %d", core.ErrPermissionDenied, status)
	}
	if status != http.StatusAccepted && (status < http.StatusOK || status >= http.StatusMultipleChoices) {
		return fmt.Errorf("MCP notification HTTP status %d", status)
	}
	return nil
}

func (s *HTTPSource) do(ctx context.Context, token string, body []byte) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build MCP request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", s.ProtocolVersion)
	if token != "" {
		request.Header.Set("Mcp-token", token)
	}
	response, err := s.Client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("send MCP request: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(response.Body)
	reader := io.LimitReader(response.Body, s.MaxResponseBytes+1)
	responseBody, err := io.ReadAll(reader)
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read MCP response: %w", err)
	}
	if int64(len(responseBody)) > s.MaxResponseBytes {
		return nil, response.StatusCode, errors.New("MCP response exceeds configured size limit")
	}
	return responseBody, response.StatusCode, nil
}

func classifyRPCError(message string) error {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "permission") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "mcp-token") {
		return fmt.Errorf("%w: %s", core.ErrPermissionDenied, message)
	}
	return errors.New(message)
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCallResult struct {
	Content           []contentItem `json:"content"`
	StructuredContent any           `json:"structuredContent"`
	IsError           bool          `json:"isError"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func contentData(content []contentItem) any {
	items := make([]any, 0, len(content))
	for _, item := range content {
		items = append(items, decodeContentText(item.Text))
	}
	if len(items) == 1 {
		return items[0]
	}
	return items
}

func decodeContentText(text string) any {
	cleaned := strings.TrimSpace(text)
	if strings.HasPrefix(cleaned, "```") {
		newline := strings.IndexByte(cleaned, '\n')
		if newline >= 0 {
			cleaned = strings.TrimSpace(cleaned[newline+1:])
		}
		if strings.HasSuffix(cleaned, "```") {
			cleaned = strings.TrimSpace(strings.TrimSuffix(cleaned, "```"))
		}
	}
	var value any
	if json.Unmarshal([]byte(cleaned), &value) == nil {
		return value
	}
	return text
}

func contentText(content []contentItem) string {
	texts := make([]string, 0, len(content))
	for _, item := range content {
		texts = append(texts, item.Text)
	}
	return strings.Join(texts, "\n")
}

// businessError 兼容部分 MCP 服务将 OpenAPI 业务码直接放在成功内容中的响应。
// 正常数据没有 code 字段，或 code 为 0；非零 code 必须视为业务失败。
func businessError(data any) (int, string, bool) {
	object, ok := data.(map[string]any)
	if !ok {
		return 0, "", false
	}
	value, exists := object["code"]
	if !exists {
		return 0, "", false
	}
	code, ok := numericCode(value)
	if !ok || code == 0 {
		return 0, "", false
	}
	message, _ := object["msg"].(string)
	if message == "" {
		message, _ = object["message"].(string)
	}
	if message == "" {
		message = fmt.Sprintf("MCP business error code %d", code)
	}
	return code, message, true
}

func numericCode(value any) (int, bool) {
	switch current := value.(type) {
	case int:
		return current, true
	case int64:
		return int(current), true
	case float64:
		return int(current), current == float64(int(current))
	case string:
		var code int
		if _, err := fmt.Sscan(current, &code); err == nil {
			return code, true
		}
	}
	return 0, false
}
