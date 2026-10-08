package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"sync"
	"time"
)

type key struct {
	tenantID  string
	userID    string
	sessionID string
}

type Store struct {
	mu           sync.Mutex
	sessions     map[key]core.Session
	compacts     map[key]core.SessionCompact
	evidence     map[evidenceKey][]core.EvidenceFact
	evidenceRuns map[key][]string
	approvals    map[string]core.Approval
}

type evidenceKey struct {
	key
	runID string
}

func (s *Store) List(_ context.Context, identity core.Identity) ([]core.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]core.Session, 0)
	for _, value := range s.sessions {
		if value.Identity.TenantID == identity.TenantID && value.Identity.UserID == identity.UserID {
			result = append(result, value)
		}
	}
	return result, nil
}

func New() *Store {
	return &Store{sessions: map[key]core.Session{}, compacts: map[key]core.SessionCompact{}, evidence: map[evidenceKey][]core.EvidenceFact{}, evidenceRuns: map[key][]string{}, approvals: map[string]core.Approval{}}
}
func (s *Store) Get(_ context.Context, identity core.Identity, id string) (core.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(identity, id)
	v, ok := s.sessions[key]
	if !ok {
		v = core.Session{ID: id, Identity: identity, State: "new"}
		s.sessions[key] = v
	}
	return v, nil
}

func (s *Store) Delete(_ context.Context, identity core.Identity, id string) error {
	if err := validateOwner(identity, id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	base := sessionKey(identity, id)
	if _, ok := s.sessions[base]; !ok {
		return errors.New("session not found")
	}
	delete(s.sessions, base)
	delete(s.compacts, base)
	delete(s.evidenceRuns, base)
	for approvalID, approval := range s.approvals {
		if approval.Identity.TenantID == identity.TenantID && approval.Identity.UserID == identity.UserID && approval.SessionID == id {
			delete(s.approvals, approvalID)
		}
	}
	for key := range s.evidence {
		if key.key == base {
			delete(s.evidence, key)
		}
	}
	return nil
}
func (s *Store) AppendMessage(_ context.Context, identity core.Identity, id string, m core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(identity, id)
	v, ok := s.sessions[key]
	if !ok {
		return errors.New("session not found")
	}
	m.Visualizations = append([]core.Visualization(nil), m.Visualizations...)
	v.Messages = append(v.Messages, m)
	s.sessions[key] = v
	return nil
}
func (s *Store) UpdateState(_ context.Context, identity core.Identity, id, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(identity, id)
	v, ok := s.sessions[key]
	if !ok {
		return errors.New("session not found")
	}
	v.State = state
	s.sessions[key] = v
	return nil
}

func (s *Store) GetCompact(_ context.Context, identity core.Identity, id string) (core.SessionCompact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionKey(identity, id)]; !ok {
		return core.SessionCompact{}, errors.New("session not found")
	}
	return s.compacts[sessionKey(identity, id)], nil
}

func (s *Store) CompareAndSwapCompact(_ context.Context, identity core.Identity, id string, version int64, compact core.SessionCompact) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(identity, id)
	if _, ok := s.sessions[key]; !ok {
		return false, errors.New("session not found")
	}
	current := s.compacts[key]
	if current.Version != version {
		return false, nil
	}
	compact.Version = version + 1
	s.compacts[key] = compact
	return true, nil
}

func sessionKey(identity core.Identity, sessionID string) key {
	return key{tenantID: identity.TenantID, userID: identity.UserID, sessionID: sessionID}
}

func (s *Store) SaveEvidenceFacts(_ context.Context, identity core.Identity, sessionID, runID string, facts []core.EvidenceFact) error {
	if err := validateOwner(identity, sessionID); err != nil {
		return err
	}
	if runID == "" {
		return errors.New("run id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	base := sessionKey(identity, sessionID)
	if _, ok := s.sessions[base]; !ok {
		return errors.New("session not found")
	}
	s.evidence[evidenceKey{key: base, runID: runID}] = append([]core.EvidenceFact(nil), facts...)
	found := false
	for _, existing := range s.evidenceRuns[base] {
		if existing == runID {
			found = true
			break
		}
	}
	if !found {
		s.evidenceRuns[base] = append(s.evidenceRuns[base], runID)
	}
	return nil
}

func (s *Store) ListEvidenceFacts(_ context.Context, identity core.Identity, sessionID, runID string) ([]core.EvidenceFact, error) {
	if err := validateOwner(identity, sessionID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	base := sessionKey(identity, sessionID)
	if _, ok := s.sessions[base]; !ok {
		return nil, errors.New("session not found")
	}
	return append([]core.EvidenceFact(nil), s.evidence[evidenceKey{key: base, runID: runID}]...), nil
}

func (s *Store) ListLatestEvidenceFacts(_ context.Context, identity core.Identity, sessionID string) ([]core.EvidenceFact, error) {
	if err := validateOwner(identity, sessionID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	base := sessionKey(identity, sessionID)
	if _, ok := s.sessions[base]; !ok {
		return nil, errors.New("session not found")
	}
	runs := s.evidenceRuns[base]
	if len(runs) == 0 {
		return nil, nil
	}
	return append([]core.EvidenceFact(nil), s.evidence[evidenceKey{key: base, runID: runs[len(runs)-1]}]...), nil
}

func (s *Store) PruneEvidenceFacts(_ context.Context, identity core.Identity, sessionID string, keepRuns int) (int, error) {
	if err := validateOwner(identity, sessionID); err != nil {
		return 0, err
	}
	if keepRuns < 1 {
		keepRuns = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	base := sessionKey(identity, sessionID)
	runs := s.evidenceRuns[base]
	if len(runs) <= keepRuns {
		return 0, nil
	}
	remove := runs[:len(runs)-keepRuns]
	removed := 0
	for _, runID := range remove {
		itemKey := evidenceKey{key: base, runID: runID}
		removed += len(s.evidence[itemKey])
		delete(s.evidence, itemKey)
	}
	s.evidenceRuns[base] = append([]string(nil), runs[len(runs)-keepRuns:]...)
	return removed, nil
}

func (s *Store) Request(_ context.Context, request core.ApprovalRequest) (core.Approval, error) {
	if request.ID == "" || request.RunID == "" || request.SessionID == "" {
		return core.Approval{}, errors.New("approval id, run and session are required")
	}
	if err := validateOwner(request.Identity, request.SessionID); err != nil {
		return core.Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionKey(request.Identity, request.SessionID)]; !ok {
		return core.Approval{}, errors.New("session not found")
	}
	if existing, ok := s.approvals[request.ID]; ok {
		if existing.Identity.TenantID != request.Identity.TenantID || existing.Identity.UserID != request.Identity.UserID || existing.SessionID != request.SessionID {
			return core.Approval{}, core.ErrApprovalNotFound
		}
		if existing.Status == "pending" {
			return existing, nil
		}
		return core.Approval{}, core.ErrApprovalAlreadyResolved
	}
	now := time.Now().UTC()
	approval := core.Approval{ID: request.ID, RunID: request.RunID, SessionID: request.SessionID, Identity: request.Identity, Action: cloneAction(request.Action), ArgsHash: approvalArgsHash(request.Action.Payload), Reason: request.Action.Reason, Summary: request.Summary, Status: "pending", CreatedAt: now, UpdatedAt: now}
	s.approvals[approval.ID] = approval
	return approval, nil
}

func (s *Store) Resolve(_ context.Context, identity core.Identity, id string, decision core.ApprovalDecision) (core.Approval, error) {
	if identity.TenantID == "" || identity.UserID == "" || id == "" {
		return core.Approval{}, errors.New("tenant, user and approval id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	if !ok || approval.Identity.TenantID != identity.TenantID || approval.Identity.UserID != identity.UserID {
		return core.Approval{}, core.ErrApprovalNotFound
	}
	if approval.Status != "pending" {
		return core.Approval{}, core.ErrApprovalAlreadyResolved
	}
	if decision.Approved {
		approval.Status = "approved"
	} else {
		approval.Status = "rejected"
	}
	now := time.Now().UTC()
	approval.UpdatedAt = now
	approval.ResolvedAt = &now
	s.approvals[id] = approval
	return approval, nil
}

func (s *Store) RestorePending(_ context.Context, identity core.Identity, id string) error {
	if s == nil || identity.TenantID == "" || identity.UserID == "" || id == "" {
		return errors.New("tenant, user and approval id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	if !ok || approval.Identity.TenantID != identity.TenantID || approval.Identity.UserID != identity.UserID {
		return core.ErrApprovalNotFound
	}
	approval.Status = "pending"
	approval.ResolvedAt = nil
	approval.UpdatedAt = time.Now()
	s.approvals[id] = approval
	return nil
}

func cloneAction(action core.Action) core.Action {
	copy := action
	if action.Payload != nil {
		copy.Payload = make(map[string]any, len(action.Payload))
		for key, value := range action.Payload {
			copy.Payload[key] = value
		}
	}
	return copy
}

func approvalArgsHash(args map[string]any) string {
	data, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var _ core.EvidenceFactStore = (*Store)(nil)
var _ core.EvidenceFactPruner = (*Store)(nil)
var _ core.ApprovalService = (*Store)(nil)
