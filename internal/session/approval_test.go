package session

import (
	"context"
	"errors"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestApprovalLifecycleIsScopedAndSingleUse(t *testing.T) {
	ctx := context.Background()
	store := New()
	owner := core.Identity{TenantID: "tenant-a", UserID: "user-a"}
	other := core.Identity{TenantID: "tenant-b", UserID: "user-b"}
	if _, err := store.Get(ctx, owner, "session-a"); err != nil {
		t.Fatal(err)
	}
	request := core.ApprovalRequest{
		ID: "approval-1", RunID: "run-1", SessionID: "session-a", Identity: owner,
		Action: core.Action{Type: "transition", Name: "publish", Payload: map[string]any{"draft": "d-1"}, ResumeNode: core.NodeTypeFinalize},
	}
	created, err := store.Request(ctx, request)
	if err != nil || created.Status != "pending" || created.Action.ResumeNode != core.NodeTypeFinalize || created.ArgsHash == "" {
		t.Fatalf("create approval = (%+v, %v)", created, err)
	}
	repeated, err := store.Request(ctx, request)
	if err != nil || repeated.ID != created.ID || repeated.Status != "pending" {
		t.Fatalf("idempotent pending request = (%+v, %v)", repeated, err)
	}
	if _, err := store.Resolve(ctx, other, request.ID, core.ApprovalDecision{Approved: true}); !errors.Is(err, core.ErrApprovalNotFound) {
		t.Fatalf("cross-owner resolve error = %v", err)
	}
	resolved, err := store.Resolve(ctx, owner, request.ID, core.ApprovalDecision{Approved: true})
	if err != nil || resolved.Status != "approved" || resolved.ResolvedAt == nil {
		t.Fatalf("resolve approval = (%+v, %v)", resolved, err)
	}
	if _, err := store.Resolve(ctx, owner, request.ID, core.ApprovalDecision{Approved: false}); !errors.Is(err, core.ErrApprovalAlreadyResolved) {
		t.Fatalf("duplicate resolve error = %v", err)
	}
}

func TestApprovalRequestRequiresExistingSession(t *testing.T) {
	store := New()
	_, err := store.Request(context.Background(), core.ApprovalRequest{
		ID: "approval-missing", RunID: "run", SessionID: "missing", Identity: core.Identity{TenantID: "t", UserID: "u"},
		Action: core.Action{Type: "transition", Name: "next"},
	})
	if err == nil {
		t.Fatal("approval request must require an existing session")
	}
}

func TestApprovalCanBeRestoredToPending(t *testing.T) {
	store := New()
	identity := core.Identity{TenantID: "tenant", UserID: "user"}
	request := core.ApprovalRequest{ID: "approval-rollback", RunID: "run-rollback", SessionID: "session-rollback", Identity: identity, Action: core.Action{Type: "write", Name: "update"}}
	if _, err := store.Get(context.Background(), identity, request.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Request(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(context.Background(), identity, request.ID, core.ApprovalDecision{Approved: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.RestorePending(context.Background(), identity, request.ID); err != nil {
		t.Fatal(err)
	}
	approval, err := store.Request(context.Background(), request)
	if err != nil || approval.Status != "pending" {
		t.Fatalf("restored approval = %+v, err=%v", approval, err)
	}
}
