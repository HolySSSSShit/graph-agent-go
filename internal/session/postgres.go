package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/HolySSSSShit/go-agent/internal/core"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore 是会话和用户消息的唯一持久化所有者。
// MCP 工具目录仍只在进程内按 session 缓存，重启后由工具注册表重新加载。
type PostgresStore struct {
	db *sql.DB
}

func NewPostgres(ctx context.Context, dsn string, timezone ...string) (*PostgresStore, error) {
	if dsn == "" {
		return nil, errors.New("postgres session store requires database url")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	store := &PostgresStore{db: db}
	if err := store.db.PingContext(ctx); err != nil {
		pingErr := err
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("ping postgres: %v; close postgres: %w", pingErr, closeErr)
		}
		return nil, fmt.Errorf("ping postgres: %w", pingErr)
	}
	location := core.DefaultTimezone
	if len(timezone) > 0 && timezone[0] != "" {
		location = timezone[0]
	}
	if _, err := store.db.ExecContext(ctx, `SELECT set_config('TimeZone', $1, false)`, location); err != nil {
		err := db.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("set postgres timezone: %w", err)
	}
	if err := store.migrate(ctx); err != nil {
		err := db.Close()
		if err != nil {
			return nil, err
		}
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

func (s *PostgresStore) Get(ctx context.Context, identity core.Identity, id string) (core.Session, error) {
	if err := validateOwner(identity, id); err != nil {
		return core.Session{}, err
	}
	const insert = `INSERT INTO agent_sessions (tenant_id, user_id, session_id, state)
		VALUES ($1, $2, $3, 'new') ON CONFLICT (tenant_id, user_id, session_id) DO NOTHING`
	if _, err := s.db.ExecContext(ctx, insert, identity.TenantID, identity.UserID, id); err != nil {
		return core.Session{}, fmt.Errorf("create session: %w", err)
	}
	var state string
	const selectState = `SELECT state FROM agent_sessions WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3`
	if err := s.db.QueryRowContext(ctx, selectState, identity.TenantID, identity.UserID, id).Scan(&state); err != nil {
		return core.Session{}, fmt.Errorf("load session: %w", err)
	}
	messages, err := s.messages(ctx, identity, id)
	if err != nil {
		return core.Session{}, err
	}
	return core.Session{ID: id, Identity: identity, State: state, Messages: messages}, nil
}

func (s *PostgresStore) List(ctx context.Context, identity core.Identity) ([]core.Session, error) {
	if identity.TenantID == "" || identity.UserID == "" {
		return nil, errors.New("session owner is required")
	}
	const query = `SELECT session_id, state FROM agent_sessions WHERE tenant_id = $1 AND user_id = $2 ORDER BY updated_at DESC, session_id`
	rows, err := s.db.QueryContext(ctx, query, identity.TenantID, identity.UserID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {

		}
	}(rows)
	result := make([]core.Session, 0)
	for rows.Next() {
		var session core.Session
		session.Identity = identity
		if err := rows.Scan(&session.ID, &session.State); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) Delete(ctx context.Context, identity core.Identity, id string) error {
	if err := validateOwner(identity, id); err != nil {
		return err
	}
	const query = `DELETE FROM agent_sessions WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3`
	result, err := s.db.ExecContext(ctx, query, identity.TenantID, identity.UserID, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted sessions: %w", err)
	}
	if count != 1 {
		return errors.New("session not found")
	}
	return nil
}

func (s *PostgresStore) AppendMessage(ctx context.Context, identity core.Identity, id string, message core.Message) error {
	if err := validateOwner(identity, id); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin append message: %w", err)
	}
	defer func(tx *sql.Tx) {
		err := tx.Rollback()
		if err != nil {

		}
	}(tx)
	const update = `UPDATE agent_sessions SET updated_at = NOW() WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3`
	result, err := tx.ExecContext(ctx, update, identity.TenantID, identity.UserID, id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count session update: %w", err)
	}
	if count != 1 {
		return errors.New("session not found")
	}
	const insert = `INSERT INTO agent_messages (tenant_id, user_id, session_id, role, content, visualizations_json)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)`
	if _, err := tx.ExecContext(ctx, insert, identity.TenantID, identity.UserID, id, message.Role, message.Content, nullableJSON(visualizationsJSON(message.Visualizations))); err != nil {
		return fmt.Errorf("insert session message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session message: %w", err)
	}
	return nil
}

func (s *PostgresStore) UpdateState(ctx context.Context, identity core.Identity, id, state string) error {
	if err := validateOwner(identity, id); err != nil {
		return err
	}
	const query = `UPDATE agent_sessions SET state = $4, updated_at = NOW() WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3`
	result, err := s.db.ExecContext(ctx, query, identity.TenantID, identity.UserID, id, state)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count session state update: %w", err)
	}
	if count != 1 {
		return errors.New("session not found")
	}
	return nil
}

func (s *PostgresStore) GetCompact(ctx context.Context, identity core.Identity, id string) (core.SessionCompact, error) {
	if err := validateOwner(identity, id); err != nil {
		return core.SessionCompact{}, err
	}
	var compact core.SessionCompact
	const query = `SELECT version, covered_messages, content FROM session_compacts WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3`
	err := s.db.QueryRowContext(ctx, query, identity.TenantID, identity.UserID, id).Scan(&compact.Version, &compact.CoveredMessages, &compact.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return core.SessionCompact{}, nil
	}
	if err != nil {
		return core.SessionCompact{}, fmt.Errorf("load session compact: %w", err)
	}
	return compact, nil
}

func (s *PostgresStore) CompareAndSwapCompact(ctx context.Context, identity core.Identity, id string, version int64, compact core.SessionCompact) (bool, error) {
	if err := validateOwner(identity, id); err != nil {
		return false, err
	}
	const query = `INSERT INTO session_compacts (tenant_id, user_id, session_id, version, covered_messages, content)
		VALUES ($1, $2, $3, 1, $4, $5)
		ON CONFLICT (tenant_id, user_id, session_id) DO UPDATE SET version = session_compacts.version + 1, covered_messages = EXCLUDED.covered_messages, content = EXCLUDED.content, updated_at = NOW()
		WHERE session_compacts.version = $6
		RETURNING version`
	var newVersion int64
	err := s.db.QueryRowContext(ctx, query, identity.TenantID, identity.UserID, id, compact.CoveredMessages, compact.Content, version).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("save session compact: %w", err)
	}
	return true, nil
}

func (s *PostgresStore) SaveEvidenceFacts(ctx context.Context, identity core.Identity, sessionID, runID string, facts []core.EvidenceFact) error {
	if err := validateOwner(identity, sessionID); err != nil {
		return err
	}
	if runID == "" {
		return errors.New("run id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save evidence facts: %w", err)
	}
	defer func(tx *sql.Tx) {
		err := tx.Rollback()
		if err != nil {

		}
	}(tx)
	// 数据库 trace 可选。即使关闭，事实仍需要运行归属，因此仅在不存在时创建最小的已完成运行记录。
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_runs
		(run_id, tenant_id, user_id, session_id, status, started_at, completed_at, duration_ms)
		VALUES ($1, $2, $3, $4, 'completed', NOW(), NOW(), 0)
		ON CONFLICT (run_id) DO NOTHING`, runID, identity.TenantID, identity.UserID, sessionID); err != nil {
		return fmt.Errorf("ensure evidence run: %w", err)
	}
	const insert = `INSERT INTO agent_evidence_facts
		(evidence_id, tenant_id, user_id, session_id, run_id, metric_id, label, value_json, unit, dimensions, step_id, source_tool, source_ref, source_path, chartable, kind)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10::jsonb, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (run_id, evidence_id) DO UPDATE SET
		metric_id = EXCLUDED.metric_id,
		label = EXCLUDED.label,
		value_json = EXCLUDED.value_json,
		unit = EXCLUDED.unit,
		dimensions = EXCLUDED.dimensions,
		step_id = EXCLUDED.step_id,
		source_tool = EXCLUDED.source_tool,
		source_ref = EXCLUDED.source_ref,
		source_path = EXCLUDED.source_path,
		chartable = EXCLUDED.chartable,
		kind = EXCLUDED.kind`
	for _, fact := range facts {
		if fact.ID == "" || fact.SourceRef == "" || fact.SourceTool == "" || fact.SourcePath == "" {
			return errors.New("evidence fact requires id and complete source provenance")
		}
		value, err := json.Marshal(fact.Value)
		if err != nil {
			return fmt.Errorf("encode evidence value: %w", err)
		}
		dimensions, err := json.Marshal(fact.Dimensions)
		if err != nil {
			return fmt.Errorf("encode evidence dimensions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, insert, fact.ID, identity.TenantID, identity.UserID, sessionID, runID, fact.MetricID, fact.Label, value, fact.Unit, dimensions, fact.StepID, fact.SourceTool, fact.SourceRef, fact.SourcePath, fact.Chartable, fact.Kind); err != nil {
			return fmt.Errorf("insert evidence fact: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit evidence facts: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListEvidenceFacts(ctx context.Context, identity core.Identity, sessionID, runID string) ([]core.EvidenceFact, error) {
	if err := validateOwner(identity, sessionID); err != nil {
		return nil, err
	}
	const query = `SELECT evidence_id, metric_id, label, value_json, unit, dimensions, step_id, source_tool, source_ref, source_path, chartable, kind
		FROM agent_evidence_facts
		WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3 AND run_id = $4
		ORDER BY evidence_id`
	rows, err := s.db.QueryContext(ctx, query, identity.TenantID, identity.UserID, sessionID, runID)
	if err != nil {
		return nil, fmt.Errorf("list evidence facts: %w", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {

		}
	}(rows)
	result := make([]core.EvidenceFact, 0)
	for rows.Next() {
		var fact core.EvidenceFact
		var value, dimensions []byte
		var chartable sql.NullBool
		if err := rows.Scan(&fact.ID, &fact.MetricID, &fact.Label, &value, &fact.Unit, &dimensions, &fact.StepID, &fact.SourceTool, &fact.SourceRef, &fact.SourcePath, &chartable, &fact.Kind); err != nil {
			return nil, fmt.Errorf("scan evidence fact: %w", err)
		}
		if chartable.Valid {
			fact.Chartable = &chartable.Bool
		}
		if err := json.Unmarshal(value, &fact.Value); err != nil {
			return nil, fmt.Errorf("decode evidence value: %w", err)
		}
		if len(dimensions) > 0 && string(dimensions) != "null" {
			if err := json.Unmarshal(dimensions, &fact.Dimensions); err != nil {
				return nil, fmt.Errorf("decode evidence dimensions: %w", err)
			}
		}
		result = append(result, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence facts: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListLatestEvidenceFacts(ctx context.Context, identity core.Identity, sessionID string) ([]core.EvidenceFact, error) {
	if err := validateOwner(identity, sessionID); err != nil {
		return nil, err
	}
	const query = `SELECT evidence_id, metric_id, label, value_json, unit, dimensions, step_id, source_tool, source_ref, source_path, chartable, kind
		FROM agent_evidence_facts
		WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3
		AND run_id = (
			SELECT run_id FROM agent_evidence_facts
			WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3
			ORDER BY created_at DESC, run_id DESC LIMIT 1
		)
		ORDER BY evidence_id`
	rows, err := s.db.QueryContext(ctx, query, identity.TenantID, identity.UserID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list latest evidence facts: %w", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {

		}
	}(rows)
	result := make([]core.EvidenceFact, 0)
	for rows.Next() {
		var fact core.EvidenceFact
		var value, dimensions []byte
		var chartable sql.NullBool
		if err := rows.Scan(&fact.ID, &fact.MetricID, &fact.Label, &value, &fact.Unit, &dimensions, &fact.StepID, &fact.SourceTool, &fact.SourceRef, &fact.SourcePath, &chartable, &fact.Kind); err != nil {
			return nil, fmt.Errorf("scan latest evidence fact: %w", err)
		}
		if chartable.Valid {
			fact.Chartable = &chartable.Bool
		}
		if err := json.Unmarshal(value, &fact.Value); err != nil {
			return nil, fmt.Errorf("decode latest evidence value: %w", err)
		}
		if len(dimensions) > 0 && string(dimensions) != "null" {
			if err := json.Unmarshal(dimensions, &fact.Dimensions); err != nil {
				return nil, fmt.Errorf("decode latest evidence dimensions: %w", err)
			}
		}
		result = append(result, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest evidence facts: %w", err)
	}
	return result, nil
}

// PruneEvidenceFacts 为一个会话保留最近 keepRuns 次运行中的事实。
// 归属条件保证在共享数据库中清理也是安全的。
func (s *PostgresStore) PruneEvidenceFacts(ctx context.Context, identity core.Identity, sessionID string, keepRuns int) (int, error) {
	if err := validateOwner(identity, sessionID); err != nil {
		return 0, err
	}
	if keepRuns < 1 {
		keepRuns = 1
	}
	const query = `DELETE FROM agent_evidence_facts
		WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3
		AND run_id NOT IN (
			SELECT run_id FROM agent_evidence_facts
			WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3
			GROUP BY run_id ORDER BY MAX(created_at) DESC, run_id DESC LIMIT $4
		)`
	result, err := s.db.ExecContext(ctx, query, identity.TenantID, identity.UserID, sessionID, keepRuns)
	if err != nil {
		return 0, fmt.Errorf("prune evidence facts: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned evidence facts: %w", err)
	}
	return int(count), nil
}

func (s *PostgresStore) messages(ctx context.Context, identity core.Identity, id string) ([]core.Message, error) {
	const query = `SELECT role, content, visualizations_json FROM agent_messages
		WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3 ORDER BY message_id`
	rows, err := s.db.QueryContext(ctx, query, identity.TenantID, identity.UserID, id)
	if err != nil {
		return nil, fmt.Errorf("list session messages: %w", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {

		}
	}(rows)
	result := make([]core.Message, 0)
	for rows.Next() {
		var message core.Message
		var visualizations []byte
		if err := rows.Scan(&message.Role, &message.Content, &visualizations); err != nil {
			return nil, fmt.Errorf("scan session message: %w", err)
		}
		if len(visualizations) > 0 {
			if err := json.Unmarshal(visualizations, &message.Visualizations); err != nil {
				return nil, fmt.Errorf("decode message visualizations: %w", err)
			}
		}
		result = append(result, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session messages: %w", err)
	}
	return result, nil
}

func visualizationsJSON(values []core.Visualization) string {
	if len(values) == 0 {
		return ""
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func nullableJSON(value string) any {
	if value == "" || value == "null" {
		return nil
	}
	return value
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	return applyMigrations(ctx, s)
}

func validateOwner(identity core.Identity, sessionID string) error {
	if identity.TenantID == "" || identity.UserID == "" || sessionID == "" {
		return errors.New("tenant, user and session id are required")
	}
	return nil
}

var _ core.SessionStore = (*PostgresStore)(nil)
var _ core.EvidenceFactStore = (*PostgresStore)(nil)
var _ core.EvidenceFactPruner = (*PostgresStore)(nil)
