package orchestrator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

// MemoryRunLeaseStore 用于单测，模拟 PostgreSQL 的租户并发计数和租约 CAS。
type MemoryRunLeaseStore struct {
	mu           sync.Mutex
	limits       map[string]int
	active       map[string]int
	leases       map[string]memoryLease
	defaultLimit int
}

type memoryLease struct {
	core.RunLease
	TenantID string
}

func NewMemoryRunLeaseStore(defaultLimit int) *MemoryRunLeaseStore {
	if defaultLimit <= 0 {
		defaultLimit = 5
	}
	return &MemoryRunLeaseStore{limits: make(map[string]int), active: make(map[string]int), leases: make(map[string]memoryLease), defaultLimit: defaultLimit}
}

func (s *MemoryRunLeaseStore) AcquireRunLease(_ context.Context, request core.RunLeaseRequest) (core.RunLease, error) {
	if request.RunID == "" || request.TenantID == "" || request.WorkerID == "" {
		return core.RunLease{}, errors.New("run lease ids are required")
	}
	duration := request.LeaseDuration
	if duration <= 0 {
		duration = 30 * time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for runID, current := range s.leases {
		if current.TenantID == request.TenantID && !current.LeaseUntil.After(now) {
			delete(s.leases, runID)
			if s.active[request.TenantID] > 0 {
				s.active[request.TenantID]--
			}
		}
	}
	if _, exists := s.leases[request.RunID]; exists {
		return core.RunLease{}, errors.New("run already exists")
	}
	limit := request.MaxTenantRuns
	if limit <= 0 {
		limit = s.defaultLimit
	}
	if configured, ok := s.limits[request.TenantID]; ok {
		limit = configured
	} else {
		s.limits[request.TenantID] = limit
	}
	if s.active[request.TenantID] >= limit {
		return core.RunLease{}, errors.New("tenant concurrent run limit reached")
	}
	lease := core.RunLease{RunID: request.RunID, WorkerID: request.WorkerID, Version: 1, LeaseUntil: now.Add(duration)}
	s.leases[request.RunID] = memoryLease{RunLease: lease, TenantID: request.TenantID}
	s.active[request.TenantID]++
	return lease, nil
}

func (s *MemoryRunLeaseStore) RenewRunLease(_ context.Context, lease core.RunLease, duration time.Duration) (core.RunLease, error) {
	if duration <= 0 {
		duration = 30 * time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.leases[lease.RunID]
	if !ok || current.WorkerID != lease.WorkerID || current.Version != lease.Version || !current.LeaseUntil.After(time.Now()) {
		return core.RunLease{}, core.ErrRunLeaseLost
	}
	current.Version++
	current.LeaseUntil = time.Now().Add(duration)
	s.leases[lease.RunID] = current
	return current.RunLease, nil
}

func (s *MemoryRunLeaseStore) ReleaseRunLease(_ context.Context, lease core.RunLease, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.leases[lease.RunID]
	if !ok || current.WorkerID != lease.WorkerID {
		return nil
	}
	delete(s.leases, lease.RunID)
	if s.active[current.TenantID] > 0 {
		s.active[current.TenantID]--
	}
	return nil
}

func (s *MemoryRunLeaseStore) Active(tenantID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[tenantID]
}

var _ core.RunLeaseStore = (*MemoryRunLeaseStore)(nil)
