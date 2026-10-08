package trace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

// Manager 是默认的进程内 Trace 实现，同时可选地将事件追加写入 JSONL 文件。
// 文件只追加不覆盖，便于按 trace_id/run_id 复盘一次请求的完整过程。
type Manager struct {
	mu                  sync.RWMutex
	events              map[string][]core.TraceEvent
	sequences           map[string]int
	lastSeen            map[string]time.Time
	expiryTimers        map[string]*time.Timer
	expiryVersions      map[string]uint64
	nextExpiryVersion   uint64
	file                *os.File
	writer              *bufio.Writer
	sinks               []TraceSink
	basePath            string
	daily               bool
	location            *time.Location
	day                 string
	maxTraces           int
	maxEventsPerTrace   int
	maxMemoryEventBytes int
	retention           time.Duration
}

const (
	defaultMaxTraces           = 128
	defaultMaxEventsPerTrace   = 128
	defaultMaxMemoryEventBytes = 8 << 10
	defaultTraceRetention      = 10 * time.Minute
)

// TraceSink 在管理器分配运行内序号后接收事件。
// 各 Sink 必须自行负责持久化和脱敏边界。
type TraceSink interface {
	Record(context.Context, core.TraceEvent) error
	Close() error
}

// New 创建 Trace 管理器。filePath 为空时仅保存在内存中。
func New(filePath string, sinks ...TraceSink) (*Manager, error) {
	manager := newManager(sinks...)
	if filePath == "" {
		return manager, nil
	}
	if directory := filepath.Dir(filePath); directory != "." {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	manager.file = file
	manager.writer = bufio.NewWriter(file)
	return manager, nil
}

// NewDaily 创建按本地午夜轮转文件的 Trace 管理器。
// filePath 作为基础文件名使用：audit.log 会变为 audit-YYYY-MM-DD.log。
func NewDaily(filePath, timezone string, sinks ...TraceSink) (*Manager, error) {
	location := time.Local
	if timezone == "" {
		timezone = core.DefaultTimezone
	}
	if loaded, err := time.LoadLocation(timezone); err == nil {
		location = loaded
	}
	manager := newManager(sinks...)
	manager.basePath = filePath
	manager.daily = filePath != ""
	manager.location = location
	if filePath == "" {
		return manager, nil
	}
	if err := manager.rotateLocked(time.Now()); err != nil {
		return nil, err
	}
	return manager, nil
}

func newManager(sinks ...TraceSink) *Manager {
	return &Manager{
		events: make(map[string][]core.TraceEvent), sequences: make(map[string]int), lastSeen: make(map[string]time.Time),
		expiryTimers: make(map[string]*time.Timer), expiryVersions: make(map[string]uint64),
		sinks: append([]TraceSink(nil), sinks...), maxTraces: defaultMaxTraces,
		maxEventsPerTrace: defaultMaxEventsPerTrace, maxMemoryEventBytes: defaultMaxMemoryEventBytes,
		retention: defaultTraceRetention,
	}
}

func (m *Manager) Record(ctx context.Context, event core.TraceEvent) error {
	if m == nil {
		return errors.New("trace manager is nil")
	}
	if event.TraceID == "" {
		return errors.New("trace id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if m.daily && m.writer != nil {
		if err := m.rotateLocked(event.At); err != nil {
			return err
		}
	}
	// Trace 使用独立序号；内存事件可淘汰，序号因此单独维护。
	event.Sequence = m.sequences[event.TraceID] + 1
	m.sequences[event.TraceID] = event.Sequence
	m.ensureTraceCapacityLocked(event.TraceID)
	memoryEvent := boundMemoryEvent(event, m.maxMemoryEventBytes)
	m.events[event.TraceID] = append(m.events[event.TraceID], memoryEvent)
	if len(m.events[event.TraceID]) > m.maxEventsPerTrace {
		m.events[event.TraceID] = append([]core.TraceEvent(nil), m.events[event.TraceID][len(m.events[event.TraceID])-m.maxEventsPerTrace:]...)
	}
	m.lastSeen[event.TraceID] = event.At
	if isTerminalTraceEvent(event.Type) {
		m.scheduleExpiryLocked(event.TraceID, event.Sequence)
	}
	var result error
	if m.writer != nil {
		data, err := json.Marshal(event)
		if err != nil {
			result = errors.Join(result, err)
		} else if _, err := m.writer.Write(append(data, '\n')); err != nil {
			result = errors.Join(result, err)
		} else if err := m.writer.Flush(); err != nil {
			result = errors.Join(result, err)
		}
	}
	for _, sink := range m.sinks {
		if sink == nil {
			continue
		}
		if err := sink.Record(ctx, event); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (m *Manager) ensureTraceCapacityLocked(current string) {
	if _, exists := m.events[current]; exists || m.maxTraces < 1 || len(m.events) < m.maxTraces {
		return
	}
	var oldestID string
	var oldest time.Time
	for traceID, seen := range m.lastSeen {
		if oldestID == "" || seen.Before(oldest) {
			oldestID, oldest = traceID, seen
		}
	}
	delete(m.events, oldestID)
	delete(m.sequences, oldestID)
	delete(m.lastSeen, oldestID)
	m.stopExpiryLocked(oldestID)
}

func (m *Manager) scheduleExpiryLocked(traceID string, sequence int) {
	m.stopExpiryLocked(traceID)
	m.nextExpiryVersion++
	version := m.nextExpiryVersion
	m.expiryVersions[traceID] = version
	m.expiryTimers[traceID] = time.AfterFunc(m.retention, func() {
		m.expireTrace(traceID, sequence, version)
	})
}

func (m *Manager) stopExpiryLocked(traceID string) {
	if timer := m.expiryTimers[traceID]; timer != nil {
		timer.Stop()
	}
	delete(m.expiryTimers, traceID)
	delete(m.expiryVersions, traceID)
}

func (m *Manager) expireTrace(traceID string, sequence int, version ...uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(version) > 0 && m.expiryVersions[traceID] != version[0] {
		return
	}
	m.stopExpiryLocked(traceID)
	if m.sequences[traceID] != sequence {
		return
	}
	delete(m.events, traceID)
	delete(m.sequences, traceID)
	delete(m.lastSeen, traceID)
}

func isTerminalTraceEvent(kind string) bool {
	switch kind {
	case "message.completed", "run.failed", "approval.required":
		return true
	default:
		return false
	}
}

func boundMemoryEvent(event core.TraceEvent, limit int) core.TraceEvent {
	if limit < 1 {
		return core.TraceEvent{TraceID: event.TraceID, RunID: event.RunID, SessionID: event.SessionID, TenantID: event.TenantID, UserID: event.UserID, Sequence: event.Sequence, Type: event.Type, Stage: event.Stage, Status: event.Status, At: event.At}
	}
	if data, err := json.Marshal(event); err == nil && len(data) <= limit {
		return event
	}
	event.Prompt = nil
	event.ToolArguments = nil
	event.Metadata = nil
	event.Message = truncateMemoryText(event.Message, limit/3)
	event.Output = truncateMemoryText(event.Output, limit/3)
	return event
}

func truncateMemoryText(value string, limit int) string {
	if limit < 1 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func (m *Manager) rotateLocked(at time.Time) error {
	if !m.daily || m.basePath == "" {
		return nil
	}
	location := m.location
	if location == nil {
		location = time.Local
	}
	day := at.In(location).Format("2006-01-02")
	if m.writer != nil && m.day == day {
		return nil
	}
	if m.writer != nil {
		if err := m.writer.Flush(); err != nil {
			return err
		}
		if err := m.file.Close(); err != nil {
			return err
		}
		m.file = nil
		m.writer = nil
	}
	directory := filepath.Dir(m.basePath)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return err
		}
	}
	ext := filepath.Ext(m.basePath)
	stem := strings.TrimSuffix(filepath.Base(m.basePath), ext)
	if stem == "" {
		stem = filepath.Base(m.basePath)
	}
	path := filepath.Join(directory, stem+"-"+day+ext)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	m.file = file
	m.writer = bufio.NewWriter(file)
	m.day = day
	return nil
}

func (m *Manager) List(traceID string) []core.TraceEvent {
	if m == nil || traceID == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.events[traceID]
	return append([]core.TraceEvent(nil), items...)
}

// Close 刷新并关闭文件；仅由服务退出流程调用。
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for traceID := range m.expiryTimers {
		m.stopExpiryLocked(traceID)
	}
	if m.writer != nil {
		if err := m.writer.Flush(); err != nil {
			return err
		}
	}
	var result error
	if m.file != nil {
		result = errors.Join(result, m.file.Close())
	}
	for _, sink := range m.sinks {
		if sink == nil {
			continue
		}
		result = errors.Join(result, sink.Close())
	}
	return result
}
