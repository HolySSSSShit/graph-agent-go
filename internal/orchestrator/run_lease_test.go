package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestMemoryRunLeaseStoreEnforcesTenantConcurrency(t *testing.T) {
	store := NewMemoryRunLeaseStore(3)
	const attempts = 20
	leases := make(chan core.RunLease, attempts)
	var group sync.WaitGroup
	for index := 0; index < attempts; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			lease, err := store.AcquireRunLease(context.Background(), core.RunLeaseRequest{
				RunID: "run-" + time.Now().Format("150405.000000") + string(rune('a'+index)), TenantID: "tenant-1",
				UserID: "user", SessionID: "session", WorkerID: "worker-" + string(rune('a'+index)),
				LeaseDuration: time.Minute,
			})
			if err == nil {
				leases <- lease
			}
		}()
	}
	group.Wait()
	close(leases)
	if got := len(leases); got != 3 {
		t.Fatalf("expected exactly three leases, got %d", got)
	}
	if got := store.Active("tenant-1"); got != 3 {
		t.Fatalf("expected three active slots, got %d", got)
	}
	for lease := range leases {
		if err := store.ReleaseRunLease(context.Background(), lease, "completed"); err != nil {
			t.Fatalf("release lease: %v", err)
		}
		if err := store.ReleaseRunLease(context.Background(), lease, "completed"); err != nil {
			t.Fatalf("idempotent release: %v", err)
		}
	}
	if got := store.Active("tenant-1"); got != 0 {
		t.Fatalf("expected all slots released, got %d", got)
	}
}

func TestMemoryRunLeaseStoreUsesOptimisticVersion(t *testing.T) {
	store := NewMemoryRunLeaseStore(1)
	lease, err := store.AcquireRunLease(context.Background(), core.RunLeaseRequest{
		RunID: "run-1", TenantID: "tenant-1", UserID: "user", SessionID: "session", WorkerID: "worker-1", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.RenewRunLease(context.Background(), lease, time.Minute)
	if err != nil || next.Version != lease.Version+1 {
		t.Fatalf("renew lease = %#v, err = %v", next, err)
	}
	if _, err := store.RenewRunLease(context.Background(), lease, time.Minute); err == nil {
		t.Fatal("stale lease version should be rejected")
	}
	if err := store.ReleaseRunLease(context.Background(), next, "completed"); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryRunLeaseStoreReclaimsExpiredSlot(t *testing.T) {
	store := NewMemoryRunLeaseStore(1)
	first, err := store.AcquireRunLease(context.Background(), core.RunLeaseRequest{
		RunID: "run-expired", TenantID: "tenant-1", UserID: "user", SessionID: "session", WorkerID: "worker-1", LeaseDuration: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	second, err := store.AcquireRunLease(context.Background(), core.RunLeaseRequest{
		RunID: "run-reclaimed", TenantID: "tenant-1", UserID: "user", SessionID: "session", WorkerID: "worker-2", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("expired lease should be reclaimed: %v", err)
	}
	if second.RunID == first.RunID || store.Active("tenant-1") != 1 {
		t.Fatalf("unexpected reclaimed lease state: %#v active=%d", second, store.Active("tenant-1"))
	}
}

type flakyRunLeaseStore struct {
	inner    *MemoryRunLeaseStore
	mu       sync.Mutex
	failures int
	calls    int
}

func (s *flakyRunLeaseStore) AcquireRunLease(ctx context.Context, request core.RunLeaseRequest) (core.RunLease, error) {
	return s.inner.AcquireRunLease(ctx, request)
}

func (s *flakyRunLeaseStore) RenewRunLease(ctx context.Context, lease core.RunLease, duration time.Duration) (core.RunLease, error) {
	s.mu.Lock()
	s.calls++
	if s.failures > 0 {
		s.failures--
		s.mu.Unlock()
		return core.RunLease{}, errors.New("temporary database failure")
	}
	s.mu.Unlock()
	return s.inner.RenewRunLease(ctx, lease, duration)
}

func (s *flakyRunLeaseStore) ReleaseRunLease(ctx context.Context, lease core.RunLease, status string) error {
	return s.inner.ReleaseRunLease(ctx, lease, status)
}

func TestRenewLeaseRetriesTransientFailure(t *testing.T) {
	store := &flakyRunLeaseStore{inner: NewMemoryRunLeaseStore(1), failures: 1}
	lease, err := store.inner.AcquireRunLease(context.Background(), core.RunLeaseRequest{
		RunID: "run-retry", TenantID: "tenant-1", UserID: "user", SessionID: "session", WorkerID: "worker", LeaseDuration: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := renewLeaseWithRetry(context.Background(), store, lease, 5*time.Second)
	if err != nil {
		t.Fatalf("renew with transient failure: %v", err)
	}
	if next.Version != lease.Version+1 || store.calls != 2 {
		t.Fatalf("expected one retry and next version, lease=%#v calls=%d", next, store.calls)
	}
}
