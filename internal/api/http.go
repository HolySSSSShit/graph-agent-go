package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
	"github.com/HolySSSSShit/go-agent/internal/orchestrator"
)

type Handler struct {
	Runner             *orchestrator.Runner
	Sessions           core.SessionStore
	Approvals          core.ApprovalService
	Runs               *RunManager
	Identity           core.Identity
	IdentityProvider   core.IdentityProvider
	Policy             core.PolicyGuard
	GeneratedImages    http.Handler
	PublicChatWorkflow string
	ToolSessions       interface{ Invalidate(core.ToolScope) }
}

type runRequest struct {
	SessionID string            `json:"session_id"`
	Workflow  string            `json:"workflow,omitempty"`
	Message   string            `json:"message"`
	Images    []core.ImageInput `json:"images,omitempty"`
}

type runAcceptedResponse struct {
	RunID     string `json:"run_id"`
	EventsURL string `json:"events_url"`
	SessionID string `json:"session_id"`
}

type apiResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apiResponse{Code: status, Msg: "error", Data: map[string]string{"error": message}})
}

type approvalDecisionRequest struct {
	Approved *bool `json:"approved"`
}

func (h *Handler) Routes() http.Handler {
	if h.Runs == nil {
		h.Runs = NewRunManager(h.Runner)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("POST /api/agent/chat", h.run)
	mux.HandleFunc("POST /api/agent/approvals/{id}", h.resolveApproval)
	mux.HandleFunc("POST /api/agent/runs/{id}/cancel", h.cancelRun)
	mux.HandleFunc("GET /api/agent/runs/{id}/events", h.runEvents)
	mux.HandleFunc("GET /api/agent/sessions", h.sessions)
	mux.HandleFunc("DELETE /api/agent/sessions/{id}", h.deleteSession)
	mux.HandleFunc("GET /api/agent/sessions/{id}/messages", h.messages)
	if h.GeneratedImages != nil {
		mux.Handle("GET /api/generated-images/{session}/{name}", h.GeneratedImages)
	}
	return withCORS(mux)
}

func (h *Handler) resolveApproval(w http.ResponseWriter, request *http.Request) {
	if h.Approvals == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "approval service is not configured"})
		return
	}
	var input approvalDecisionRequest
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Approved == nil {
		writeAPIError(w, http.StatusBadRequest, "approved is required")
		return
	}
	identity, authErr := h.resolveIdentity(request)
	if writeIdentityError(w, authErr) {
		return
	}
	approval, err := h.Approvals.Resolve(request.Context(), identity, request.PathValue("id"), core.ApprovalDecision{Approved: *input.Approved})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, core.ErrApprovalNotFound):
			status = http.StatusNotFound
		case errors.Is(err, core.ErrApprovalAlreadyResolved):
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	var checkpoint core.Checkpoint
	checkpointing := h.Runner != nil && h.Runner.CheckpointingEnabled()
	if checkpointing {
		var checkpointErr error
		checkpoint, checkpointErr = h.Runner.LatestCheckpoint(request.Context(), identity, approval.RunID)
		if checkpointErr != nil || checkpoint.Status != core.CheckpointStatusSuspended {
			if checkpointErr == nil {
				checkpointErr = errors.New("approval checkpoint is not suspended")
			}
			restoreApproval := func() {
				if rollbacker, ok := h.Approvals.(core.ApprovalRollbacker); ok {
					_ = rollbacker.RestorePending(request.Context(), identity, approval.ID)
				}
			}
			restoreApproval()
			writeJSON(w, http.StatusConflict, map[string]string{"error": checkpointErr.Error()})
			return
		}
	}
	restoreApproval := func() {
		if rollbacker, ok := h.Approvals.(core.ApprovalRollbacker); ok {
			if rollbackErr := rollbacker.RestorePending(request.Context(), identity, approval.ID); rollbackErr != nil {
				log.Printf("approval rollback failed approval_id=%s error=%v", approval.ID, rollbackErr)
			}
		}
	}
	if !*input.Approved {
		if checkpointing {
			checkpoint.Status = core.CheckpointStatusRejected
			if checkpoint.State.Control.Approval != nil {
				checkpoint.State.Control.Approval.Status = approval.Status
			}
			if err := h.Runner.SaveWorkflowCheckpoint(request.Context(), checkpoint); err != nil {
				restoreApproval()
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "status": approval.Status})
		return
	}
	if h.Runs == nil {
		h.Runs = NewRunManager(h.Runner)
	}
	if checkpointing {
		if approvalFingerprint := core.ActionFingerprint(approval.Action); approvalFingerprint != "" {
			if checkpoint.State.Control.ApprovedActionHashes == nil {
				checkpoint.State.Control.ApprovedActionHashes = make(map[string]struct{})
			}
			checkpoint.State.Control.ApprovedActionHashes[approvalFingerprint] = struct{}{}
		}
		if checkpoint.State.Control.Approval != nil {
			checkpoint.State.Control.Approval.Status = approval.Status
		}
		// 先保存已授权动作但保持 suspended，使 ResumeWithID 能原子地读取它。
		if err := h.Runner.SaveWorkflowCheckpoint(request.Context(), checkpoint); err != nil {
			restoreApproval()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	runID, err := h.Runs.ResumeWithCredential(approval.RunID, identity, h.requestToken(request))
	if err != nil {
		restoreApproval()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if checkpointing {
		checkpoint.Status = core.CheckpointStatusResumed
		if err := h.Runner.SaveWorkflowCheckpoint(request.Context(), checkpoint); err != nil {
			if h.Runs != nil {
				h.Runs.CancelForIdentity(runID, identity)
			}
			restoreApproval()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusAccepted, apiResponse{Code: 0, Msg: "success", Data: runAcceptedResponse{RunID: runID, EventsURL: "/api/agent/runs/" + url.PathEscape(runID) + "/events", SessionID: approval.SessionID}})
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) run(w http.ResponseWriter, request *http.Request) {
	var input runRequest
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil || (strings.TrimSpace(input.Message) == "" && len(input.Images) == 0) {
		writeAPIError(w, http.StatusBadRequest, "message or images is required")
		return
	}
	publicWorkflow := strings.TrimSpace(h.PublicChatWorkflow)
	if publicWorkflow == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "public chat workflow is not configured")
		return
	}
	if input.Workflow != publicWorkflow {
		writeAPIError(w, http.StatusBadRequest, "workflow must be "+publicWorkflow)
		return
	}
	if len(input.Images) > 4 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "最多同时发送 4 张图片"})
		return
	}
	for _, image := range input.Images {
		parsed, parseErr := url.Parse(image.URL)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || len(image.URL) > 4096 || (image.MIMEType != "" && !strings.HasPrefix(strings.ToLower(image.MIMEType), "image/")) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "图片 URL 或类型无效"})
			return
		}
	}
	identity, authErr := h.resolveIdentity(request)
	if writeIdentityError(w, authErr) {
		return
	}
	if h.Policy != nil {
		if err := h.Policy.AuthorizeAPI(request.Context(), identity, request.URL.Path); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "无权访问"})
			return
		}
	}
	if input.SessionID == "" {
		var err error
		input.SessionID, err = generateSessionID()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if h.Runner == nil || h.Runner.Workflows == nil {
		writeAPIError(w, http.StatusInternalServerError, "workflow registry is not configured")
		return
	}
	if _, err := h.Runner.Workflows.Resolve(input.Workflow); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	runID, err := h.Runs.StartWorkflowMessageWithCredential(input.SessionID, input.Workflow, identity, h.requestToken(request), core.Message{Role: "user", Content: input.Message, Images: input.Images})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, apiResponse{Code: 0, Msg: "success", Data: runAcceptedResponse{
		RunID:     runID,
		EventsURL: "/api/agent/runs/" + url.PathEscape(runID) + "/events",
		SessionID: input.SessionID,
	}})
}

func generateSessionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded), nil
}

func (h *Handler) runEvents(w http.ResponseWriter, request *http.Request) {
	runID := request.PathValue("id")
	identity, authErr := h.resolveIdentity(request)
	if writeIdentityError(w, authErr) {
		return
	}
	afterSequence := parseLastEventID(request.Header.Get("Last-Event-ID"))
	replay, events, unsubscribe, err := h.Runs.SubscribeForIdentity(runID, afterSequence, identity)
	if err != nil {
		log.Printf("SSE subscribe failed run_id=%s tenant_id=%s user_id=%s after_sequence=%d error=%v", runID, identity.TenantID, identity.UserID, afterSequence, err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	defer unsubscribe()
	log.Printf("SSE subscribed run_id=%s tenant_id=%s user_id=%s after_sequence=%d replay=%d", runID, identity.TenantID, identity.UserID, afterSequence, len(replay))
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	for _, event := range replay {
		if err := writeSSE(w, event); err != nil {
			log.Printf("SSE replay write failed run_id=%s sequence=%d error=%v", runID, event.Sequence, err)
			return
		}
		flusher.Flush()
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, event); err != nil {
				log.Printf("SSE event write failed run_id=%s sequence=%d error=%v", runID, event.Sequence, err)
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
				log.Printf("SSE heartbeat write failed run_id=%s error=%v", runID, err)
				return
			}
			flusher.Flush()
		case <-request.Context().Done():
			log.Printf("SSE disconnected run_id=%s error=%v", runID, request.Context().Err())
			return
		}
	}
}

func (h *Handler) cancelRun(w http.ResponseWriter, request *http.Request) {
	runID := request.PathValue("id")
	identity, authErr := h.resolveIdentity(request)
	if writeIdentityError(w, authErr) {
		return
	}
	if !h.Runs.CancelForIdentity(runID, identity) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found or already completed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseLastEventID(value string) int {
	sequence, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || sequence < 0 {
		return 0
	}
	return sequence
}

func writeSSE(w http.ResponseWriter, event orchestrator.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte("id: " + strconv.Itoa(event.Sequence) + "\nevent: " + event.Type + "\ndata: " + string(payload) + "\n\n"))
	return err
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Last-Event-ID, Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if request.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, request)
	})
}

func (h *Handler) sessions(w http.ResponseWriter, request *http.Request) {
	identity, authErr := h.resolveIdentity(request)
	if writeIdentityError(w, authErr) {
		return
	}
	values, err := h.Sessions.List(request.Context(), identity)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, values)
}

func (h *Handler) deleteSession(w http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	identity, authErr := h.resolveIdentity(request)
	if writeIdentityError(w, authErr) {
		return
	}
	if err := h.Sessions.Delete(request.Context(), identity, id); err != nil {
		if err.Error() == "session not found" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.ToolSessions != nil {
		h.ToolSessions.Invalidate(core.ToolScope{TenantID: identity.TenantID, UserID: identity.UserID, SessionID: id})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) messages(w http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	identity, authErr := h.resolveIdentity(request)
	if writeIdentityError(w, authErr) {
		return
	}
	value, err := h.Sessions.Get(request.Context(), identity, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, value.Messages)
}

func (h *Handler) identity() core.Identity {
	if h.Identity.TenantID != "" && h.Identity.UserID != "" {
		return h.Identity
	}
	// 开发期身份占位符。后续由 Harness 的权限体系注入真实 tenant/user。
	return core.Identity{TenantID: "local", UserID: "f6e363ee-263b-4e4e-a68c-60e82564b190", Role: "operator"}
}

func (h *Handler) resolveIdentity(request *http.Request) (core.Identity, error) {
	requestContext := core.WithRequestHost(request.Context(), request.Host)
	if h.IdentityProvider == nil {
		identity := h.identity()
		identity.Metadata = withRequestHostMetadata(identity.Metadata, request.Host)
		return identity, nil
	}
	token := h.requestToken(request)
	if token == "" {
		log.Printf("identity resolve rejected path=%s token_present=false", request.URL.Path)
		return core.Identity{}, core.ErrPermissionDenied
	}
	identity, err := h.IdentityProvider.Resolve(requestContext, token)
	identity.Metadata = withRequestHostMetadata(identity.Metadata, request.Host)
	if err != nil {
		log.Printf("identity resolve failed path=%s token_present=true error_type=%T error=%v", request.URL.Path, err, err)
	}
	return identity, err
}

func withRequestHostMetadata(metadata map[string]any, host string) map[string]any {
	if strings.TrimSpace(host) == "" {
		return metadata
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["request.host"] = core.NormalizeRequestHost(host)
	return metadata
}

func writeIdentityError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, core.ErrPermissionDenied) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未授权"})
		return true
	}
	if message, code, _, ok := core.PublicErrorInfo(err); ok {
		codeValue, parseErr := strconv.Atoi(code)
		if parseErr != nil || codeValue < 100 || codeValue > 599 {
			codeValue = http.StatusUnauthorized
		}
		value := map[string]any{"code": codeValue, "msg": message, "data": nil}
		var publicErr *core.PublicError
		if errors.As(err, &publicErr) && publicErr.ExternalTraceID != "" {
			value["trace_id"] = publicErr.ExternalTraceID
		}
		writeJSON(w, http.StatusOK, value)
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "身份服务暂时不可用"})
	return true
}

func (h *Handler) requestToken(request *http.Request) string {
	return strings.TrimSpace(request.Header.Get("Token"))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if status >= http.StatusBadRequest {
		if fields, ok := value.(map[string]string); ok {
			if message, exists := fields["error"]; exists && len(fields) == 1 {
				value = apiResponse{Code: status, Msg: "error", Data: map[string]string{"error": message}}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
