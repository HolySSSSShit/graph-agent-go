package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// Request 将待确认动作写入数据库。相同 approval_id 的重复请求只返回仍处于 pending 的记录。
func (s *PostgresStore) Request(ctx context.Context, request core.ApprovalRequest) (core.Approval, error) {
	if s == nil || s.db == nil {
		return core.Approval{}, errors.New("postgres approval service is nil")
	}
	if request.ID == "" || request.RunID == "" || request.SessionID == "" {
		return core.Approval{}, errors.New("approval id, run and session are required")
	}
	if err := validateOwner(request.Identity, request.SessionID); err != nil {
		return core.Approval{}, err
	}
	action, err := json.Marshal(request.Action)
	if err != nil {
		return core.Approval{}, fmt.Errorf("encode approval action: %w", err)
	}
	argsHash := request.ArgsHash
	if argsHash == "" {
		argsHash = hashApprovalArgs(request.Action.Payload)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Approval{}, fmt.Errorf("begin approval request: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_runs
		(run_id, tenant_id, user_id, session_id, status, started_at)
		VALUES ($1, $2, $3, $4, 'running', NOW()) ON CONFLICT (run_id) DO NOTHING`,
		request.RunID, request.Identity.TenantID, request.Identity.UserID, request.SessionID); err != nil {
		return core.Approval{}, fmt.Errorf("ensure approval run: %w", err)
	}
	var existing core.Approval
	row := tx.QueryRowContext(ctx, `SELECT approval_id, run_id, session_id, tenant_id, user_id,
		action_json, args_hash, reason, summary, status, created_at, updated_at, resolved_at
		FROM agent_approval_requests WHERE approval_id = $1 FOR UPDATE`, request.ID)
	if err := scanApproval(row, &existing); err == nil {
		if existing.Identity.TenantID != request.Identity.TenantID || existing.Identity.UserID != request.Identity.UserID || existing.SessionID != request.SessionID {
			return core.Approval{}, core.ErrApprovalNotFound
		}
		if existing.Status != "pending" {
			return core.Approval{}, core.ErrApprovalAlreadyResolved
		}
		if err := tx.Commit(); err != nil {
			return core.Approval{}, fmt.Errorf("commit existing approval: %w", err)
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, core.ErrApprovalNotFound) {
		return core.Approval{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_approval_requests
		(approval_id, run_id, session_id, tenant_id, user_id, action_json, args_hash, reason, summary, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, 'pending', $10, $10)`, request.ID,
		request.RunID, request.SessionID, request.Identity.TenantID, request.Identity.UserID, action, argsHash, request.Action.Reason, request.Summary, now); err != nil {
		return core.Approval{}, fmt.Errorf("insert approval request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return core.Approval{}, fmt.Errorf("commit approval request: %w", err)
	}
	return core.Approval{ID: request.ID, RunID: request.RunID, SessionID: request.SessionID, Identity: request.Identity, Action: request.Action, ArgsHash: argsHash, Reason: request.Action.Reason, Summary: request.Summary, Status: "pending", CreatedAt: now, UpdatedAt: now}, nil
}

func (s *PostgresStore) Resolve(ctx context.Context, identity core.Identity, id string, decision core.ApprovalDecision) (core.Approval, error) {
	if s == nil || s.db == nil {
		return core.Approval{}, errors.New("postgres approval service is nil")
	}
	if identity.TenantID == "" || identity.UserID == "" || id == "" {
		return core.Approval{}, errors.New("tenant, user and approval id are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Approval{}, fmt.Errorf("begin approval resolution: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var approval core.Approval
	if err := scanApproval(tx.QueryRowContext(ctx, `SELECT approval_id, run_id, session_id, tenant_id, user_id,
		action_json, args_hash, reason, summary, status, created_at, updated_at, resolved_at
		FROM agent_approval_requests WHERE approval_id = $1 FOR UPDATE`, id), &approval); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, core.ErrApprovalNotFound) {
			return core.Approval{}, core.ErrApprovalNotFound
		}
		return core.Approval{}, err
	}
	if approval.Identity.TenantID != identity.TenantID || approval.Identity.UserID != identity.UserID {
		return core.Approval{}, core.ErrApprovalNotFound
	}
	if approval.Status != "pending" {
		return core.Approval{}, core.ErrApprovalAlreadyResolved
	}
	status := "rejected"
	if decision.Approved {
		status = "approved"
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE agent_approval_requests SET status = $2, updated_at = $3, resolved_at = $3 WHERE approval_id = $1`, id, status, now); err != nil {
		return core.Approval{}, fmt.Errorf("update approval status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return core.Approval{}, fmt.Errorf("commit approval resolution: %w", err)
	}
	approval.Status = status
	approval.UpdatedAt = now
	approval.ResolvedAt = &now
	return approval, nil
}

func (s *PostgresStore) RestorePending(ctx context.Context, identity core.Identity, id string) error {
	if s == nil || s.db == nil {
		return errors.New("postgres approval service is nil")
	}
	if identity.TenantID == "" || identity.UserID == "" || id == "" {
		return errors.New("tenant, user and approval id are required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_approval_requests
		SET status = 'pending', updated_at = NOW(), resolved_at = NULL
		WHERE approval_id = $1 AND tenant_id = $2 AND user_id = $3`, id, identity.TenantID, identity.UserID)
	if err != nil {
		return fmt.Errorf("restore approval status: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("restore approval status rows: %w", err)
	} else if count != 1 {
		return core.ErrApprovalNotFound
	}
	return nil
}

func scanApproval(scanner interface{ Scan(...any) error }, approval *core.Approval) error {
	var tenantID, userID string
	var action []byte
	var resolvedAt sql.NullTime
	if err := scanner.Scan(&approval.ID, &approval.RunID, &approval.SessionID, &tenantID, &userID,
		&action, &approval.ArgsHash, &approval.Reason, &approval.Summary, &approval.Status, &approval.CreatedAt, &approval.UpdatedAt, &resolvedAt); err != nil {
		return err
	}
	approval.Identity = core.Identity{TenantID: tenantID, UserID: userID}
	if err := json.Unmarshal(action, &approval.Action); err != nil {
		return fmt.Errorf("decode approval action: %w", err)
	}
	if resolvedAt.Valid {
		value := resolvedAt.Time
		approval.ResolvedAt = &value
	}
	return nil
}

func hashApprovalArgs(args map[string]any) string {
	data, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

var _ core.ApprovalService = (*PostgresStore)(nil)
