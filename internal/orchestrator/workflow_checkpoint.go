package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// WorkflowCheckpointStore 在通用 CheckpointStore 外包一层 Workflow Codec。
// 存储实现只负责 RunState 外壳；所有业务 payload 的打包和解包都在这里完成。
type WorkflowCheckpointStore struct {
	Store    core.CheckpointStore
	Workflow WorkflowDefinition
}

// CheckpointingEnabled 只暴露控制面能力，不向 API 泄露底层存储实现。
func (r *Runner) CheckpointingEnabled() bool {
	return r != nil && r.Checkpoints != nil
}

// LatestCheckpoint 通过 checkpoint 声明的 Workflow 选择 StateCodec，供编排器外层控制面安全读取。
func (r *Runner) LatestCheckpoint(ctx context.Context, identity core.Identity, runID string) (core.Checkpoint, error) {
	if r == nil || r.Checkpoints == nil {
		return core.Checkpoint{}, fmt.Errorf("checkpoint store is not configured")
	}
	checkpoint, err := r.Checkpoints.LatestCheckpoint(ctx, identity, runID)
	if err != nil {
		return core.Checkpoint{}, err
	}
	return r.normalizeCheckpoint(checkpoint)
}

// LatestSuspendedCheckpoint 与 LatestCheckpoint 使用同一 codec 边界。
func (r *Runner) LatestSuspendedCheckpoint(ctx context.Context, identity core.Identity, sessionID string) (core.Checkpoint, error) {
	if r == nil || r.Checkpoints == nil {
		return core.Checkpoint{}, fmt.Errorf("checkpoint store is not configured")
	}
	checkpoint, err := r.Checkpoints.LatestSuspendedCheckpoint(ctx, identity, sessionID)
	if err != nil {
		return core.Checkpoint{}, err
	}
	return r.normalizeCheckpoint(checkpoint)
}

// SaveWorkflowCheckpoint 保证控制面更新 checkpoint 时同样经过当前 Workflow codec。
func (r *Runner) SaveWorkflowCheckpoint(ctx context.Context, checkpoint core.Checkpoint) error {
	if r == nil || r.Checkpoints == nil {
		return fmt.Errorf("checkpoint store is not configured")
	}
	workflow, err := r.workflowForCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	return (WorkflowCheckpointStore{Store: r.Checkpoints, Workflow: workflow}).SaveCheckpoint(ctx, checkpoint)
}

func (r *Runner) normalizeCheckpoint(checkpoint core.Checkpoint) (core.Checkpoint, error) {
	workflow, err := r.workflowForCheckpoint(checkpoint)
	if err != nil {
		return core.Checkpoint{}, err
	}
	if err := normalizeWorkflowCheckpoint(workflow, &checkpoint.State); err != nil {
		return core.Checkpoint{}, err
	}
	return checkpoint, nil
}

func (r *Runner) workflowForCheckpoint(checkpoint core.Checkpoint) (WorkflowDefinition, error) {
	if r == nil || r.Workflows == nil {
		return nil, fmt.Errorf("workflow registry is not configured")
	}
	if checkpoint.State.WorkflowKind == "" {
		return nil, fmt.Errorf("checkpoint workflow kind is required")
	}
	return r.Workflows.Resolve(checkpoint.State.WorkflowKind)
}

func (s WorkflowCheckpointStore) SaveCheckpoint(ctx context.Context, checkpoint core.Checkpoint) error {
	if s.Store == nil {
		return fmt.Errorf("workflow checkpoint store is not configured")
	}
	if err := normalizeWorkflowCheckpoint(s.Workflow, &checkpoint.State); err != nil {
		return fmt.Errorf("encode workflow checkpoint: %w", err)
	}
	checkpoint.State.WorkflowState.Data = nil
	return s.Store.SaveCheckpoint(ctx, checkpoint)
}

func (s WorkflowCheckpointStore) ListCheckpoints(ctx context.Context, identity core.Identity, runID string) ([]core.Checkpoint, error) {
	checkpoints, err := s.Store.ListCheckpoints(ctx, identity, runID)
	if err != nil {
		return nil, err
	}
	for index := range checkpoints {
		if err := normalizeWorkflowCheckpoint(s.Workflow, &checkpoints[index].State); err != nil {
			return nil, fmt.Errorf("decode workflow checkpoint %s: %w", checkpoints[index].ID, err)
		}
	}
	return checkpoints, nil
}

func (s WorkflowCheckpointStore) LatestCheckpoint(ctx context.Context, identity core.Identity, runID string) (core.Checkpoint, error) {
	checkpoint, err := s.Store.LatestCheckpoint(ctx, identity, runID)
	if err != nil {
		return core.Checkpoint{}, err
	}
	if err := normalizeWorkflowCheckpoint(s.Workflow, &checkpoint.State); err != nil {
		return core.Checkpoint{}, fmt.Errorf("decode workflow checkpoint %s: %w", checkpoint.ID, err)
	}
	return checkpoint, nil
}

func (s WorkflowCheckpointStore) LatestSuspendedCheckpoint(ctx context.Context, identity core.Identity, sessionID string) (core.Checkpoint, error) {
	checkpoint, err := s.Store.LatestSuspendedCheckpoint(ctx, identity, sessionID)
	if err != nil {
		return core.Checkpoint{}, err
	}
	if err := normalizeWorkflowCheckpoint(s.Workflow, &checkpoint.State); err != nil {
		return core.Checkpoint{}, fmt.Errorf("decode workflow checkpoint %s: %w", checkpoint.ID, err)
	}
	return checkpoint, nil
}

func (s WorkflowCheckpointStore) UpdateCheckpointStatus(ctx context.Context, identity core.Identity, checkpointID, status string) error {
	return s.Store.UpdateCheckpointStatus(ctx, identity, checkpointID, status)
}

// normalizeWorkflowCheckpoint 是 checkpoint 与业务 StateCodec 的唯一连接点。
// 外部存储只解析通用 RunState；业务 payload 在保存和恢复时都必须经过 Workflow Codec。
func normalizeWorkflowCheckpoint(workflow WorkflowDefinition, state *core.RunState) error {
	if workflow == nil || state == nil {
		return nil
	}
	if state.WorkflowKind != workflow.Kind() {
		return fmt.Errorf("checkpoint workflow mismatch: expected %s, got %s", workflow.Kind(), state.WorkflowKind)
	}
	var payload json.RawMessage
	var err error
	if state.WorkflowState.Data != nil {
		payload, err = workflow.EncodeStateValue(state.WorkflowState.Data)
	} else {
		decoded, decodeErr := workflow.DecodeStatePayload(state.WorkflowPayload)
		if decodeErr != nil {
			err = decodeErr
		} else {
			state.WorkflowState.Data = decoded
			payload, err = workflow.EncodeStateValue(decoded)
		}
	}
	if err != nil {
		return fmt.Errorf("workflow payload codec failed: %w", err)
	}
	state.WorkflowState.WorkflowPayload = payload
	return nil
}

var _ core.CheckpointStore = WorkflowCheckpointStore{}
