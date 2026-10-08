package session

import (
	"context"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestStoreScopesSameSessionIDByTenantAndUser(t *testing.T) {
	store := New()
	ctx := context.Background()
	ownerA := core.Identity{TenantID: "tenant-a", UserID: "user-a"}
	ownerB := core.Identity{TenantID: "tenant-a", UserID: "user-b"}
	if _, err := store.Get(ctx, ownerA, "shared"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(ctx, ownerA, "shared", core.Message{Role: "user", Content: "仅 A 可见"}); err != nil {
		t.Fatal(err)
	}
	other, err := store.Get(ctx, ownerB, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Messages) != 0 {
		t.Fatalf("不同用户不应读取同名会话消息：%+v", other.Messages)
	}
	ownerASessions, err := store.List(ctx, ownerA)
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerASessions) != 1 || ownerASessions[0].ID != "shared" {
		t.Fatalf("会话列表范围错误：%+v", ownerASessions)
	}
}

func TestStorePersistsMessageVisualizations(t *testing.T) {
	store := New()
	ctx := context.Background()
	owner := core.Identity{TenantID: "tenant-a", UserID: "user-a"}
	if _, err := store.Get(ctx, owner, "visualizations"); err != nil {
		t.Fatal(err)
	}
	chart := core.Visualization{ID: "trend", Type: "line", Title: "趋势", Option: map[string]any{"series": []any{}}}
	if err := store.AppendMessage(ctx, owner, "visualizations", core.Message{Role: "assistant", Content: "报告", Visualizations: []core.Visualization{chart}}); err != nil {
		t.Fatal(err)
	}
	session, err := store.Get(ctx, owner, "visualizations")
	if err != nil || len(session.Messages) != 1 || len(session.Messages[0].Visualizations) != 1 || session.Messages[0].Visualizations[0].ID != "trend" {
		t.Fatalf("stored visualizations = %#v, err=%v", session.Messages, err)
	}
}

func TestStoreDeleteRemovesSessionDataAndRespectsOwner(t *testing.T) {
	ctx := context.Background()
	owner := core.Identity{TenantID: "tenant-a", UserID: "user-a"}
	other := core.Identity{TenantID: "tenant-a", UserID: "user-b"}
	store := New()
	if _, err := store.Get(ctx, owner, "to-delete"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(ctx, owner, "to-delete", core.Message{Role: "user", Content: "消息"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEvidenceFacts(ctx, owner, "to-delete", "run-1", []core.EvidenceFact{{ID: "fact"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, other, "to-delete"); err == nil {
		t.Fatal("other owner should not delete session")
	}
	if err := store.Delete(ctx, owner, "to-delete"); err != nil {
		t.Fatal(err)
	}
	if sessions, err := store.List(ctx, owner); err != nil || len(sessions) != 0 {
		t.Fatalf("deleted session remains in list: %+v, %v", sessions, err)
	}
	if err := store.Delete(ctx, owner, "to-delete"); err == nil {
		t.Fatal("deleting missing session should fail")
	}
}

func TestStoreCompareAndSwapCompactScopesByOwner(t *testing.T) {
	store := New()
	ctx := context.Background()
	owner := core.Identity{TenantID: "tenant-a", UserID: "user-a"}
	other := core.Identity{TenantID: "tenant-a", UserID: "user-b"}
	if _, err := store.Get(ctx, owner, "shared"); err != nil {
		t.Fatal(err)
	}
	if saved, err := store.CompareAndSwapCompact(ctx, owner, "shared", 0, core.SessionCompact{CoveredMessages: 3, Content: "已确认目标"}); err != nil || !saved {
		t.Fatalf("save compact = (%v, %v), want (true, nil)", saved, err)
	}
	compact, err := store.GetCompact(ctx, owner, "shared")
	if err != nil || compact.Version != 1 || compact.CoveredMessages != 3 {
		t.Fatalf("stored compact = (%+v, %v)", compact, err)
	}
	if saved, err := store.CompareAndSwapCompact(ctx, owner, "shared", 0, core.SessionCompact{Content: "stale"}); err != nil || saved {
		t.Fatalf("stale save = (%v, %v), want (false, nil)", saved, err)
	}
	if _, err := store.GetCompact(ctx, other, "shared"); err == nil {
		t.Fatal("other owner must not read compact for shared session id")
	}
}

func TestStoreListsLatestEvidenceFactsForSession(t *testing.T) {
	ctx := context.Background()
	owner := core.Identity{TenantID: "tenant-1", UserID: "user-1"}
	store := New()
	if _, err := store.Get(ctx, owner, "latest"); err != nil {
		t.Fatal(err)
	}
	fact := core.EvidenceFact{ID: "fact-2", MetricID: "metric", Value: 8, Unit: "人", StepID: "step", SourceTool: "tool", SourceRef: "ref", SourcePath: "$.value"}
	if err := store.SaveEvidenceFacts(ctx, owner, "latest", "run-1", []core.EvidenceFact{{ID: "fact-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEvidenceFacts(ctx, owner, "latest", "run-2", []core.EvidenceFact{fact}); err != nil {
		t.Fatal(err)
	}
	facts, err := store.ListLatestEvidenceFacts(ctx, owner, "latest")
	if err != nil || len(facts) != 1 || facts[0].ID != "fact-2" {
		t.Fatalf("latest facts = (%+v, %v)", facts, err)
	}
}

func TestStoreScopesEvidenceByRunAndOwner(t *testing.T) {
	store := New()
	ctx := context.Background()
	owner := core.Identity{TenantID: "tenant-a", UserID: "user-a"}
	other := core.Identity{TenantID: "tenant-a", UserID: "user-b"}
	if _, err := store.Get(ctx, owner, "shared"); err != nil {
		t.Fatal(err)
	}
	facts := []core.EvidenceFact{{ID: "fact", Value: 8, SourceTool: "source", SourceRef: "ref", SourcePath: "$.value"}}
	if err := store.SaveEvidenceFacts(ctx, owner, "shared", "run-a", facts); err != nil {
		t.Fatal(err)
	}
	stored, err := store.ListEvidenceFacts(ctx, owner, "shared", "run-a")
	if err != nil || len(stored) != 1 || stored[0].Value != 8 {
		t.Fatalf("stored facts = %#v, %v", stored, err)
	}
	if _, err := store.Get(ctx, other, "shared"); err != nil {
		t.Fatal(err)
	}
	otherFacts, err := store.ListEvidenceFacts(ctx, other, "shared", "run-a")
	if err != nil || len(otherFacts) != 0 {
		t.Fatalf("other owner facts = %#v, %v", otherFacts, err)
	}
}

func TestStorePrunesEvidenceFactsToNewestRuns(t *testing.T) {
	ctx := context.Background()
	owner := core.Identity{TenantID: "tenant-prune", UserID: "user-prune"}
	store := New()
	if _, err := store.Get(ctx, owner, "session-prune"); err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{"run-1", "run-2", "run-3"} {
		fact := core.EvidenceFact{ID: runID, Value: runID}
		if err := store.SaveEvidenceFacts(ctx, owner, "session-prune", runID, []core.EvidenceFact{fact}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.PruneEvidenceFacts(ctx, owner, "session-prune", 1)
	if err != nil || removed != 2 {
		t.Fatalf("prune = (%d, %v), want (2, nil)", removed, err)
	}
	if facts, err := store.ListEvidenceFacts(ctx, owner, "session-prune", "run-1"); err != nil || len(facts) != 0 {
		t.Fatalf("old facts = (%#v, %v)", facts, err)
	}
	latest, err := store.ListLatestEvidenceFacts(ctx, owner, "session-prune")
	if err != nil || len(latest) != 1 || latest[0].ID != "run-3" {
		t.Fatalf("latest facts = (%#v, %v)", latest, err)
	}
}
