package api

import (
	"testing"
	"time"
)

func TestRunManagerPrunesOnlyCompletedRuns(t *testing.T) {
	manager := NewRunManagerWithConfig(nil, 2, time.Minute)
	now := time.Now()
	manager.runs["active"] = &managedRun{expiresAt: now.Add(-time.Minute)}
	manager.runs["completed"] = &managedRun{completed: true, expiresAt: now.Add(-time.Minute)}
	manager.pruneLocked(now)
	if _, ok := manager.runs["active"]; !ok {
		t.Fatal("活动运行不能按事件保留期删除")
	}
	if _, ok := manager.runs["completed"]; ok {
		t.Fatal("过期的已完成运行应被删除")
	}
}

func TestRunManagerExpiresCompletedRunWithoutFurtherTraffic(t *testing.T) {
	manager := NewRunManagerWithConfig(nil, 2, time.Minute)
	expiresAt := time.Now().Add(-time.Second)
	manager.runs["completed"] = &managedRun{completed: true, expiresAt: expiresAt}
	manager.expireCompleted("completed", expiresAt, time.Now())
	if _, ok := manager.runs["completed"]; ok {
		t.Fatal("终态到期回调应主动删除运行")
	}
}
