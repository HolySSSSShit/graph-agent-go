package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// MemoryCheckpointStore 保存完整快照，适合开发调试和单元测试。
// 生产实现可以把同一结构写入 PostgreSQL 或对象存储；回放协议不依赖存储介质。
type MemoryCheckpointStore struct {
	mu      sync.RWMutex
	entries map[string][]core.Checkpoint
}

func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{entries: make(map[string][]core.Checkpoint)}
}

func (s *MemoryCheckpointStore) SaveCheckpoint(_ context.Context, checkpoint core.Checkpoint) error {
	if s == nil {
		return errors.New("checkpoint store is nil")
	}
	if checkpoint.RunID == "" {
		return errors.New("checkpoint run id is required")
	}
	if checkpoint.Identity.TenantID == "" || checkpoint.Identity.UserID == "" {
		return errors.New("checkpoint owner is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string][]core.Checkpoint)
	}
	checkpoint.TraceEvents = slices.Clone(checkpoint.TraceEvents)
	checkpoint.State = cloneRunState(checkpoint.State)
	entries := s.entries[checkpoint.RunID]
	for index := range entries {
		if entries[index].ID == checkpoint.ID {
			entries[index] = checkpoint
			s.entries[checkpoint.RunID] = entries
			return nil
		}
	}
	s.entries[checkpoint.RunID] = append(entries, checkpoint)
	return nil
}

func (s *MemoryCheckpointStore) ListCheckpoints(_ context.Context, identity core.Identity, runID string) ([]core.Checkpoint, error) {
	if s == nil {
		return nil, errors.New("checkpoint store is nil")
	}
	if identity.TenantID == "" || identity.UserID == "" {
		return nil, errors.New("checkpoint owner is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := s.entries[runID]
	result := make([]core.Checkpoint, 0, len(entries))
	for _, entry := range entries {
		if entry.Identity.TenantID == identity.TenantID && entry.Identity.UserID == identity.UserID {
			result = append(result, entry)
		}
	}
	for index := range result {
		result[index].TraceEvents = slices.Clone(result[index].TraceEvents)
		result[index].State = cloneRunState(result[index].State)
	}
	return result, nil
}

func (s *MemoryCheckpointStore) LatestCheckpoint(ctx context.Context, identity core.Identity, runID string) (core.Checkpoint, error) {
	entries, err := s.ListCheckpoints(ctx, identity, runID)
	if err != nil {
		return core.Checkpoint{}, err
	}
	if len(entries) == 0 {
		return core.Checkpoint{}, errors.New("checkpoint not found")
	}
	return entries[len(entries)-1], nil
}

func (s *MemoryCheckpointStore) LatestSuspendedCheckpoint(_ context.Context, identity core.Identity, sessionID string) (core.Checkpoint, error) {
	if s == nil {
		return core.Checkpoint{}, errors.New("checkpoint store is nil")
	}
	if identity.TenantID == "" || identity.UserID == "" || sessionID == "" {
		return core.Checkpoint{}, errors.New("checkpoint owner and session are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found core.Checkpoint
	for _, entries := range s.entries {
		for _, entry := range entries {
			if entry.Identity.TenantID != identity.TenantID || entry.Identity.UserID != identity.UserID || entry.SessionID != sessionID || entry.Status != core.CheckpointStatusSuspended {
				continue
			}
			if newerCheckpoint(entry, found) {
				found = entry
			}
		}
	}
	if found.ID == "" {
		return core.Checkpoint{}, errors.New("suspended checkpoint not found")
	}
	found.TraceEvents = slices.Clone(found.TraceEvents)
	found.State = cloneRunState(found.State)
	return found, nil
}

func newerCheckpoint(candidate, current core.Checkpoint) bool {
	if current.ID == "" {
		return true
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	if candidate.Sequence != current.Sequence {
		return candidate.Sequence > current.Sequence
	}
	return candidate.ID > current.ID
}

func (s *MemoryCheckpointStore) UpdateCheckpointStatus(_ context.Context, identity core.Identity, checkpointID, status string) error {
	if s == nil {
		return errors.New("checkpoint store is nil")
	}
	if identity.TenantID == "" || identity.UserID == "" || checkpointID == "" || status == "" {
		return errors.New("checkpoint owner, id and status are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for runID, entries := range s.entries {
		for index := range entries {
			entry := &entries[index]
			if entry.ID != checkpointID {
				continue
			}
			if entry.Identity.TenantID != identity.TenantID || entry.Identity.UserID != identity.UserID {
				return errors.New("checkpoint not found")
			}
			entry.Status = status
			s.entries[runID] = entries
			return nil
		}
	}
	return errors.New("checkpoint not found")
}

// cloneRunState 复制检查点中会被运行流程继续修改的切片、map 和指针。
// 动态工具 JSON 保持原值，节点不得原地修改这类 payload。
func cloneRunState(state core.RunState) core.RunState {
	copy := state
	copy.WorkflowPayload = json.RawMessage(append([]byte(nil), state.WorkflowPayload...))
	copy.WorkflowState.Data = nil
	copy.Session.Messages = slices.Clone(state.Session.Messages)
	copy.Metrics.NodeVisits = cloneNodeVisits(state.Metrics.NodeVisits)
	copy.Metrics.NodeDurationsMS = cloneNodeDurations(state.Metrics.NodeDurationsMS)
	copy.Route.History = slices.Clone(state.Route.History)
	copy.Control.ApprovedActionHashes = cloneSet(state.Control.ApprovedActionHashes)
	if state.Control.PendingAction != nil {
		action := *state.Control.PendingAction
		copy.Control.PendingAction = &action
	}
	if state.Control.Approval != nil {
		approval := *state.Control.Approval
		copy.Control.Approval = &approval
	}
	if state.Route.Last != nil {
		last := *state.Route.Last
		copy.Route.Last = &last
	}
	return copy
}

func cloneNodeVisits(source map[core.NodeType]int) map[core.NodeType]int {
	if source == nil {
		return nil
	}
	result := make(map[core.NodeType]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneNodeDurations(source map[core.NodeType]int64) map[core.NodeType]int64 {
	if source == nil {
		return nil
	}
	result := make(map[core.NodeType]int64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneSet(source map[string]struct{}) map[string]struct{} {
	if source == nil {
		return nil
	}
	result := make(map[string]struct{}, len(source))
	for key := range source {
		result[key] = struct{}{}
	}
	return result
}

var _ core.CheckpointStore = (*MemoryCheckpointStore)(nil)
