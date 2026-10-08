package trace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresSink 保存可查询的执行投影。
// 工具参数、模型提示词和输出以结构化字段保存，便于排查失败调用。
type PostgresSink struct {
	db *sql.DB
}

func NewPostgresSink(ctx context.Context, dsn, timezone string) (*PostgresSink, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres trace sink requires database url")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres trace sink: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		err := db.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("ping postgres trace sink: %w", err)
	}
	if timezone != "" {
		if _, err := db.ExecContext(ctx, `SELECT set_config('TimeZone', $1, false)`, timezone); err != nil {
			err := db.Close()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("set postgres trace timezone: %w", err)
		}
	}
	return &PostgresSink{db: db}, nil
}

func (s *PostgresSink) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *PostgresSink) Record(ctx context.Context, event core.TraceEvent) error {
	if s == nil || s.db == nil {
		return errors.New("postgres trace sink is nil")
	}
	projected, ok := project(event)
	if !ok {
		return nil
	}
	if projected.at.IsZero() {
		projected.at = time.Now().UTC()
	}
	if projected.runID == "" || projected.tenantID == "" || projected.userID == "" || projected.sessionID == "" {
		return errors.New("projected trace event requires run and owner identity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin trace projection: %w", err)
	}
	defer func(tx *sql.Tx) {
		err := tx.Rollback()
		if err != nil {

		}
	}(tx)
	if projected.eventType == "run.started" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_runs (run_id, tenant_id, user_id, session_id, status, started_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (run_id) DO NOTHING`, projected.runID, projected.tenantID, projected.userID, projected.sessionID, projected.status, projected.at); err != nil {
			return fmt.Errorf("insert trace run: %w", err)
		}
	}
	if projected.eventType == "run.completed" || projected.eventType == "run.failed" {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_runs
			SET status = $2, error_code = $3, completed_at = $4, duration_ms = $5
			WHERE run_id = $1`, projected.runID, projected.status, nullableString(projected.errorCode), projected.at, projected.durationMS); err != nil {
			return fmt.Errorf("complete trace run: %w", err)
		}
	}
	// 保存紧凑的运行索引，使 runs 表无需连接全部事件即可回答运维查询。
	toolIncrement := 0
	if projected.toolTerminal {
		toolIncrement = 1
	}
	modelIncrement := 0
	if projected.modelTerminal {
		modelIncrement = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_runs
		SET event_count = COALESCE(event_count, 0) + 1,
			tool_call_count = COALESCE(tool_call_count, 0) + $2,
			model_call_count = COALESCE(model_call_count, 0) + $3,
			last_stage = COALESCE($4, last_stage),
			last_message = COALESCE(NULLIF($5, ''), last_message),
			last_event_type = $6,
			last_tool_name = COALESCE(NULLIF($7, ''), last_tool_name),
			last_model_name = COALESCE(NULLIF($8, ''), last_model_name)
		WHERE run_id = $1`, projected.runID, toolIncrement, modelIncrement, nullableString(projected.stage), projected.message, projected.eventType, projected.toolName, projected.modelName); err != nil {
		return fmt.Errorf("update trace run summary: %w", err)
	}
	if projected.isTool {
		completedAt := any(nil)
		if projected.toolComplete {
			completedAt = projected.at
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_tool_calls
			(run_id, step_id, attempt, tool_name, status, error_code, started_at, completed_at, duration_ms, result_bytes, result_handle_id, arguments_json)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb)
			ON CONFLICT (run_id, step_id, attempt) DO UPDATE SET
			status = EXCLUDED.status,
			error_code = EXCLUDED.error_code,
			completed_at = COALESCE(EXCLUDED.completed_at, agent_tool_calls.completed_at),
			duration_ms = COALESCE(EXCLUDED.duration_ms, agent_tool_calls.duration_ms),
			result_bytes = COALESCE(EXCLUDED.result_bytes, agent_tool_calls.result_bytes),
			result_handle_id = COALESCE(EXCLUDED.result_handle_id, agent_tool_calls.result_handle_id),
			arguments_json = COALESCE(EXCLUDED.arguments_json, agent_tool_calls.arguments_json)`,
			projected.runID, projected.stepID, projected.attempt, projected.toolName, projected.status,
			nullableString(projected.errorCode), projected.at, completedAt, nullableInt64(projected.durationMS),
			nullableInt64(projected.resultBytes), nullableString(projected.resultHandleID), nullableJSON(projected.argumentsJSON)); err != nil {
			return fmt.Errorf("upsert tool audit: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_trace_events
		(run_id, sequence, type, stage, status, model_name, tool_name, input_tokens, output_tokens, reasoning_tokens, finish_reason, duration_ms, evidence_count, error_code, occurred_at, message, next_stage, metadata_json, prompt_json, output_text)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18::jsonb, $19::jsonb, $20)`,
		projected.runID, projected.sequence, projected.eventType, nullableString(projected.stage), nullableString(projected.status),
		nullableString(projected.modelName), nullableString(projected.toolName), nullableInt(projected.inputTokens),
		nullableInt(projected.outputTokens), nullableInt(projected.reasoningTokens), nullableString(projected.finishReason),
		nullableInt64(projected.durationMS), nullableInt(projected.evidenceCount), nullableString(projected.errorCode), projected.at,
		nullableString(projected.message), nullableString(projected.nextStage), nullableJSON(projected.metadataJSON), nullableJSON(projected.promptJSON), nullableString(projected.outputText)); err != nil {
		return fmt.Errorf("insert trace event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit trace projection: %w", err)
	}
	return nil
}

type projectedEvent struct {
	runID, tenantID, userID, sessionID              string
	sequence                                        int
	eventType, stage, status                        string
	modelName, toolName, stepID                     string
	attempt                                         int
	inputTokens, outputTokens                       int
	reasoningTokens                                 int
	finishReason, errorCode                         string
	durationMS, resultBytes                         int64
	evidenceCount                                   int
	resultHandleID                                  string
	message, nextStage, argumentsJSON, metadataJSON string
	promptJSON, outputText                          string
	isModel                                         bool
	modelTerminal                                   bool
	at                                              time.Time
	isTool, toolComplete                            bool
	toolTerminal                                    bool
}

func project(event core.TraceEvent) (projectedEvent, bool) {
	if !allowedTraceType(event.Type) {
		return projectedEvent{}, false
	}
	result := projectedEvent{
		runID: event.RunID, tenantID: event.TenantID, userID: event.UserID, sessionID: event.SessionID,
		sequence: event.Sequence, eventType: event.Type, stage: event.Stage.String(), status: event.Status,
		modelName: event.ModelName, toolName: event.ToolName, stepID: event.StepID, attempt: event.Attempt,
		inputTokens: event.InputTokens, outputTokens: event.OutputTokens, reasoningTokens: event.ReasoningTokens,
		finishReason: event.FinishReason, durationMS: event.DurationMS, resultBytes: event.ResultBytes,
		resultHandleID: event.ResultHandleID, evidenceCount: event.EvidenceCount, at: event.At,
	}
	result.message = event.Message
	result.promptJSON = sanitizeJSON(event.Prompt)
	result.outputText = event.Output
	// 节点不再在输出中携带下一跳；路由决定由编排层单独记录。
	result.nextStage = ""
	result.argumentsJSON = sanitizeJSON(event.ToolArguments)
	metadata := map[string]any{"step_id": event.StepID, "attempt": event.Attempt, "result_bytes": event.ResultBytes, "evidence_count": event.EvidenceCount}
	for key, value := range event.Metadata {
		metadata[key] = value
	}
	result.metadataJSON = sanitizeJSON(metadata)
	if strings.HasPrefix(event.Type, "tool.") {
		result.isTool = true
		result.toolComplete = event.Type != "tool.started"
		result.toolTerminal = result.toolComplete
		if result.stepID == "" {
			result.stepID = "unplanned"
		}
	}
	result.isModel = strings.HasPrefix(event.Type, "model.")
	result.modelTerminal = event.Type == "model.completed" || event.Type == "model.failed"
	if event.Type == "run.failed" || strings.HasSuffix(event.Type, ".failed") {
		result.errorCode = fixedErrorCode(event.Message)
	}
	return result, true
}

func nullableJSON(value string) any {
	if value == "" || value == "null" {
		return nil
	}
	return value
}

func sanitizeJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func allowedTraceType(kind string) bool {
	if kind == "run.started" || kind == "run.completed" || kind == "run.failed" || kind == "node.completed" || kind == "node.failed" || kind == "node.output" {
		return true
	}
	if kind == "message.completed" {
		return true
	}
	if kind == "model.started" || kind == "model.completed" || kind == "model.failed" || kind == "model.prompt" || kind == "model.output" {
		return true
	}
	if kind == "evidence.compiled" || kind == "evidence.verified" || kind == "evidence.verification_failed" || kind == "evidence.persisted" || kind == "evidence.prune_failed" {
		return true
	}
	if kind == "session.entities.inherited" || kind == "session.entities.updated" {
		return true
	}
	return kind == "tool.started" || kind == "tool.completed" || kind == "tool.failed" || kind == "tool.skipped"
}

func fixedErrorCode(message string) string {
	value := strings.ToLower(message)
	switch {
	case strings.Contains(value, "deadline"), strings.Contains(value, "timeout"):
		return "timeout"
	case strings.Contains(value, "cancel"):
		return "canceled"
	case strings.Contains(value, "permission"), strings.Contains(value, "unauthor"), strings.Contains(value, "authoriz"), strings.Contains(value, "forbidden"):
		return "permission_denied"
	case strings.Contains(value, "model"):
		return "model_error"
	case strings.Contains(value, "tool"), strings.Contains(value, "mcp"):
		return "tool_error"
	default:
		return "internal_error"
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

var _ TraceSink = (*PostgresSink)(nil)
