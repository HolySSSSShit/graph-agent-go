package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"
	"github.com/HolySSSSShit/graph-agent-go/internal/session"
	"github.com/HolySSSSShit/graph-agent-go/internal/testkit"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCoreWithoutWorkflowReturnsServiceUnavailable(t *testing.T) {
	handler := (&Handler{Sessions: session.New()}).Routes()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/chat", strings.NewReader(`{"message":"hello"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "public chat workflow is not configured") {
		t.Fatalf("unconfigured workflow response: %d %s", response.Code, response.Body.String())
	}
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}
}

func TestRunEventSourceSSE(t *testing.T) {
	runner, sessions := newTestRunner(t)
	server := httptest.NewServer((&Handler{Runner: runner, Sessions: sessions, PublicChatWorkflow: "test"}).Routes())
	defer server.Close()
	accepted := createRun(t, server.URL, "s1")
	eventsRequest, err := http.NewRequest(http.MethodGet, server.URL+accepted.EventsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	eventsResponse, err := http.DefaultClient.Do(eventsRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer eventsResponse.Body.Close()
	if eventsResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected events status: %d", eventsResponse.StatusCode)
	}
	body, err := io.ReadAll(eventsResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, event := range []string{"event: progress", "event: message.completed"} {
		if !strings.Contains(text, event) {
			t.Fatalf("missing %s in %s", event, text)
		}
	}
	if !strings.Contains(text, "测试运行完成") {
		t.Fatalf("missing final answer: %s", text)
	}
	if strings.Contains(text, "event: tool.result") || strings.Contains(text, "正在整理查询结果") {
		t.Fatalf("不应向前端发送结果整理事件：%s", text)
	}
}

func TestRunRejectsUnknownWorkflow(t *testing.T) {
	runner, sessions := newTestRunner(t)
	server := httptest.NewServer((&Handler{Runner: runner, Sessions: sessions, PublicChatWorkflow: "test"}).Routes())
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/agent/chat", strings.NewReader(`{"session_id":"workflow-session","workflow":"missing","message":"sales"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown workflow status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestRunAliasIsNotExposed(t *testing.T) {
	runner, sessions := newTestRunner(t)
	server := httptest.NewServer((&Handler{Runner: runner, Sessions: sessions, PublicChatWorkflow: "test"}).Routes())
	defer server.Close()

	response, err := http.Post(server.URL+"/api/agent/runs", "application/json", strings.NewReader(`{"workflow":"test","message":"sales"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("run alias status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

func TestWorkflowProtocolIsNotExposed(t *testing.T) {
	server := httptest.NewServer((&Handler{}).Routes())
	defer server.Close()

	response, err := http.Get(server.URL + "/api/agent/workflows/test/protocol")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want=%d", response.StatusCode, http.StatusNotFound)
	}
}

func TestChatRequiresExplicitPublicWorkflow(t *testing.T) {
	runner, sessions := newTestRunner(t)
	server := httptest.NewServer((&Handler{Runner: runner, Sessions: sessions, PublicChatWorkflow: "test"}).Routes())
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/agent/chat", strings.NewReader(`{"message":"sales"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing workflow status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestRunEventSourceLastEventID(t *testing.T) {
	runner, sessions := newTestRunner(t)
	server := httptest.NewServer((&Handler{Runner: runner, Sessions: sessions, PublicChatWorkflow: "test"}).Routes())
	defer server.Close()
	accepted := createRun(t, server.URL, "s2")
	eventsRequest, err := http.NewRequest(http.MethodGet, server.URL+accepted.EventsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	eventsRequest.Header.Set("Last-Event-ID", "2")
	eventsResponse, err := http.DefaultClient.Do(eventsRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer eventsResponse.Body.Close()
	body, err := io.ReadAll(eventsResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "id: 1\n") || strings.Contains(text, "id: 2\n") {
		t.Fatalf("events before Last-Event-ID were replayed: %s", text)
	}
	if !strings.Contains(text, "event: message.completed") {
		t.Fatalf("missing completed event after replay: %s", text)
	}
}

func TestDeleteSession(t *testing.T) {
	runner, sessions := newTestRunner(t)
	tools := &toolSessionInvalidator{}
	server := httptest.NewServer((&Handler{Runner: runner, Sessions: sessions, PublicChatWorkflow: "test", ToolSessions: tools}).Routes())
	defer server.Close()
	identity := core.Identity{TenantID: "local", UserID: "f6e363ee-263b-4e4e-a68c-60e82564b190"}
	if _, err := sessions.Get(context.Background(), identity, "delete-me"); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/agent/sessions/delete-me", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected delete status: %d", response.StatusCode)
	}
	listed, err := sessions.List(context.Background(), identity)
	if err != nil || len(listed) != 0 {
		t.Fatalf("deleted session remains: %+v, %v", listed, err)
	}
	if tools.scope.TenantID != identity.TenantID || tools.scope.UserID != identity.UserID || tools.scope.SessionID != "delete-me" {
		t.Fatalf("工具目录未按完整身份失效：%+v", tools.scope)
	}
	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/agent/sessions/delete-me", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected missing delete status: %d", response.StatusCode)
	}
}

type toolSessionInvalidator struct{ scope core.ToolScope }

func (i *toolSessionInvalidator) Invalidate(scope core.ToolScope) { i.scope = scope }

type headerIdentityProvider struct {
	token string
}

type unavailableIdentityProvider struct{}

func (unavailableIdentityProvider) Resolve(context.Context, any) (core.Identity, error) {
	return core.Identity{}, errors.New("identity upstream timeout")
}

func (p *headerIdentityProvider) Resolve(_ context.Context, credential any) (core.Identity, error) {
	value, _ := credential.(string)
	p.token = value
	if value != "header-token" {
		return core.Identity{}, core.ErrPermissionDenied
	}
	return core.Identity{TenantID: "tenant-1", UserID: "user-1"}, nil
}

func TestTokenMustBeProvidedInHeader(t *testing.T) {
	runner, sessions := newTestRunner(t)
	provider := &headerIdentityProvider{}
	server := httptest.NewServer((&Handler{Runner: runner, Sessions: sessions, IdentityProvider: provider, PublicChatWorkflow: "test"}).Routes())
	defer server.Close()

	queryRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/agent/sessions?token=header-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	queryResponse, err := http.DefaultClient.Do(queryRequest)
	if err != nil {
		t.Fatal(err)
	}
	queryResponse.Body.Close()
	if queryResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query token must be rejected, got %d", queryResponse.StatusCode)
	}

	headerRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/agent/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	headerRequest.Header.Set("Token", "header-token")
	headerResponse, err := http.DefaultClient.Do(headerRequest)
	if err != nil {
		t.Fatal(err)
	}
	headerResponse.Body.Close()
	if headerResponse.StatusCode != http.StatusOK {
		t.Fatalf("header token must be accepted, got %d", headerResponse.StatusCode)
	}
	if provider.token != "header-token" {
		t.Fatalf("identity provider received %q", provider.token)
	}
}

func TestChatAcceptsTokenHeader(t *testing.T) {
	runner, sessions := newTestRunner(t)
	provider := &headerIdentityProvider{}
	server := httptest.NewServer((&Handler{
		Runner:             runner,
		Sessions:           sessions,
		IdentityProvider:   provider,
		PublicChatWorkflow: "test",
	}).Routes())
	defer server.Close()

	request, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/api/agent/chat",
		strings.NewReader(`{"workflow":"test","message":"sales"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Token", "header-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("chat token status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	if provider.token != "header-token" {
		t.Fatalf("identity provider received %q", provider.token)
	}
}

func TestIdentityUpstreamFailureReturnsServiceUnavailable(t *testing.T) {
	runner, sessions := newTestRunner(t)
	server := httptest.NewServer((&Handler{Runner: runner, Sessions: sessions, IdentityProvider: unavailableIdentityProvider{}, PublicChatWorkflow: "test"}).Routes())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/agent/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Token", "header-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("upstream identity failure status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
}

func createRun(t *testing.T, serverURL, sessionID string) runAcceptedResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, serverURL+"/api/agent/chat", strings.NewReader("{\"session_id\":\""+sessionID+"\",\"workflow\":\"test\",\"message\":\"sales\"}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected create status: %d", response.StatusCode)
	}
	var envelope apiResponse
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 {
		t.Fatalf("unexpected response code: %+v", envelope)
	}
	data, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatal(err)
	}
	var accepted runAcceptedResponse
	if err := json.Unmarshal(data, &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.RunID == "" || accepted.EventsURL == "" {
		t.Fatalf("invalid accepted response: %+v", accepted)
	}
	return accepted
}

func newTestRunner(t *testing.T) (*orchestrator.Runner, *session.Store) {
	t.Helper()
	testWorkflow, err := testkit.Workflow()
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := orchestrator.NewWorkflowRegistry("test", testWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.New()
	return &orchestrator.Runner{Workflows: workflows, Sessions: sessions, MaxSteps: 16, RequestTimeout: time.Second}, sessions
}
