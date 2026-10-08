package orchestrator_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"
	"github.com/HolySSSSShit/graph-agent-go/internal/session"
	"github.com/HolySSSSShit/graph-agent-go/internal/testkit"
)

type timeoutNode struct{}

func (timeoutNode) Name() string { return "timeout" }

func (timeoutNode) Execute(ctx context.Context, _ orchestrator.NodeInput[struct{}]) (core.NodeOutput, error) {
	<-ctx.Done()
	return core.NodeOutput{}, ctx.Err()
}

type publicFailureNode struct{}

func (publicFailureNode) Name() string { return "public_failure" }

func (publicFailureNode) Execute(context.Context, orchestrator.NodeInput[struct{}]) (core.NodeOutput, error) {
	return core.NodeOutput{}, &core.PublicError{Message: "图片生成失败：共 1 张，1 张超时", Code: "image_generation_timeout", Details: map[string]any{"failed": 1}, ExternalTraceID: "external-trace", Cause: context.DeadlineExceeded}
}

type completingNode struct {
	name string
	text string
}

type waitingMessagesNode struct{}

func (waitingMessagesNode) Name() string { return "wait_messages" }

func (waitingMessagesNode) Execute(context.Context, orchestrator.NodeInput[struct{}]) (core.NodeOutput, error) {
	return core.NodeOutput{Status: core.NodeStatusWaitUser, Messages: []core.Message{
		{Role: "assistant", Content: `{"type":"brief_summary"}`},
		{Role: "assistant", Content: `{"action":"irrelevant","reply":"继续提交资源"}`},
	}}, nil
}

func (n completingNode) Name() string { return n.name }

func (n completingNode) Execute(context.Context, orchestrator.NodeInput[struct{}]) (core.NodeOutput, error) {
	return core.NodeOutput{Status: core.NodeStatusComplete, Messages: []core.Message{{Role: "assistant", Content: n.text}}}, nil
}

type protocolStatusNode struct {
	name   string
	status string
}

func (n protocolStatusNode) Name() string { return n.name }

func (n protocolStatusNode) Execute(context.Context, orchestrator.NodeInput[protocolTestState]) (core.NodeOutput, error) {
	return core.NodeOutput{Status: n.status, Messages: []core.Message{{Role: "assistant", Content: "不应输出"}}}, nil
}

type protocolFallbackNode struct{}

func (protocolFallbackNode) Name() string { return "protocol_fallback" }

func (protocolFallbackNode) Execute(_ context.Context, input orchestrator.NodeInput[protocolTestState]) (core.NodeOutput, error) {
	if !input.State.ProtocolViolation {
		return core.NodeOutput{}, context.Canceled
	}
	return core.NodeOutput{Status: core.NodeStatusComplete, Messages: []core.Message{{Role: "assistant", Content: "协议异常已处理"}}}, nil
}

type protocolTestState struct {
	ProtocolViolation bool `json:"protocol_violation"`
}

type traceRecorder struct {
	mu     sync.Mutex
	events []core.TraceEvent
}

func (r *traceRecorder) Record(_ context.Context, event core.TraceEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *traceRecorder) List(_ string) []core.TraceEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.TraceEvent(nil), r.events...)
}

func TestRunEmitsToolAndFinalEvents(t *testing.T) {
	runner, _ := newTestRunner(t)
	events := make([]orchestrator.Event, 0)
	for event := range runner.Run(context.Background(), "session-1", core.Identity{TenantID: "tenant-1"}, "sales") {
		events = append(events, event)
	}
	if len(events) < 5 {
		t.Fatalf("too few events: %#v", events)
	}
	if events[0].Type != "run.started" {
		t.Fatalf("unexpected first event: %#v", events[0])
	}
	if events[len(events)-1].Type != "message.completed" {
		t.Fatalf("unexpected final event: %#v", events[len(events)-1])
	}
	for i, event := range events {
		if event.Sequence != i+1 {
			t.Fatalf("sequence gap at %d: %#v", i, event)
		}
	}
}

func TestRunEmitsAndPersistsAllWaitingMessages(t *testing.T) {
	nodes := orchestrator.NewNodeRegistry[struct{}]()
	if err := nodes.Register(waitingMessagesNode{}); err != nil {
		t.Fatal(err)
	}
	graph := orchestrator.NewGraph[struct{}]().Start("wait_messages").End("wait_messages")
	definition := orchestrator.Definition[struct{}]{ID: "waiting_messages", WorkflowGraph: graph.Build(), NodeGroup: nodes, Codec: orchestrator.JSONStateCodec[struct{}]{}}
	workflows, err := orchestrator.NewWorkflowRegistry("waiting_messages", definition)
	if err != nil {
		t.Fatal(err)
	}
	store := session.New()
	runner := &orchestrator.Runner{Workflows: workflows, Sessions: store, Checkpoints: orchestrator.NewMemoryCheckpointStore(), MaxSteps: 3}
	identity := core.Identity{TenantID: "tenant", UserID: "user"}
	var completed []string
	for event := range runner.Run(context.Background(), "waiting-messages-session", identity, "input") {
		if event.Type == "message.completed" {
			completed = append(completed, event.Text)
		}
	}
	if len(completed) != 2 || completed[0] != `{"type":"brief_summary"}` || completed[1] != `{"action":"irrelevant","reply":"继续提交资源"}` {
		t.Fatalf("waiting messages were not emitted in order: %#v", completed)
	}
	saved, err := store.Get(context.Background(), identity, "waiting-messages-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) < 3 || saved.Messages[len(saved.Messages)-2].Content != completed[0] || saved.Messages[len(saved.Messages)-1].Content != completed[1] {
		t.Fatalf("waiting messages were not persisted in order: %#v", saved.Messages)
	}
}

func TestRunSelectsExplicitWorkflowAndPersistsKind(t *testing.T) {
	defaultNodes := orchestrator.NewNodeRegistry[struct{}]()
	if err := defaultNodes.Register(completingNode{name: "default_end", text: "default"}); err != nil {
		t.Fatal(err)
	}
	defaultGraph := orchestrator.NewGraph[struct{}]().Start("default_end").End("default_end").Build()
	defaultWorkflow := orchestrator.Definition[struct{}]{ID: "default", WorkflowGraph: defaultGraph, NodeGroup: defaultNodes, Codec: orchestrator.JSONStateCodec[struct{}]{}}
	orderWorkflow, err := testkit.Workflow()
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := orchestrator.NewWorkflowRegistry("default", defaultWorkflow, orderWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	store := session.New()
	checkpoints := orchestrator.NewMemoryCheckpointStore()
	runner := &orchestrator.Runner{Workflows: workflows, Sessions: store, Checkpoints: checkpoints, MaxSteps: 4}
	identity := core.Identity{TenantID: "tenant", UserID: "user"}
	var events []orchestrator.Event
	for event := range runner.RunWorkflow(context.Background(), testkit.WorkflowID, "workflow-session", identity, "1001") {
		events = append(events, event)
	}
	if len(events) == 0 || events[len(events)-1].Text != testkit.Answer {
		t.Fatalf("explicit workflow events = %+v", events)
	}
	latest, err := checkpoints.LatestCheckpoint(context.Background(), identity, events[len(events)-1].RunID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.State.WorkflowKind != testkit.WorkflowID {
		t.Fatalf("checkpoint workflow kind = %q", latest.State.WorkflowKind)
	}
}

func TestRunDoesNotPersistImageURLsInCheckpoint(t *testing.T) {
	nodes := orchestrator.NewNodeRegistry[struct{}]()
	if err := nodes.Register(completingNode{name: "image_safe_end", text: "完成"}); err != nil {
		t.Fatal(err)
	}
	graph := orchestrator.NewGraph[struct{}]().Start("image_safe_end").End("image_safe_end").BudgetTarget("image_safe_end").Build()
	definition := orchestrator.Definition[struct{}]{ID: "image_safe", WorkflowGraph: graph, NodeGroup: nodes, Codec: orchestrator.JSONStateCodec[struct{}]{}}
	workflows, err := orchestrator.NewWorkflowRegistry("image_safe", definition)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := orchestrator.NewMemoryCheckpointStore()
	identity := core.Identity{TenantID: "tenant", UserID: "user"}
	runner := &orchestrator.Runner{Workflows: workflows, Sessions: session.New(), Checkpoints: checkpoints, MaxSteps: 1}
	message := core.Message{Role: "user", Content: "查看图片", Images: []core.ImageInput{{URL: "https://example.com/private.png"}}}
	var runID string
	for event := range runner.RunWorkflowWithMessageAndCredential(context.Background(), "image_safe", "image-session", identity, message, "", "") {
		runID = event.RunID
	}
	checkpoint, err := checkpoints.LatestCheckpoint(context.Background(), identity, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.State.Input.Images) != 0 {
		t.Fatalf("checkpoint 不应保存图片 URL：%+v", checkpoint.State.Input.Images)
	}
}

func TestRunRedirectsInvalidNodeStatusesWithoutSuspending(t *testing.T) {
	for _, status := range []string{core.NodeStatusComplete, core.NodeStatusWaitUser, "unexpected"} {
		t.Run(status, func(t *testing.T) {
			nodes := orchestrator.NewNodeRegistry[protocolTestState]()
			if err := nodes.Register(protocolStatusNode{name: "protocol_source", status: status}); err != nil {
				t.Fatal(err)
			}
			if err := nodes.Register(protocolFallbackNode{}); err != nil {
				t.Fatal(err)
			}
			graph := orchestrator.NewGraph[protocolTestState]().Start("protocol_source")
			graph.From("protocol_source").To("protocol_fallback")
			graph.End("protocol_fallback").FallbackTarget("protocol_fallback").BudgetTarget("protocol_fallback")
			definition := orchestrator.Definition[protocolTestState]{
				ID: "protocol_test", WorkflowGraph: graph.Build(), NodeGroup: nodes,
				Codec: orchestrator.JSONStateCodec[protocolTestState]{},
				Hooks: orchestrator.StateHooks[protocolTestState]{HandleProtocolViolation: func(state *protocolTestState, _ string) {
					state.ProtocolViolation = true
				}},
			}
			workflows, err := orchestrator.NewWorkflowRegistry("protocol_test", definition)
			if err != nil {
				t.Fatal(err)
			}
			checkpoints := orchestrator.NewMemoryCheckpointStore()
			recorder := &traceRecorder{}
			identity := core.Identity{TenantID: "tenant", UserID: "user"}
			runner := &orchestrator.Runner{Workflows: workflows, Sessions: session.New(), Checkpoints: checkpoints, Trace: recorder, MaxSteps: 3}
			var events []orchestrator.Event
			for event := range runner.Run(context.Background(), "protocol-session-"+status, identity, "input") {
				events = append(events, event)
			}
			if len(events) == 0 || events[len(events)-1].Type != "message.completed" || events[len(events)-1].Text != "协议异常已处理" {
				t.Fatalf("协议违规未进入 fallback：%+v", events)
			}
			if countTraceEvents(recorder.List(""), "graph.protocol_violation") != 1 {
				t.Fatalf("协议违规审计数量错误：%+v", recorder.List(""))
			}
			items, err := checkpoints.ListCheckpoints(context.Background(), identity, events[len(events)-1].RunID)
			if err != nil {
				t.Fatal(err)
			}
			for _, checkpoint := range items {
				if checkpoint.Status == core.CheckpointStatusSuspended || checkpoint.Node == "protocol_source" {
					t.Fatalf("非法节点产出不应保存为 checkpoint：%+v", items)
				}
			}
		})
	}
}

func TestRunHidesPreparationProgressAndResultReductionEvents(t *testing.T) {
	runner, _ := newTestRunner(t)
	events := make([]orchestrator.Event, 0)
	for event := range runner.Run(context.Background(), "session-progress", core.Identity{TenantID: "tenant-1"}, "sales") {
		events = append(events, event)
	}
	progress := make([]string, 0)
	for _, event := range events {
		if event.Type == "progress" {
			progress = append(progress, event.Message)
		}
		if event.Type == "tool.result" {
			t.Fatalf("不应向前端发送 tool.result：%+v", event)
		}
	}
	if countMessage(progress, "test_work") != 1 {
		t.Fatalf("技能进度应只展示一次：%v", progress)
	}
	if countMessage(progress, "test_end") != 1 {
		t.Fatalf("上下文进度应只展示一次：%v", progress)
	}
	if len(progress) == 0 || progress[0] != "test_start" {
		t.Fatalf("首个用户可见进度应为意图识别：%v", progress)
	}
}

func countMessage(messages []string, target string) int {
	count := 0
	for _, message := range messages {
		if message == target {
			count++
		}
	}
	return count
}
func TestRunRestoresSessionHistory(t *testing.T) {
	runner, store := newTestRunner(t)
	identity := core.Identity{TenantID: "tenant-1", UserID: "user-1"}
	if _, err := store.Get(context.Background(), identity, "session-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(context.Background(), identity, "session-1", core.Message{Role: "assistant", Content: "上一轮结论"}); err != nil {
		t.Fatal(err)
	}
	for range runner.Run(context.Background(), "session-1", identity, "继续分析") {
	}
	value, err := store.Get(context.Background(), identity, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Messages) != 3 {
		t.Fatalf("期望保留历史、用户输入和最终输出，实际消息数：%d", len(value.Messages))
	}
	if value.Messages[0].Content != "上一轮结论" || value.Messages[1].Content != "继续分析" {
		t.Fatalf("会话历史顺序错误：%+v", value.Messages)
	}
}

type testCompactor struct{}

func (testCompactor) Compact(_ context.Context, previous core.SessionCompact, messages []core.Message) (core.SessionCompact, error) {
	return core.SessionCompact{Content: previous.Content + " compacted"}, nil
}

func TestRunPersistsRollingCompact(t *testing.T) {
	runner, store := newTestRunner(t)
	runner.Compactor = testCompactor{}
	runner.CompactKeepMessages = 4
	identity := core.Identity{TenantID: "tenant-1", UserID: "user-compact"}
	if _, err := store.Get(context.Background(), identity, "compact-session"); err != nil {
		t.Fatal(err)
	}
	for _, message := range []core.Message{
		{Role: "user", Content: "m1"},
		{Role: "assistant", Content: "r1"},
		{Role: "user", Content: "m2"},
		{Role: "assistant", Content: "r2"},
	} {
		if err := store.AppendMessage(context.Background(), identity, "compact-session", message); err != nil {
			t.Fatal(err)
		}
	}
	for range runner.Run(context.Background(), "compact-session", identity, "m3") {
	}
	compact, err := store.GetCompact(context.Background(), identity, "compact-session")
	if err != nil {
		t.Fatal(err)
	}
	if compact.Version != 0 {
		t.Fatalf("compact must not split an incomplete turn: %+v", compact)
	}
	for range runner.Run(context.Background(), "compact-session", identity, "m4") {
	}
	compact, err = store.GetCompact(context.Background(), identity, "compact-session")
	if err != nil {
		t.Fatal(err)
	}
	if compact.Version != 1 || compact.CoveredMessages != 2 || compact.Content == "" {
		t.Fatalf("rolling compact did not persist a complete turn: %+v", compact)
	}
}

func TestRunTurnsStepBudgetExhaustionIntoEvidenceGap(t *testing.T) {
	runner, _ := newTestRunner(t)
	runner.MaxSteps = 1
	events := make([]orchestrator.Event, 0)
	for event := range runner.Run(context.Background(), "session-budget", core.Identity{TenantID: "tenant-1"}, "sales") {
		events = append(events, event)
	}
	if len(events) == 0 || events[len(events)-1].Type != "message.completed" {
		t.Fatalf("预算耗尽应正常返回证据不足结果：%+v", events)
	}
	for _, event := range events {
		if event.Type == "run.failed" {
			t.Fatalf("模型规划失控不应暴露为 run.failed：%+v", events)
		}
	}
	if events[len(events)-1].Text == "" {
		t.Fatal("证据不足结果不能为空")
	}
}

func TestRunTimeoutEmitsOneTerminalFailure(t *testing.T) {
	nodes := orchestrator.NewNodeRegistry[struct{}]()
	if err := nodes.Register(timeoutNode{}); err != nil {
		t.Fatal(err)
	}
	recorder := &traceRecorder{}
	graph := orchestrator.NewGraph[struct{}]().Start("timeout").End("timeout").BudgetTarget("timeout").Build()
	definition := orchestrator.Definition[struct{}]{ID: "timeout", WorkflowGraph: graph, NodeGroup: nodes, Codec: orchestrator.JSONStateCodec[struct{}]{}}
	workflows, err := orchestrator.NewWorkflowRegistry("timeout", definition)
	if err != nil {
		t.Fatal(err)
	}
	runner := &orchestrator.Runner{
		Workflows:      workflows,
		Sessions:       session.New(),
		Trace:          recorder,
		RequestTimeout: 10 * time.Millisecond,
		MaxSteps:       1,
	}
	events := make([]orchestrator.Event, 0)
	for event := range runner.Run(context.Background(), "timeout-session", core.Identity{TenantID: "tenant-1", UserID: "user-1"}, "timeout") {
		events = append(events, event)
	}
	if len(events) == 0 || events[len(events)-1].Type != "run.failed" {
		t.Fatalf("timeout must emit terminal run.failed: %+v", events)
	}
	failures := 0
	for _, event := range events {
		if event.Type == "run.failed" {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("terminal run.failed count = %d, want 1", failures)
	}
	traces := recorder.List("timeout-session")
	if countTraceEvents(traces, "run.failed") != 1 {
		t.Fatalf("persisted terminal run.failed count = %d", countTraceEvents(traces, "run.failed"))
	}
}

func TestRunExposesExplicitPublicFailureMessage(t *testing.T) {
	nodes := orchestrator.NewNodeRegistry[struct{}]()
	if err := nodes.Register(publicFailureNode{}); err != nil {
		t.Fatal(err)
	}
	graph := orchestrator.NewGraph[struct{}]().Start("public_failure").End("public_failure").Build()
	definition := orchestrator.Definition[struct{}]{ID: "public_failure", WorkflowGraph: graph, NodeGroup: nodes, Codec: orchestrator.JSONStateCodec[struct{}]{}}
	workflows, err := orchestrator.NewWorkflowRegistry("public_failure", definition)
	if err != nil {
		t.Fatal(err)
	}
	runner := &orchestrator.Runner{Workflows: workflows, Sessions: session.New(), MaxSteps: 1}
	var last orchestrator.Event
	for event := range runner.Run(context.Background(), "public-failure-session", core.Identity{TenantID: "tenant-1", UserID: "user-1"}, "public_failure") {
		last = event
	}
	if last.Type != "run.failed" || last.Message != "图片生成失败：共 1 张，1 张超时" || last.ErrorCode != "image_generation_timeout" || last.Code != "image_generation_timeout" || last.TraceID != "external-trace" || last.ErrorDetails["failed"] != 1 {
		t.Fatalf("last event = %+v", last)
	}
}

func countTraceEvents(events []core.TraceEvent, kind string) int {
	count := 0
	for _, event := range events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

func newTestRunner(t *testing.T) (*orchestrator.Runner, *session.Store) {
	t.Helper()
	testWorkflow, err := testkit.Workflow()
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := orchestrator.NewWorkflowRegistry(testkit.WorkflowID, testWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	store := session.New()
	return &orchestrator.Runner{Workflows: workflows, Sessions: store, MaxSteps: 16, RequestTimeout: time.Second}, store
}
