package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// SaveCheckpoint 将一次节点完成后的完整运行快照写入 PostgreSQL。
// 快照只用于审计和恢复，不会被拼入模型上下文。state_json 这里只解析通用
// RunState 外壳；其中的 WorkflowPayload 已由 orchestrator 交给 Workflow Codec 编解码。
func (s *PostgresStore) SaveCheckpoint(ctx context.Context, checkpoint core.Checkpoint) error {
	if s == nil || s.db == nil {
		return errors.New("postgres checkpoint store is nil")
	}
	if checkpoint.RunID == "" || checkpoint.SessionID == "" {
		return errors.New("checkpoint run and session are required")
	}
	if err := validateOwner(checkpoint.Identity, checkpoint.SessionID); err != nil {
		return err
	}
	state, err := marshalCheckpointJSON(checkpoint.State)
	if err != nil {
		return fmt.Errorf("encode checkpoint state: %w", err)
	}
	route, err := marshalCheckpointJSON(checkpoint.Route)
	if err != nil {
		return fmt.Errorf("encode checkpoint route: %w", err)
	}
	events, err := marshalCheckpointJSON(checkpoint.TraceEvents)
	if err != nil {
		return fmt.Errorf("encode checkpoint trace events: %w", err)
	}
	if checkpoint.ID == "" {
		return errors.New("checkpoint id is required")
	}
	if checkpoint.TraceID == "" {
		checkpoint.TraceID = checkpoint.RunID
	}
	if checkpoint.CreatedAt.IsZero() {
		checkpoint.CreatedAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save checkpoint: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Trace 可能被关闭，先建立最小运行记录以满足外键并保留完整归属信息。
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_runs
		(run_id, tenant_id, user_id, session_id, status, started_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (run_id) DO NOTHING`, checkpoint.RunID, checkpoint.Identity.TenantID,
		checkpoint.Identity.UserID, checkpoint.SessionID, checkpoint.Status, checkpoint.CreatedAt); err != nil {
		return fmt.Errorf("ensure checkpoint run: %w", err)
	}
	var runTenant, runUser, runSession string
	if err := tx.QueryRowContext(ctx, `SELECT tenant_id, user_id, session_id FROM agent_runs WHERE run_id = $1`, checkpoint.RunID).Scan(&runTenant, &runUser, &runSession); err != nil {
		return fmt.Errorf("verify checkpoint run owner: %w", err)
	}
	if runTenant != checkpoint.Identity.TenantID || runUser != checkpoint.Identity.UserID || runSession != checkpoint.SessionID {
		return errors.New("checkpoint run owner mismatch")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_checkpoints
		(checkpoint_id, run_id, trace_id, session_id, tenant_id, user_id, credential, node, status, sequence, state_json, route_json, trace_events_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12::jsonb, $13::jsonb, $14)
		ON CONFLICT (checkpoint_id) DO UPDATE SET
		trace_id = EXCLUDED.trace_id, node = EXCLUDED.node, status = EXCLUDED.status,
		sequence = EXCLUDED.sequence, state_json = EXCLUDED.state_json, route_json = EXCLUDED.route_json,
		trace_events_json = EXCLUDED.trace_events_json, credential = EXCLUDED.credential`,
		checkpoint.ID, checkpoint.RunID, checkpoint.TraceID, checkpoint.SessionID,
		checkpoint.Identity.TenantID, checkpoint.Identity.UserID, checkpoint.Credential,
		checkpoint.Node, checkpoint.Status, checkpoint.Sequence, state, route, events, checkpoint.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit checkpoint: %w", err)
	}
	return nil
}

// marshalCheckpointJSON 为 PostgreSQL jsonb 生成可写入的 JSON。Go 编码器会把
// 内嵌 NUL 转义为 \\u0000；该形式虽然是合法 JSON，但 PostgreSQL jsonb 会拒绝。
// 因此在持久化边界替换成 Unicode 替代字符，避免单个异常上游字符串回滚整个事务。
func marshalCheckpointJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return replaceJSONNUL(data), nil
}

func replaceJSONNUL(data []byte) []byte {
	const nulEscape = `\u0000`
	const replacement = `\ufffd`
	result := make([]byte, 0, len(data))
	for index := 0; index < len(data); {
		if index+len(nulEscape) <= len(data) && string(data[index:index+len(nulEscape)]) == nulEscape {
			// 前面连续反斜杠为奇数时，当前位置属于 JSON 字符串中的转义反斜杠，
			// 并不是真正的 NUL 转义。
			backslashes := 0
			for cursor := index - 1; cursor >= 0 && data[cursor] == '\\'; cursor-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				result = append(result, replacement...)
				index += len(nulEscape)
				continue
			}
		}
		result = append(result, data[index])
		index++
	}
	return result
}

func (s *PostgresStore) ListCheckpoints(ctx context.Context, identity core.Identity, runID string) ([]core.Checkpoint, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres checkpoint store is nil")
	}
	if identity.TenantID == "" || identity.UserID == "" || runID == "" {
		return nil, errors.New("checkpoint owner and run are required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT checkpoint_id, run_id, trace_id, session_id, tenant_id, user_id, credential, node, status, sequence, state_json, route_json, trace_events_json, created_at
		FROM agent_checkpoints WHERE tenant_id = $1 AND user_id = $2 AND run_id = $3 ORDER BY sequence, created_at, checkpoint_id`, identity.TenantID, identity.UserID, runID)
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}
	defer rows.Close()
	result := make([]core.Checkpoint, 0)
	for rows.Next() {
		checkpoint, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, checkpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate checkpoints: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) LatestCheckpoint(ctx context.Context, identity core.Identity, runID string) (core.Checkpoint, error) {
	if s == nil || s.db == nil {
		return core.Checkpoint{}, errors.New("postgres checkpoint store is nil")
	}
	if identity.TenantID == "" || identity.UserID == "" || runID == "" {
		return core.Checkpoint{}, errors.New("checkpoint owner and run are required")
	}
	row := s.db.QueryRowContext(ctx, `SELECT checkpoint_id, run_id, trace_id, session_id, tenant_id, user_id, credential, node, status, sequence, state_json, route_json, trace_events_json, created_at
		FROM agent_checkpoints WHERE tenant_id = $1 AND user_id = $2 AND run_id = $3 ORDER BY sequence DESC, created_at DESC, checkpoint_id DESC LIMIT 1`, identity.TenantID, identity.UserID, runID)
	return scanCheckpoint(row)
}

func (s *PostgresStore) LatestSuspendedCheckpoint(ctx context.Context, identity core.Identity, sessionID string) (core.Checkpoint, error) {
	if s == nil || s.db == nil {
		return core.Checkpoint{}, errors.New("postgres checkpoint store is nil")
	}
	if err := validateOwner(identity, sessionID); err != nil {
		return core.Checkpoint{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT checkpoint_id, run_id, trace_id, session_id, tenant_id, user_id, credential, node, status, sequence, state_json, route_json, trace_events_json, created_at
		FROM agent_checkpoints WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3 AND status = 'suspended' ORDER BY created_at DESC, sequence DESC, checkpoint_id DESC LIMIT 1`, identity.TenantID, identity.UserID, sessionID)
	return scanCheckpoint(row)
}

func (s *PostgresStore) UpdateCheckpointStatus(ctx context.Context, identity core.Identity, checkpointID, status string) error {
	if s == nil || s.db == nil {
		return errors.New("postgres checkpoint store is nil")
	}
	if identity.TenantID == "" || identity.UserID == "" || checkpointID == "" || status == "" {
		return errors.New("checkpoint owner, id and status are required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_checkpoints SET status = $4
		WHERE checkpoint_id = $1 AND tenant_id = $2 AND user_id = $3`, checkpointID, identity.TenantID, identity.UserID, status)
	if err != nil {
		return fmt.Errorf("update checkpoint status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count checkpoint status update: %w", err)
	}
	if count != 1 {
		return errors.New("checkpoint not found")
	}
	return nil
}

type checkpointScanner interface{ Scan(...any) error }

func scanCheckpoint(scanner checkpointScanner) (core.Checkpoint, error) {
	var checkpoint core.Checkpoint
	var state, route, events []byte
	var tenantID, userID string
	if err := scanner.Scan(&checkpoint.ID, &checkpoint.RunID, &checkpoint.TraceID, &checkpoint.SessionID,
		&tenantID, &userID, &checkpoint.Credential, &checkpoint.Node, &checkpoint.Status,
		&checkpoint.Sequence, &state, &route, &events, &checkpoint.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.Checkpoint{}, errors.New("checkpoint not found")
		}
		return core.Checkpoint{}, fmt.Errorf("scan checkpoint: %w", err)
	}
	checkpoint.Identity = core.Identity{TenantID: tenantID, UserID: userID}
	if err := json.Unmarshal(state, &checkpoint.State); err != nil {
		return core.Checkpoint{}, fmt.Errorf("decode checkpoint state: %w", err)
	}
	if err := json.Unmarshal(route, &checkpoint.Route); err != nil {
		return core.Checkpoint{}, fmt.Errorf("decode checkpoint route: %w", err)
	}
	if err := json.Unmarshal(events, &checkpoint.TraceEvents); err != nil {
		return core.Checkpoint{}, fmt.Errorf("decode checkpoint trace events: %w", err)
	}
	return checkpoint, nil
}

var _ core.CheckpointStore = (*PostgresStore)(nil)
