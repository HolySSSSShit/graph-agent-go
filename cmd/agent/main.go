package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/HolySSSSShit/graph-agent-go/internal/api"
	"github.com/HolySSSSShit/graph-agent-go/internal/config"
	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"
	"github.com/HolySSSSShit/graph-agent-go/internal/policy"
	"github.com/HolySSSSShit/graph-agent-go/internal/prompt"
	"github.com/HolySSSSShit/graph-agent-go/internal/session"
	"github.com/HolySSSSShit/graph-agent-go/internal/workflow/orderexample"
)

// 核心发行版装配通用基础设施和不访问真实业务数据的示例 Workflow。
func main() {
	cfg := config.Load()
	sessions, closeStore, err := openSessionStore(context.Background(), cfg.Database)
	if err != nil {
		log.Fatal(err)
	}
	if closeStore != nil {
		defer func() {
			if err := closeStore(); err != nil {
				log.Printf("close session store: %v", err)
			}
		}()
	}
	definition, err := orderexample.New(orderexample.Dependencies{Prompts: prompt.NewManager(cfg.Prompts.Directory)})
	if err != nil {
		log.Fatal(err)
	}
	workflows, err := orchestrator.NewWorkflowRegistry(orderexample.WorkflowID, definition)
	if err != nil {
		log.Fatal(err)
	}
	runner := &orchestrator.Runner{
		Workflows: workflows, Sessions: sessions, MaxSteps: cfg.Runtime.MaxSteps,
		RequestTimeout: time.Duration(cfg.Runtime.RequestTimeoutSeconds) * time.Second,
	}
	if checkpoints, ok := sessions.(core.CheckpointStore); ok {
		runner.Checkpoints = checkpoints
	}
	if leases, ok := sessions.(core.RunLeaseStore); ok {
		runner.Leases = leases
		runner.LeaseDuration = 30 * time.Second
	}
	handler := (&api.Handler{Runner: runner, Sessions: sessions, Runs: api.NewRunManager(runner), Policy: policy.Guard{}, PublicChatWorkflow: orderexample.WorkflowID}).Routes()
	log.Printf("core agent service listening on %s", cfg.Server.Listen)
	log.Fatal(http.ListenAndServe(cfg.Server.Listen, handler))
}

func openSessionStore(ctx context.Context, cfg config.DatabaseConfig) (core.SessionStore, func() error, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", "mock", "memory":
		return session.New(), nil, nil
	case "postgres", "postgresql", "pgx":
		store, err := session.NewPostgres(ctx, cfg.URL, cfg.Timezone)
		if err != nil {
			return nil, nil, err
		}
		return store, store.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported session database driver: %s", cfg.Driver)
	}
}
