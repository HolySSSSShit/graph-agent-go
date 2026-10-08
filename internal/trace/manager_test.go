package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestManagerRecordsAndListsTraceEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	manager, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.Record(context.Background(), core.TraceEvent{TraceID: "trace-1", RunID: "run-1", Type: "model.prompt", Prompt: []core.Message{{Role: "user", Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Record(context.Background(), core.TraceEvent{TraceID: "trace-1", RunID: "run-1", Type: "model.output", Output: "world"}); err != nil {
		t.Fatal(err)
	}
	items := manager.List("trace-1")
	if len(items) != 2 || items[0].Sequence != 1 || items[1].Sequence != 2 {
		t.Fatalf("unexpected trace sequence: %+v", items)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event core.TraceEvent
	if err := json.Unmarshal(data[:bytes.IndexByte(data, '\n')], &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "model.prompt" || len(event.Prompt) != 1 {
		t.Fatalf("prompt payload was not persisted: %+v", event)
	}
}

func TestDailyManagerRotatesByConfiguredTimezone(t *testing.T) {
	directory := t.TempDir()
	manager, err := NewDaily(filepath.Join(directory, "audit.log"), "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	first := time.Date(2026, 9, 1, 23, 59, 0, 0, time.UTC)
	second := time.Date(2026, 9, 2, 16, 1, 0, 0, time.UTC)
	if err := manager.Record(context.Background(), core.TraceEvent{TraceID: "day-1", At: first}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Record(context.Background(), core.TraceEvent{TraceID: "day-2", At: second}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"audit-2026-09-02.log", "audit-2026-09-03.log"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("missing rotated audit file %s: %v", name, err)
		}
	}
}

func TestManagerBoundsTraceCountAndEventCount(t *testing.T) {
	manager := newManager()
	manager.maxTraces = 1
	manager.maxEventsPerTrace = 1
	manager.retention = time.Hour
	for _, event := range []core.TraceEvent{
		{TraceID: "first", Type: "one"},
		{TraceID: "first", Type: "two"},
		{TraceID: "second", Type: "three"},
	} {
		if err := manager.Record(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if items := manager.List("first"); len(items) != 0 {
		t.Fatalf("最旧 trace 应被淘汰：%+v", items)
	}
	if items := manager.List("second"); len(items) != 1 || items[0].Type != "three" {
		t.Fatalf("最新 trace 事件异常：%+v", items)
	}
}

func TestManagerBoundsOnlyInMemoryPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	manager, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.maxMemoryEventBytes = 128
	content := string(bytes.Repeat([]byte("x"), 1024))
	if err := manager.Record(context.Background(), core.TraceEvent{TraceID: "large", Type: "model.output", Output: content}); err != nil {
		t.Fatal(err)
	}
	items := manager.List("large")
	if len(items) != 1 || len(items[0].Output) >= len(content) {
		t.Fatalf("内存事件未截断：%+v", items)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte(content)) {
		t.Fatalf("持久化审计应保留原始事件：bytes=%d err=%v", len(data), err)
	}
}

func TestManagerExpiresTerminalTrace(t *testing.T) {
	manager := newManager()
	manager.retention = time.Hour
	if err := manager.Record(context.Background(), core.TraceEvent{TraceID: "done", Type: "message.completed"}); err != nil {
		t.Fatal(err)
	}
	manager.expireTrace("done", 1)
	if items := manager.List("done"); len(items) != 0 {
		t.Fatalf("终态 trace 应被主动清除：%+v", items)
	}
}

func TestManagerKeepsOneExpiryTimerPerTraceAndStopsOnClose(t *testing.T) {
	manager := newManager()
	manager.retention = time.Hour
	for index := 0; index < 3; index++ {
		if err := manager.Record(context.Background(), core.TraceEvent{TraceID: "done", Type: "message.completed"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(manager.expiryTimers) != 1 {
		t.Fatalf("同一 trace 应只保留一个到期定时器，实际为 %d", len(manager.expiryTimers))
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if len(manager.expiryTimers) != 0 {
		t.Fatalf("关闭后仍有到期定时器：%d", len(manager.expiryTimers))
	}
}
