package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"
)

const (
	defaultRunEventCapacity = 256
	defaultRunRetention     = 10 * time.Minute
)

// RunManager 保存短时间内的运行事件，让创建请求和 SSE 订阅请求解耦。
// 该实现只存在于当前进程，重启后运行记录和事件缓冲都会丢失。
type RunManager struct {
	runner    *orchestrator.Runner
	mu        sync.Mutex
	runs      map[string]*managedRun
	reserved  map[string]struct{}
	maxEvents int
	retention time.Duration
}

type managedRun struct {
	id          string
	createdAt   time.Time
	expiresAt   time.Time
	events      []orchestrator.Event
	subscribers map[*runSubscription]struct{}
	completed   bool
	cancel      context.CancelFunc
	identity    core.Identity
	credential  string
}

type runSubscription struct {
	events        chan orchestrator.Event
	afterSequence int
	closeOnce     sync.Once
}

// NewRunManager 创建使用默认事件容量和保留时长的运行管理器。
func NewRunManager(runner *orchestrator.Runner) *RunManager {
	return NewRunManagerWithConfig(runner, defaultRunEventCapacity, defaultRunRetention)
}

// NewRunManagerWithConfig 用于测试和后续配置化运行缓冲策略。
func NewRunManagerWithConfig(runner *orchestrator.Runner, maxEvents int, retention time.Duration) *RunManager {
	if maxEvents < 1 {
		maxEvents = defaultRunEventCapacity
	}
	if retention <= 0 {
		retention = defaultRunRetention
	}
	return &RunManager{
		runner:    runner,
		runs:      make(map[string]*managedRun),
		reserved:  make(map[string]struct{}),
		maxEvents: maxEvents,
		retention: retention,
	}
}

// Start 创建运行并在后台执行节点图，返回可供 EventSource 使用的 run_id。
func (m *RunManager) Start(sessionID string, identity core.Identity, input string) (string, error) {
	return m.StartWithCredential(sessionID, identity, "", input)
}

func (m *RunManager) StartWithCredential(sessionID string, identity core.Identity, credential, input string) (string, error) {
	return m.StartMessageWithCredential(sessionID, identity, credential, core.Message{Role: "user", Content: input})
}

func (m *RunManager) StartMessageWithCredential(sessionID string, identity core.Identity, credential string, input core.Message) (string, error) {
	return m.StartWorkflowMessageWithCredential(sessionID, "", identity, credential, input)
}

// StartWorkflowMessageWithCredential 启动指定 Workflow；空 kind 由 Runner 注册表解析为默认项。
func (m *RunManager) StartWorkflowMessageWithCredential(sessionID, workflowKind string, identity core.Identity, credential string, input core.Message) (string, error) {
	if m == nil || m.runner == nil {
		return "", errors.New("run manager is not configured")
	}
	if m.runner.Workflows == nil {
		return "", errors.New("workflow registry is not configured")
	}
	if _, err := m.runner.Workflows.Resolve(workflowKind); err != nil {
		return "", err
	}
	now := time.Now()
	runID := m.reserveRunID(now)
	run := &managedRun{
		id:          runID,
		createdAt:   now,
		expiresAt:   now.Add(m.retention),
		events:      make([]orchestrator.Event, 0, m.maxEvents),
		subscribers: make(map[*runSubscription]struct{}),
		identity:    identity,
		credential:  credential,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	m.mu.Lock()
	m.runs[runID] = run
	delete(m.reserved, runID)
	m.mu.Unlock()

	go func() {
		for event := range m.runner.RunWorkflowWithMessageAndCredential(runCtx, workflowKind, sessionID, identity, input, runID, credential) {
			m.publish(runID, event)
		}
		m.finish(runID)
	}()
	return runID, nil
}

// ResumeWithCredential 使用旧运行的挂起 checkpoint 创建新的运行实例。
// 新运行只继承 checkpoint 状态，运行计数器由 Runner 重新初始化。
func (m *RunManager) ResumeWithCredential(previousRunID string, identity core.Identity, credential string) (string, error) {
	if m == nil || m.runner == nil {
		return "", errors.New("run manager is not configured")
	}
	if previousRunID == "" || identity.TenantID == "" || identity.UserID == "" {
		return "", errors.New("previous run and owner are required")
	}
	now := time.Now()
	runID := m.reserveRunID(now)
	run := &managedRun{
		id:          runID,
		createdAt:   now,
		expiresAt:   now.Add(m.retention),
		events:      make([]orchestrator.Event, 0, m.maxEvents),
		subscribers: make(map[*runSubscription]struct{}),
		identity:    identity,
		credential:  credential,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	m.mu.Lock()
	m.runs[runID] = run
	delete(m.reserved, runID)
	m.mu.Unlock()
	go func() {
		for event := range m.runner.ResumeWithID(runCtx, identity, previousRunID, runID, credential) {
			m.publish(runID, event)
		}
		m.finish(runID)
	}()
	return runID, nil
}

// Cancel 请求停止指定运行。运行结束后再次取消返回 false。
func (m *RunManager) Cancel(runID string) bool {
	return m.cancel(runID, core.Identity{}, false)
}

func (m *RunManager) CancelForIdentity(runID string, identity core.Identity) bool {
	return m.cancel(runID, identity, true)
}

func (m *RunManager) cancel(runID string, identity core.Identity, checkOwner bool) bool {
	if m == nil || runID == "" {
		return false
	}
	m.mu.Lock()
	run, ok := m.runs[runID]
	if !ok || run.completed || (checkOwner && !sameIdentity(run.identity, identity)) {
		m.mu.Unlock()
		return false
	}
	cancel := run.cancel
	m.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// Subscribe 返回指定序号之后的历史事件和实时事件 channel。
// unsubscribe 必须由 HTTP handler 在请求结束时调用。
func (m *RunManager) Subscribe(runID string, afterSequence int) ([]orchestrator.Event, <-chan orchestrator.Event, func(), error) {
	return m.subscribe(runID, afterSequence, core.Identity{}, false)
}

func (m *RunManager) SubscribeForIdentity(runID string, afterSequence int, identity core.Identity) ([]orchestrator.Event, <-chan orchestrator.Event, func(), error) {
	return m.subscribe(runID, afterSequence, identity, true)
}

func (m *RunManager) subscribe(runID string, afterSequence int, identity core.Identity, checkOwner bool) ([]orchestrator.Event, <-chan orchestrator.Event, func(), error) {
	if m == nil {
		return nil, nil, func() {}, errors.New("run manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(time.Now())
	run, ok := m.runs[runID]
	if !ok || (checkOwner && !sameIdentity(run.identity, identity)) {
		return nil, nil, func() {}, fmt.Errorf("run %q not found", runID)
	}
	if afterSequence < 0 {
		afterSequence = 0
	}
	replay := make([]orchestrator.Event, 0, len(run.events))
	for _, event := range run.events {
		if event.Sequence > afterSequence {
			replay = append(replay, event)
		}
	}
	subscription := &runSubscription{
		events:        make(chan orchestrator.Event, m.maxEvents),
		afterSequence: afterSequence,
	}
	if run.completed {
		closeSubscription(subscription)
	} else {
		run.subscribers[subscription] = struct{}{}
	}
	unsubscribe := func() {
		m.unsubscribe(runID, subscription)
	}
	return replay, subscription.events, unsubscribe, nil
}

func sameIdentity(left, right core.Identity) bool {
	return left.TenantID != "" && left.TenantID == right.TenantID && left.UserID != "" && left.UserID == right.UserID
}

func (m *RunManager) reserveRunID(now time.Time) string {
	base := fmt.Sprintf("run-%d", now.UnixNano())
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(now)
	for index := 0; ; index++ {
		candidate := base
		if index > 0 {
			candidate = fmt.Sprintf("%s-%d", base, index)
		}
		if _, exists := m.runs[candidate]; exists {
			continue
		}
		if _, exists := m.reserved[candidate]; exists {
			continue
		}
		m.reserved[candidate] = struct{}{}
		return candidate
	}
}

func (m *RunManager) publish(runID string, event orchestrator.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok || run.completed {
		return
	}
	if len(run.events) >= m.maxEvents {
		run.events = append(run.events[1:], event)
	} else {
		run.events = append(run.events, event)
	}
	for subscriber := range run.subscribers {
		if event.Sequence <= subscriber.afterSequence {
			continue
		}
		select {
		case subscriber.events <- event:
		default:
			// 历史缓冲仍保留完整事件，慢订阅者可通过 Last-Event-ID 重连补齐。
		}
	}
}

func (m *RunManager) finish(runID string) {
	m.mu.Lock()
	run, ok := m.runs[runID]
	if !ok || run.completed {
		m.mu.Unlock()
		return
	}
	run.completed = true
	if run.cancel != nil {
		run.cancel()
		run.cancel = nil
	}
	run.expiresAt = time.Now().Add(m.retention)
	for subscriber := range run.subscribers {
		closeSubscription(subscriber)
		delete(run.subscribers, subscriber)
	}
	expiresAt := run.expiresAt
	m.mu.Unlock()
	time.AfterFunc(m.retention, func() {
		m.expireCompleted(runID, expiresAt, time.Now())
	})
}

func (m *RunManager) unsubscribe(runID string, subscription *runSubscription) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run, ok := m.runs[runID]; ok {
		delete(run.subscribers, subscription)
	}
	closeSubscription(subscription)
}

func closeSubscription(subscription *runSubscription) {
	subscription.closeOnce.Do(func() {
		close(subscription.events)
	})
}

func (m *RunManager) pruneLocked(now time.Time) {
	for runID, run := range m.runs {
		if run.completed && !now.Before(run.expiresAt) {
			for subscriber := range run.subscribers {
				closeSubscription(subscriber)
			}
			delete(m.runs, runID)
		}
	}
}

func (m *RunManager) expireCompleted(runID string, expiresAt, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok || !run.completed || !run.expiresAt.Equal(expiresAt) || now.Before(expiresAt) {
		return
	}
	for subscriber := range run.subscribers {
		closeSubscription(subscriber)
	}
	delete(m.runs, runID)
}
