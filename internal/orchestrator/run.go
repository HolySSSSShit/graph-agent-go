package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

// Event 是运行过程中发送给前端的 SSE 事件 payload。progress 只描述节点阶段，不暴露原始思维链。
type Event struct {
	Type           string               `json:"type"`
	Stage          string               `json:"stage,omitempty"`
	Status         string               `json:"status,omitempty"`
	Message        string               `json:"message,omitempty"`
	ErrorCode      string               `json:"error_code,omitempty"`
	ErrorDetails   map[string]any       `json:"error_details,omitempty"`
	Code           any                  `json:"code,omitempty"`
	Msg            string               `json:"msg,omitempty"`
	ToolName       string               `json:"tool_name,omitempty"`
	Text           string               `json:"text,omitempty"`
	RunID          string               `json:"run_id"`
	TraceID        string               `json:"trace_id"`
	ApprovalID     string               `json:"approval_id,omitempty"`
	Visualizations []core.Visualization `json:"visualizations,omitempty"`
	Sequence       int                  `json:"sequence"`
	traceMessage   string
}

func (e Event) MarshalJSON() ([]byte, error) {
	type eventAlias Event
	data, err := json.Marshal(eventAlias(e))
	if err != nil || e.Type != "run.failed" {
		return data, err
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	delete(object, "message")
	delete(object, "error_code")
	delete(object, "error_details")
	return json.Marshal(object)
}

// Runner 只管理节点图、预算、取消、会话落库和 SSE；模型、工具与业务判断均在节点中实现。
type Runner struct {
	Workflows           *WorkflowRegistry
	Sessions            core.SessionStore
	Trace               core.TraceRecorder
	Compactor           core.SessionCompactor
	CompactKeepMessages int
	MaxSteps            int
	RequestTimeout      time.Duration
	Checkpoints         core.CheckpointStore
	Leases              core.RunLeaseStore
	LeaseDuration       time.Duration
	MaxTenantRuns       int
}

// checkpointStore 返回指定 Workflow 的 checkpoint 视图。
// 保存和恢复业务 payload 时必须使用本次运行实际选择的 Codec。
func (r *Runner) checkpointStore(workflow WorkflowDefinition) core.CheckpointStore {
	if r == nil || r.Checkpoints == nil || workflow == nil {
		if r == nil {
			return nil
		}
		return r.Checkpoints
	}
	return WorkflowCheckpointStore{Store: r.Checkpoints, Workflow: workflow}
}

func (r *Runner) Run(parent context.Context, sessionID string, identity core.Identity, input string) <-chan Event {
	return r.RunWorkflow(parent, "", sessionID, identity, input)
}

// RunWorkflow 使用显式 kind 选择 Workflow；kind 为空时使用注册表默认项。
func (r *Runner) RunWorkflow(parent context.Context, workflowKind, sessionID string, identity core.Identity, input string) <-chan Event {
	return r.RunWorkflowWithID(parent, workflowKind, sessionID, identity, input, "")
}

// RunWithID 使用默认 Workflow 和调用方提供的运行 ID 执行节点图。
func (r *Runner) RunWithID(parent context.Context, sessionID string, identity core.Identity, input, requestedRunID string) <-chan Event {
	return r.RunWorkflowWithID(parent, "", sessionID, identity, input, requestedRunID)
}

// RunWorkflowWithID 允许传入 Workflow kind 和运行 ID。
func (r *Runner) RunWorkflowWithID(parent context.Context, workflowKind, sessionID string, identity core.Identity, input, requestedRunID string) <-chan Event {
	return r.RunWorkflowWithMessageAndCredential(parent, workflowKind, sessionID, identity, core.Message{Role: "user", Content: input}, requestedRunID, "")
}

// RunWithIDAndCredential 使用默认 Workflow，并在不污染 Identity 的情况下传递请求级凭证。
func (r *Runner) RunWithIDAndCredential(parent context.Context, sessionID string, identity core.Identity, input, requestedRunID, credential string) <-chan Event {
	return r.RunWorkflowWithMessageAndCredential(parent, "", sessionID, identity, core.Message{Role: "user", Content: input}, requestedRunID, credential)
}

func (r *Runner) RunWithMessageAndCredential(parent context.Context, sessionID string, identity core.Identity, message core.Message, requestedRunID, credential string) <-chan Event {
	return r.RunWorkflowWithMessageAndCredential(parent, "", sessionID, identity, message, requestedRunID, credential)
}

func (r *Runner) RunWorkflowWithMessageAndCredential(parent context.Context, workflowKind, sessionID string, identity core.Identity, message core.Message, requestedRunID, credential string) <-chan Event {
	if r != nil && r.Checkpoints != nil {
		if checkpoint, err := r.LatestSuspendedCheckpoint(parent, identity, sessionID); err == nil {
			if workflowKind == "" || strings.TrimSpace(workflowKind) == checkpoint.State.WorkflowKind {
				return r.runWithMessageID(parent, checkpoint.State.WorkflowKind, sessionID, identity, message, requestedRunID, credential, &checkpoint)
			}
		}
	}
	return r.runWithMessageID(parent, workflowKind, sessionID, identity, message, requestedRunID, credential, nil)
}

// ResumeWithID 从最近的挂起 checkpoint 恢复运行。新的 run 使用新的计数器和预算，
// 但保留 checkpoint 中的事实、工具结果和会话上下文。
func (r *Runner) ResumeWithID(parent context.Context, identity core.Identity, runID, requestedRunID, credential string) <-chan Event {
	resumeRunID := requestedRunID
	if resumeRunID == "" {
		resumeRunID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	if r == nil || r.Checkpoints == nil {
		return failedEventChannel(resumeRunID, "checkpoint store is not configured")
	}
	cp, err := r.LatestCheckpoint(parent, identity, runID)
	if err != nil || (cp.Status != core.CheckpointStatusSuspended && cp.Status != core.CheckpointStatusResumed) {
		if err == nil {
			err = errors.New("checkpoint is not resumable")
		}
		return failedEventChannel(resumeRunID, err.Error())
	}
	return r.runWithID(parent, cp.State.WorkflowKind, cp.SessionID, identity, "", resumeRunID, credential, &cp)
}

// ResumeSession 恢复指定会话最近一次挂起运行；恢复会创建新的 run_id。
func (r *Runner) ResumeSession(parent context.Context, identity core.Identity, sessionID, requestedRunID, credential string) <-chan Event {
	resumeRunID := requestedRunID
	if resumeRunID == "" {
		resumeRunID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	if r == nil || r.Checkpoints == nil {
		return failedEventChannel(resumeRunID, "checkpoint store is not configured")
	}
	cp, err := r.LatestSuspendedCheckpoint(parent, identity, sessionID)
	if err != nil {
		return failedEventChannel(resumeRunID, err.Error())
	}
	return r.runWithID(parent, cp.State.WorkflowKind, sessionID, identity, "", resumeRunID, credential, &cp)
}

func failedEventChannel(runID, detail string) <-chan Event {
	out := make(chan Event, 1)
	out <- Event{Type: "run.failed", Status: "failed", Message: "暂时无法恢复本次请求，请稍后重试", ErrorCode: "resume_failed", RunID: runID, TraceID: runID, Sequence: 1, traceMessage: detail}
	close(out)
	return out
}

func (r *Runner) runWithID(parent context.Context, workflowKind, sessionID string, identity core.Identity, input, requestedRunID, credential string, resume *core.Checkpoint) <-chan Event {
	return r.runWithMessageID(parent, workflowKind, sessionID, identity, core.Message{Role: "user", Content: input}, requestedRunID, credential, resume)
}

func (r *Runner) runWithMessageID(parent context.Context, workflowKind, sessionID string, identity core.Identity, inputMessage core.Message, requestedRunID, credential string, resume *core.Checkpoint) <-chan Event {
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		ctx := parent
		if host, ok := identity.Metadata["request.host"].(string); ok {
			ctx = core.WithRequestHost(ctx, host)
		}
		cancel := func() {}
		if r.RequestTimeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, r.RequestTimeout)
		}
		defer cancel()

		runID := requestedRunID
		if runID == "" {
			runID = fmt.Sprintf("run-%d", time.Now().UnixNano())
		}
		sequence := 0
		runStartedAt := time.Now()
		var graphPath []core.NodeType
		leaseStatus := "completed"
		var lease core.RunLease
		var leaseDone chan struct{}
		var stopLease context.CancelFunc
		defer func() {
			if stopLease != nil {
				stopLease()
				<-leaseDone
				leaseCtx, cancelLease := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancelLease()
				_ = r.Leases.ReleaseRunLease(leaseCtx, lease, leaseStatus)
			}
		}()
		progressSeen := make(map[string]struct{})
		budgetDiagnosticLogged := false
		emit := func(event Event) {
			sequence++
			event.RunID = runID
			if event.TraceID == "" {
				event.TraceID = runID
			}
			event.Sequence = sequence
			if r.Trace != nil {
				message := event.traceMessage
				if message == "" {
					message = event.Message
				}
				if message == "" {
					message = event.Text
				}
				traceContext := ctx
				if event.Type == "run.failed" {
					traceContext = context.WithoutCancel(ctx)
				}
				_ = r.Trace.Record(traceContext, core.TraceEvent{
					TraceID: runID, RunID: runID, SessionID: sessionID, TenantID: identity.TenantID, UserID: identity.UserID, Sequence: sequence,
					Type: event.Type, Stage: core.NodeType(event.Stage), Status: event.Status,
					Message: message, ToolName: event.ToolName,
					Metadata: visualizationMetadata(event.Visualizations),
				})
			}
			terminal := event.Type == "run.failed"
			done := ctx.Done()
			if terminal {
				done = parent.Done()
			}
			select {
			case out <- event:
			case <-done:
			}
		}
		fail := func(err error) {
			leaseStatus = "failed"
			if err == nil {
				err = errors.New("agent run failed")
			}
			logGraphPath(runID, graphPath, err)
			message := "暂时无法完成本次请求，请稍后重试"
			var errorCode string
			var errorDetails map[string]any
			errorTraceID := runID
			if publicMessage, code, details, ok := core.PublicErrorInfo(err); ok {
				message = publicMessage
				errorCode = code
				errorDetails = details
				var publicErr *core.PublicError
				if errors.As(err, &publicErr) && publicErr.ExternalTraceID != "" {
					errorTraceID = publicErr.ExternalTraceID
				}
			}
			failedEvent := Event{Type: "run.failed", Status: "failed", TraceID: errorTraceID, ErrorDetails: errorDetails, traceMessage: err.Error()}
			failedEvent.Code = errorCode
			failedEvent.Msg = message
			failedEvent.Message = message
			failedEvent.ErrorCode = errorCode
			emit(failedEvent)
		}
		session, err := r.Sessions.Get(ctx, identity, sessionID)
		if err != nil {
			fail(err)
			return
		}
		if r.Leases != nil {
			workerID := fmt.Sprintf("%s-%d", runID, time.Now().UnixNano())
			lease, err = r.Leases.AcquireRunLease(ctx, core.RunLeaseRequest{
				RunID: runID, TenantID: identity.TenantID, UserID: identity.UserID, SessionID: sessionID,
				WorkerID: workerID, LeaseDuration: r.LeaseDuration, MaxTenantRuns: r.MaxTenantRuns,
			})
			if err != nil {
				fail(err)
				return
			}
			leaseCtx, cancelLease := context.WithCancel(ctx)
			ctx = leaseCtx
			stopLease = cancelLease
			leaseDone = make(chan struct{})
			go r.renewLease(leaseCtx, lease, leaseDone, cancelLease)
		}
		emit(Event{Type: "run.started", Status: "running"})
		if r.Trace != nil {
			_ = r.Trace.Record(ctx, core.TraceEvent{
				TraceID: runID, RunID: runID, SessionID: sessionID, TenantID: identity.TenantID, UserID: identity.UserID,
				Type: "run.input", Status: "received", Message: inputMessage.Content,
			})
		}
		if r.Workflows == nil {
			fail(errors.New("workflow registry is not configured"))
			return
		}
		workflow, err := r.Workflows.Resolve(workflowKind)
		if err != nil {
			fail(err)
			return
		}
		workflowKind = workflow.Kind()
		var workflowPayload []byte
		var workflowData any
		if resume == nil {
			workflowData, err = workflow.NewStateValue()
			if err == nil {
				workflowPayload, err = workflow.EncodeStateValue(workflowData)
			}
			if err != nil {
				fail(err)
				return
			}
		} else if resume.State.WorkflowKind != workflowKind {
			fail(fmt.Errorf("checkpoint workflow mismatch: expected %s, got %s", workflowKind, resume.State.WorkflowKind))
			return
		}
		if resume != nil {
			workflowData, err = workflow.DecodeStatePayload(resume.State.WorkflowPayload)
			if err != nil {
				fail(fmt.Errorf("decode workflow state: %w", err))
				return
			}
		}
		if err := workflow.Validate(); err != nil {
			fail(fmt.Errorf("workflow graph is invalid: %w", err))
			return
		}
		recordRunCompleted := func() {
			logGraphPath(runID, graphPath, nil)
			if r.Trace == nil {
				return
			}
			traceCtx, cancelTrace := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancelTrace()
			_ = r.Trace.Record(traceCtx, core.TraceEvent{
				TraceID: runID, RunID: runID, SessionID: sessionID, TenantID: identity.TenantID, UserID: identity.UserID,
				Type: "run.completed", Status: "completed", DurationMS: time.Since(runStartedAt).Milliseconds(),
			})
		}
		currentInput := inputMessage
		if resume != nil && strings.TrimSpace(inputMessage.Content) == "" && len(inputMessage.Images) == 0 {
			currentInput = resume.State.Input
		} else {
			// 图片 URL 只用于本轮视觉请求，不写入会话消息或 checkpoint。
			persistedInput := currentInput
			persistedInput.Images = nil
			if err := r.Sessions.AppendMessage(ctx, identity, sessionID, persistedInput); err != nil {
				fail(err)
				return
			}
			session.Messages = append(session.Messages, persistedInput)
		}
		compact, err := r.Sessions.GetCompact(ctx, identity, sessionID)
		if err != nil {
			fail(err)
			return
		}
		if compact.Content != "" {
			session.Compact = &compact
		}
		if r.Compactor != nil && r.CompactKeepMessages > 0 && len(session.Messages) > r.CompactKeepMessages {
			through := compactThroughCompleteTurn(session.Messages, r.CompactKeepMessages)
			if through > compact.CoveredMessages {
				updated, compactErr := r.Compactor.Compact(ctx, compact, session.Messages[compact.CoveredMessages:through])
				if compactErr == nil {
					updated.CoveredMessages = through
					if saved, saveErr := r.Sessions.CompareAndSwapCompact(ctx, identity, sessionID, compact.Version, updated); saveErr == nil && saved {
						updated.Version = compact.Version + 1
						session.Compact = &updated
					}
				}
			}
		}
		if session.Compact != nil && session.Compact.CoveredMessages > 0 && session.Compact.CoveredMessages < len(session.Messages) {
			session.Messages = session.Messages[session.Compact.CoveredMessages:]
		}
		if credential == "" {
			credential = "configured"
		}
		scope := core.ToolScope{TenantID: identity.TenantID, UserID: identity.UserID, SessionID: sessionID, CredentialRef: credential}
		values := &core.RunState{
			WorkflowState: core.WorkflowState{Version: 1, WorkflowKind: workflowKind, WorkflowPayload: workflowPayload},
			RuntimeState: core.RuntimeState{Session: session, Input: currentInput, Scope: scope,
				Metrics: core.RunMetrics{MaxSteps: r.MaxSteps, NodeVisits: make(map[core.NodeType]int), NodeDurationsMS: make(map[core.NodeType]int64)}},
		}
		if resume != nil {
			restored := cloneRunState(resume.State)
			restored.Session = session
			restored.Input = currentInput
			restored.Scope = scope
			restored.Metrics = core.RunMetrics{MaxSteps: r.MaxSteps, NodeVisits: make(map[core.NodeType]int), NodeDurationsMS: make(map[core.NodeType]int64)}
			values = &restored
		}
		values.WorkflowState.Data = workflowData
		runContext := core.RunContext{RunID: runID, TraceID: runID, SessionID: sessionID, Identity: identity, Credential: credential, AllowedNodes: workflow.NodeTypes()}
		restoredEvents, restoreErr := workflow.Restore(ctx, r.Sessions, runContext, workflowData)
		if restoreErr != nil {
			fail(fmt.Errorf("restore workflow state: %w", restoreErr))
			return
		}
		r.recordWorkflowEvents(ctx, runContext, restoredEvents)
		entry, ok := workflow.Entry()
		if !ok {
			fail(errors.New("graph entry node is not configured"))
			return
		}
		if !workflow.Contains(entry) {
			fail(fmt.Errorf("graph entry node is not registered: %s", entry))
			return
		}
		current := entry
		if resume != nil {
			next := resume.Route.SelectedNext
			// 普通聊天不能绕过挂起的审批：带有新输入时重新进入挂起节点，
			// 只有确认接口调用的空输入恢复才沿 checkpoint 的下一条边继续。
			if (strings.TrimSpace(inputMessage.Content) != "" || len(inputMessage.Images) > 0) && resume.Status == core.CheckpointStatusSuspended {
				next = resume.Node
			}
			if next == "" {
				fail(errors.New("checkpoint has no resume route"))
				return
			}
			if !workflow.Contains(next) {
				fail(fmt.Errorf("checkpoint resume node not found: %s", next))
				return
			}
			current = next
		}
		maxSteps := r.MaxSteps
		if maxSteps < 1 {
			maxSteps = core.DefaultAgentMaxSteps
		}
		for step := 0; step <= maxSteps; step++ {
			if step == maxSteps {
				// 节点预算耗尽后额外保留一次 Workflow 图定义的合法终点执行机会。
				values.Metrics.BudgetExhausted = true
				budgetTarget, ok := workflow.BudgetTarget()
				if !ok {
					fail(errors.New("workflow graph has no budget target"))
					return
				}
				if !budgetDiagnosticLogged {
					log.Printf("[%s] DEGRADED budget exhausted: steps=%d max_steps=%d; redirecting %s -> %s", runID, values.Metrics.Steps, maxSteps, current, budgetTarget)
					budgetDiagnosticLogged = true
				}
				current = budgetTarget
			}
			if err := ctx.Err(); err != nil {
				fail(err)
				return
			}
			graphPath = append(graphPath, current)
			values.Metrics.Steps = step + 1
			values.Metrics.MaxSteps = maxSteps
			values.Metrics.LastNode = current
			values.Metrics.NodeVisits[current]++
			startedAt := time.Now()
			nodeEvents := make(chan core.NodeEvent, 32)
			nodeEventsDone := make(chan struct{})
			nodeInput := RuntimeNodeInput{
				Run:      core.RunContext{RunID: runID, TraceID: runID, SessionID: sessionID, Identity: identity, Credential: credential, Step: step, AllowedNodes: workflow.NodeTypes()},
				Messages: session.Messages,
				Runtime:  values,
				Data:     values.WorkflowState.Data,
				Events:   nodeEvents,
			}
			if message, progressErr := workflow.ProgressMessage(current, nodeInput); progressErr != nil {
				close(nodeEvents)
				fail(progressErr)
				return
			} else if message != "" {
				progressKey := current.String() + "\x00" + message
				if _, seen := progressSeen[progressKey]; !seen {
					progressSeen[progressKey] = struct{}{}
					emit(Event{Type: "progress", Stage: current.String(), Status: "running", Message: message})
				}
			}
			go func(stage core.NodeType) {
				defer close(nodeEventsDone)
				for nodeEvent := range nodeEvents {
					// 内部工具名保留在工具节点产生的审计轨迹中，但不会进入面向用户的 SSE payload。
					if r.Trace != nil {
						_ = r.Trace.Record(ctx, core.TraceEvent{
							TraceID: runID, RunID: runID, SessionID: sessionID,
							TenantID: identity.TenantID, UserID: identity.UserID,
							Type: string(nodeEvent.Type), Stage: stage,
							Status: nodeEvent.Status, Message: nodeEvent.Message,
						})
					}
					if !nodeEvent.Internal {
						emit(Event{Type: string(nodeEvent.Type), Stage: stage.String(), Status: nodeEvent.Status, Message: nodeEvent.Message, Text: nodeEvent.Text})
					}
				}
			}(current)
			result, err := workflow.ExecuteNode(ctx, current, nodeInput)
			close(nodeEvents)
			<-nodeEventsDone
			if err != nil {
				if r.Trace != nil {
					_ = r.Trace.Record(ctx, core.TraceEvent{TraceID: runID, RunID: runID, SessionID: sessionID, TenantID: identity.TenantID, UserID: identity.UserID, Type: "node.failed", Stage: current, Status: "failed", Message: err.Error(), DurationMS: time.Since(startedAt).Milliseconds()})
				}
				fail(err)
				return
			}
			// 节点也可以通过返回值提交一次性诊断事件。与流式事件使用同一条
			// 审计路径，避免校验失败等信息只存在于进程内而无法追溯。
			for _, nodeEvent := range result.Events {
				if r.Trace != nil {
					_ = r.Trace.Record(ctx, core.TraceEvent{
						TraceID: runID, RunID: runID, SessionID: sessionID,
						TenantID: identity.TenantID, UserID: identity.UserID,
						Type: string(nodeEvent.Type), Stage: current,
						Status: nodeEvent.Status, Message: nodeEvent.Message,
					})
				}
				if !nodeEvent.Internal {
					emit(Event{Type: string(nodeEvent.Type), Stage: current.String(), Status: nodeEvent.Status, Message: nodeEvent.Message, Text: nodeEvent.Text})
				}
			}
			for _, message := range result.Messages {
				if r.Trace != nil && (message.Content != "" || len(message.Images) > 0) {
					_ = r.Trace.Record(ctx, core.TraceEvent{
						TraceID: runID, RunID: runID, SessionID: sessionID,
						TenantID: identity.TenantID, UserID: identity.UserID,
						Type: "node.output", Stage: current, Status: result.Status,
						Output:   message.Content,
						Metadata: map[string]any{"role": message.Role, "image_count": len(message.Images)},
					})
				}
			}
			if r.Trace != nil {
				_ = r.Trace.Record(ctx, core.TraceEvent{TraceID: runID, RunID: runID, SessionID: sessionID, TenantID: identity.TenantID, UserID: identity.UserID, Type: "node.completed", Stage: current, Status: result.Status, DurationMS: time.Since(startedAt).Milliseconds()})
			}
			if values.Metrics.NodeVisits == nil {
				values.Metrics.NodeVisits = make(map[core.NodeType]int)
			}
			if values.Metrics.NodeDurationsMS == nil {
				values.Metrics.NodeDurationsMS = make(map[core.NodeType]int64)
			}
			durationMS := time.Since(startedAt).Milliseconds()
			values.Metrics.LastNodeDurationMS = durationMS
			values.Metrics.ElapsedMS = time.Since(runStartedAt).Milliseconds()
			values.Metrics.NodeDurationsMS[current] += durationMS
			values.Route.CurrentNode = current

			terminalStatus := result.Status == core.NodeStatusComplete || result.Status == core.NodeStatusWaitUser
			validStatus := result.Status == core.NodeStatusContinue || terminalStatus
			if !validStatus || (terminalStatus && !workflow.IsEnd(current)) {
				// 非终点结束或未知状态都违反图协议。该节点的控制产出和消息不会
				// 进入 checkpoint，Workflow 钩子负责清理已写入的业务状态。
				violationReason := fmt.Sprintf("node returned unsupported status %q", result.Status)
				if terminalStatus {
					violationReason = fmt.Sprintf("non-end node returned terminal status %q", result.Status)
				}
				fallback, ok := workflow.FallbackTarget()
				if !ok {
					fail(fmt.Errorf("%s; workflow has no fallback target", violationReason))
					return
				}
				log.Printf("[%s] ERROR graph protocol violation: %s; redirecting %s -> %s", runID, violationReason, current, fallback)
				if err := workflow.HandleProtocolViolation(values.WorkflowState.Data, violationReason); err != nil {
					fail(err)
					return
				}
				if r.Trace != nil {
					_ = r.Trace.Record(ctx, core.TraceEvent{TraceID: runID, RunID: runID, SessionID: sessionID, TenantID: identity.TenantID, UserID: identity.UserID, Type: "graph.protocol_violation", Stage: current, Status: "redirected", Message: violationReason})
				}
				current = fallback
				continue
			}
			if result.PendingAction != nil {
				values.Control.PendingAction = result.PendingAction
			}
			if result.Approval != nil {
				values.Control.Approval = result.Approval
			}
			diagnostics, afterErr := workflow.AfterNode(ctx, current, values, values.WorkflowState.Data)
			if afterErr != nil {
				fail(fmt.Errorf("workflow after node %s: %w", current, afterErr))
				return
			}
			r.recordWorkflowDiagnostics(ctx, runContext, diagnostics)
			if result.Status == core.NodeStatusWaitUser {
				stepRun := runContext
				stepRun.Step = step
				var decision core.RouteDecision
				var routeErr error
				if workflow.IsEnd(current) && result.Approval == nil {
					// End 节点的 wait_user 表示交互暂停，不需要寻找下一条边；
					// 新输入恢复时 Runner 会直接重新执行该挂起节点。
					decision = core.RouteDecision{SourceNode: current, AdmissionPassed: true}
				} else {
					decision, routeErr = workflow.ResolveRoute(ctx, stepRun, current, result, values)
				}
				values.Route.History = append(values.Route.History, decision)
				values.Route.Last = &decision
				if routeErr != nil {
					graphPath = appendRouteTarget(graphPath, decision.SelectedNext)
					if r.Trace != nil {
						_ = r.Trace.Record(ctx, core.TraceEvent{TraceID: runID, RunID: runID, SessionID: sessionID, TenantID: identity.TenantID, UserID: identity.UserID, Type: "graph.route_failed", Stage: current, Status: "failed", Message: routeErr.Error()})
					}
					fail(routeErr)
					return
				}
				leaseStatus = "suspended"
				if checkpointErr := r.saveCheckpoint(ctx, workflow, core.RunContext{RunID: runID, TraceID: runID, SessionID: sessionID, Identity: identity, Step: step}, credential, current, result, values, decision); checkpointErr != nil {
					fail(fmt.Errorf("save suspended checkpoint: %w", checkpointErr))
					return
				}
				if values.Control.Approval != nil || result.Approval != nil {
					message := "需要用户确认后继续"
					if values.Control.Approval != nil && strings.TrimSpace(values.Control.Approval.Summary) != "" {
						message = values.Control.Approval.Summary
						if err := r.Sessions.AppendMessage(ctx, identity, sessionID, core.Message{Role: "assistant", Content: message}); err == nil {
							emit(Event{Type: "message.completed", Stage: current.String(), Status: "waiting", Text: message})
						}
					}
					event := Event{Type: "approval.required", Status: "waiting", Message: message}
					if values.Control.Approval != nil {
						event.ApprovalID = values.Control.Approval.ID
					}
					emit(event)
					return
				}
				if len(result.Messages) > 0 {
					for _, item := range result.Messages {
						message := item
						message.ContextExcluded = result.ContextExcluded
						if err := r.Sessions.AppendMessage(ctx, identity, sessionID, message); err != nil {
							fail(err)
							return
						}
						emit(Event{Type: "message.completed", Stage: current.String(), Status: "waiting", Text: message.Content, Visualizations: result.Visualizations})
					}
				}
				return
			}
			if result.Status == core.NodeStatusComplete {
				if checkpointErr := r.saveCheckpoint(ctx, workflow, core.RunContext{RunID: runID, TraceID: runID, SessionID: sessionID, Identity: identity, Step: step}, credential, current, result, values, core.RouteDecision{SourceNode: current, SelectedNext: "", AdmissionPassed: true}); checkpointErr != nil {
					fail(fmt.Errorf("save completed checkpoint: %w", checkpointErr))
					return
				}
				if len(result.Messages) == 0 {
					fail(errors.New("finalize node returned no message"))
					return
				}
				final := result.Messages[len(result.Messages)-1]
				final.ContextExcluded = result.ContextExcluded
				persistedEvents, persistErr := workflow.Persist(ctx, r.Sessions, runContext, values.WorkflowState.Data)
				if persistErr != nil {
					fail(persistErr)
					return
				}
				r.recordWorkflowEvents(ctx, runContext, persistedEvents)
				if err := r.Sessions.AppendMessage(ctx, identity, sessionID, final); err != nil {
					fail(err)
					return
				}
				visualizations := append([]core.Visualization(nil), result.Visualizations...)
				final.Visualizations = visualizations
				if !result.Streamed {
					event := Event{Type: "message.delta", Text: final.Content, Stage: result.ResponseStage.String()}
					emit(event)
				}
				recordRunCompleted()
				event := Event{Type: "message.completed", Text: final.Content, Visualizations: visualizations, Stage: result.ResponseStage.String()}
				emit(event)
				return
			}
			stepRun := runContext
			stepRun.Step = step
			decision, routeErr := workflow.ResolveRoute(ctx, stepRun, current, result, values)
			if values.Metrics.BudgetExhausted && !budgetDiagnosticLogged {
				log.Printf("[%s] DEGRADED workflow node visit limit reached; redirecting %s -> %s", runID, current, decision.SelectedNext)
				budgetDiagnosticLogged = true
			}
			values.Route.History = append(values.Route.History, decision)
			values.Route.Last = &decision
			approvalTarget, hasApprovalTarget := workflow.ApprovalTarget()
			needsApproval := routeErr == nil && decision.RequiresApproval && hasApprovalTarget && current != approvalTarget
			if needsApproval {
				action := &core.Action{Type: "transition", Name: decision.TransitionID, Reason: decision.Reason, ResumeNode: decision.SelectedNext}
				values.Control.PendingAction = action
			}
			checkpointErr := r.saveCheckpoint(ctx, workflow, core.RunContext{RunID: runID, TraceID: runID, SessionID: sessionID, Identity: identity, Step: step}, credential, current, result, values, decision)
			if routeErr != nil {
				graphPath = appendRouteTarget(graphPath, decision.SelectedNext)
				if r.Trace != nil {
					_ = r.Trace.Record(ctx, core.TraceEvent{TraceID: runID, RunID: runID, SessionID: sessionID, TenantID: identity.TenantID, UserID: identity.UserID, Type: "graph.route_failed", Stage: current, Status: "failed", Message: routeErr.Error()})
				}
				fail(routeErr)
				return
			}
			// 普通中间快照写失败只记录审计，当前节点仍可按图继续；挂起和完成状态
			// 在上面的分支中已经作为运行失败处理。
			_ = checkpointErr
			if needsApproval {
				current = approvalTarget
			} else {
				current = decision.SelectedNext
			}
		}
		budgetErr := errors.New("finalize node did not complete after step budget exhaustion")
		if r.Trace != nil {
			_ = r.Trace.Record(ctx, core.TraceEvent{TraceID: runID, RunID: runID, SessionID: sessionID, TenantID: identity.TenantID, UserID: identity.UserID, Type: "graph.budget_exhausted", Stage: current, Status: "failed", Message: budgetErr.Error(), Metadata: map[string]any{"steps": values.Metrics.Steps, "max_steps": values.Metrics.MaxSteps}})
		}
		fail(budgetErr)
	}()
	return out
}

func appendRouteTarget(path []core.NodeType, target core.NodeType) []core.NodeType {
	if target == "" || (len(path) > 0 && path[len(path)-1] == target) {
		return path
	}
	return append(path, target)
}

func logGraphPath(runID string, path []core.NodeType, err error) {
	parts := make([]string, 0, len(path))
	for _, node := range path {
		parts = append(parts, node.String())
	}
	if err != nil {
		// 条件路由未声明目标时，错误本身已经包含完整的 source -> target，
		// 避免把同一段边重复打印。
		if strings.Contains(err.Error(), "[Edge is not defined]") {
			log.Printf("[%s] ERROR %v", runID, err)
			return
		}
		if len(parts) == 0 {
			log.Printf("[%s] ERROR %v", runID, err)
			return
		}
		log.Printf("[%s] ERROR %v %s", runID, err, strings.Join(parts, " -> "))
		return
	}
	log.Printf("[%s] %s", runID, strings.Join(parts, " -> "))
}

func visualizationMetadata(values []core.Visualization) map[string]any {
	if len(values) == 0 {
		return nil
	}
	return map[string]any{"visualizations": values}
}

func (r *Runner) recordWorkflowEvents(ctx context.Context, run core.RunContext, events []core.TraceEvent) {
	if r.Trace == nil {
		return
	}
	for _, event := range events {
		event.TraceID = run.TraceID
		event.RunID = run.RunID
		event.SessionID = run.SessionID
		event.TenantID = run.Identity.TenantID
		event.UserID = run.Identity.UserID
		if event.At.IsZero() {
			event.At = time.Now().UTC()
		}
		_ = r.Trace.Record(ctx, event)
	}
}

func (r *Runner) recordWorkflowDiagnostics(ctx context.Context, run core.RunContext, diagnostics []WorkflowDiagnostic) {
	for _, diagnostic := range diagnostics {
		if diagnostic.Console != "" {
			log.Printf("[%s] %s", run.RunID, diagnostic.Console)
		}
		r.recordWorkflowEvents(ctx, run, []core.TraceEvent{{
			Type: diagnostic.Type, Stage: diagnostic.Stage, Status: diagnostic.Status,
			Message: diagnostic.Message, Metadata: diagnostic.Metadata,
		}})
	}
}

func (r *Runner) saveCheckpoint(ctx context.Context, workflow WorkflowDefinition, run core.RunContext, credential string, nodeType core.NodeType, output core.NodeOutput, state *core.RunState, decision core.RouteDecision) error {
	checkpoints := r.checkpointStore(workflow)
	if checkpoints == nil || state == nil {
		return nil
	}
	checkpointState := cloneRunState(*state)
	// 图片 URL 仅供本轮视觉模型请求使用，任何 Workflow 的 checkpoint 都不持久化原始引用。
	checkpointState.Input.Images = nil
	// WorkflowCheckpointStore 需要当前强类型状态生成最新 payload；真正写入底层存储前会清掉 Data。
	checkpointState.WorkflowState.Data = state.WorkflowState.Data
	checkpoint := core.Checkpoint{
		ID:    fmt.Sprintf("%s-%d", run.RunID, time.Now().UnixNano()),
		RunID: run.RunID, TraceID: run.TraceID, SessionID: run.SessionID, Identity: run.Identity,
		Node: nodeType, Status: output.Status, State: checkpointState, Route: decision, Credential: credential, Sequence: run.Step + 1, CreatedAt: time.Now().UTC(),
	}
	if output.Status == core.NodeStatusWaitUser {
		checkpoint.Status = core.CheckpointStatusSuspended
	} else if output.Status == core.NodeStatusComplete {
		checkpoint.Status = core.CheckpointStatusCompleted
	}
	if r.Trace != nil {
		checkpoint.TraceEvents = r.Trace.List(checkpoint.RunID)
	}
	// 运行元数据由调用方通过 State 和路由上下文提供。
	if err := checkpoints.SaveCheckpoint(ctx, checkpoint); err != nil {
		if r.Trace == nil {
			return err
		}
		traceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = r.Trace.Record(traceCtx, core.TraceEvent{TraceID: run.TraceID, RunID: run.RunID, SessionID: run.SessionID, TenantID: run.Identity.TenantID, UserID: run.Identity.UserID, Type: "checkpoint.failed", Stage: nodeType, Status: "failed", Message: err.Error()})
		return err
	}
	return nil
}

func compactThroughCompleteTurn(messages []core.Message, keep int) int {
	through := len(messages) - keep
	for through > 0 && messages[through-1].Role != "assistant" {
		through--
	}
	return through
}
