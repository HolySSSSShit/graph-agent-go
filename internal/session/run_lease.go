package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

const defaultRunLeaseDuration = 30 * time.Second

// AcquireRunLease 为一个全新的 run 原子申请租户并发额度并创建执行租约。
func (s *PostgresStore) AcquireRunLease(ctx context.Context, request core.RunLeaseRequest) (core.RunLease, error) {
	if s == nil || s.db == nil {
		return core.RunLease{}, errors.New("postgres run lease store is nil")
	}
	if request.RunID == "" || request.TenantID == "" || request.UserID == "" || request.SessionID == "" || request.WorkerID == "" {
		return core.RunLease{}, errors.New("run lease owner and ids are required")
	}
	duration := request.LeaseDuration
	if duration <= 0 {
		duration = defaultRunLeaseDuration
	}
	maxRuns := request.MaxTenantRuns
	if maxRuns <= 0 {
		maxRuns = 5
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.RunLease{}, fmt.Errorf("begin run lease: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_tenant_runtime_limits (tenant_id, max_concurrent_runs)
		VALUES ($1, $2) ON CONFLICT (tenant_id) DO NOTHING`, request.TenantID, maxRuns); err != nil {
		return core.RunLease{}, fmt.Errorf("ensure tenant run limit: %w", err)
	}
	reclaimed, err := tx.ExecContext(ctx, `UPDATE agent_runs
		SET status = 'interrupted', worker_id = NULL, lease_until = NULL, active_slot = FALSE,
		    version = version + 1, updated_at = NOW()
		WHERE tenant_id = $1 AND active_slot = TRUE AND lease_until <= NOW()`, request.TenantID)
	if err != nil {
		return core.RunLease{}, fmt.Errorf("reclaim expired run leases: %w", err)
	}
	reclaimedCount, err := reclaimed.RowsAffected()
	if err != nil {
		return core.RunLease{}, fmt.Errorf("count reclaimed run leases: %w", err)
	}
	if reclaimedCount > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_tenant_runtime_limits
			SET active_runs = GREATEST(active_runs - $2, 0), updated_at = NOW()
			WHERE tenant_id = $1`, request.TenantID, reclaimedCount); err != nil {
			return core.RunLease{}, fmt.Errorf("release reclaimed tenant slots: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE agent_tenant_runtime_limits
		SET active_runs = active_runs + 1, updated_at = NOW()
		WHERE tenant_id = $1 AND active_runs < max_concurrent_runs`, request.TenantID)
	if err != nil {
		return core.RunLease{}, fmt.Errorf("reserve tenant run slot: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return core.RunLease{}, fmt.Errorf("count tenant run slot: %w", err)
	}
	if rows != 1 {
		return core.RunLease{}, errors.New("tenant concurrent run limit reached")
	}
	leaseUntil := time.Now().Add(duration)
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_runs
		(run_id, tenant_id, user_id, session_id, status, started_at, version, worker_id, lease_until, active_slot)
		VALUES ($1, $2, $3, $4, 'running', NOW(), 1, $5, $6, TRUE)`,
		request.RunID, request.TenantID, request.UserID, request.SessionID, request.WorkerID, leaseUntil); err != nil {
		return core.RunLease{}, fmt.Errorf("create run lease: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return core.RunLease{}, fmt.Errorf("commit run lease: %w", err)
	}
	return core.RunLease{RunID: request.RunID, WorkerID: request.WorkerID, Version: 1, LeaseUntil: leaseUntil}, nil
}

func (s *PostgresStore) RenewRunLease(ctx context.Context, lease core.RunLease, duration time.Duration) (core.RunLease, error) {
	if s == nil || s.db == nil {
		return core.RunLease{}, errors.New("postgres run lease store is nil")
	}
	if lease.RunID == "" || lease.WorkerID == "" {
		return core.RunLease{}, errors.New("run lease id and worker are required")
	}
	if duration <= 0 {
		duration = defaultRunLeaseDuration
	}
	var next core.RunLease
	err := s.db.QueryRowContext(ctx, `UPDATE agent_runs
		SET lease_until = NOW() + ($4 * INTERVAL '1 second'), version = version + 1, updated_at = NOW()
		WHERE run_id = $1 AND worker_id = $2 AND version = $3
		  AND status = 'running' AND lease_until > NOW()
		RETURNING version, lease_until`, lease.RunID, lease.WorkerID, lease.Version, duration.Seconds()).Scan(&next.Version, &next.LeaseUntil)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.RunLease{}, core.ErrRunLeaseLost
		}
		return core.RunLease{}, fmt.Errorf("renew run lease: %w", err)
	}
	next.RunID = lease.RunID
	next.WorkerID = lease.WorkerID
	return next, nil
}

func (s *PostgresStore) ReleaseRunLease(ctx context.Context, lease core.RunLease, status string) error {
	if s == nil || s.db == nil {
		return errors.New("postgres run lease store is nil")
	}
	if lease.RunID == "" || lease.WorkerID == "" {
		return errors.New("run lease id and worker are required")
	}
	if status == "" {
		status = "completed"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin release run lease: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE agent_runs
		SET status = $3, worker_id = NULL, lease_until = NULL, active_slot = FALSE,
		    completed_at = CASE WHEN $3 IN ('completed', 'failed', 'cancelled') THEN NOW() ELSE completed_at END,
		    version = version + 1, updated_at = NOW()
		WHERE run_id = $1 AND worker_id = $2 AND active_slot = TRUE`, lease.RunID, lease.WorkerID, status)
	if err != nil {
		return fmt.Errorf("release run lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count released run lease: %w", err)
	}
	if rows == 1 {
		var tenantID string
		if err := tx.QueryRowContext(ctx, `SELECT tenant_id FROM agent_runs WHERE run_id = $1`, lease.RunID).Scan(&tenantID); err != nil {
			return fmt.Errorf("load released run tenant: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_tenant_runtime_limits
			SET active_runs = GREATEST(active_runs - 1, 0), updated_at = NOW()
			WHERE tenant_id = $1`, tenantID); err != nil {
			return fmt.Errorf("release tenant run slot: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit released run lease: %w", err)
	}
	return nil
}

var _ core.RunLeaseStore = (*PostgresStore)(nil)
