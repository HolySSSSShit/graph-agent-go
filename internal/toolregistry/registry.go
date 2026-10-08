package toolregistry

import (
	"context"
	"errors"
	"fmt"
	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"sort"
	"sync"
	"time"
)

const defaultMaxSessionCatalogs = 1024

type sessionKey struct {
	tenantID  string
	userID    string
	sessionID string
}

type sessionCatalog struct {
	tools    map[string]sessionTool
	lastUsed time.Time
}

type Registry struct {
	mu          sync.RWMutex
	sources     map[string]core.ToolSource
	sessions    map[sessionKey]sessionCatalog
	internal    map[string]core.InternalToolRegistration
	maxSessions int
}

type sessionTool struct {
	tool   core.Tool
	source string
}

func New() *Registry {
	return &Registry{sources: map[string]core.ToolSource{}, sessions: map[sessionKey]sessionCatalog{}, internal: map[string]core.InternalToolRegistration{}, maxSessions: defaultMaxSessionCatalogs}
}
func (r *Registry) Register(s core.ToolSource) error {
	if s == nil {
		return errors.New("nil tool source")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sources[s.Name()]; exists {
		return fmt.Errorf("tool source already registered: %s", s.Name())
	}
	r.sources[s.Name()] = s
	return nil
}

// RegisterInternal 注册 Harness 自有工具。
// 内部工具参与正常计划和审计流程，但不计入业务工具预算。
func (r *Registry) RegisterInternal(tool core.Tool, handler core.InternalToolHandler) error {
	return r.RegisterInternalBatch([]core.InternalToolRegistration{{Definition: tool, Handler: handler}})
}

// RegisterInternalBatch 原子注册一组内部工具。定义由调用方所属包提供，
// Registry 只负责名称冲突检查、存储和解析。
func (r *Registry) RegisterInternalBatch(tools []core.InternalToolRegistration) error {
	normalized := append([]core.InternalToolRegistration(nil), tools...)
	for index := range normalized {
		if normalized[index].Definition.Name == "" || normalized[index].Handler == nil {
			return errors.New("internal tool name and handler are required")
		}
		normalized[index].Definition.Internal = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]struct{}, len(normalized))
	for _, item := range normalized {
		if _, exists := r.internal[item.Definition.Name]; exists {
			return fmt.Errorf("internal tool already registered: %s", item.Definition.Name)
		}
		if _, exists := seen[item.Definition.Name]; exists {
			return fmt.Errorf("internal tool is registered more than once in batch: %s", item.Definition.Name)
		}
		seen[item.Definition.Name] = struct{}{}
	}
	for _, item := range normalized {
		r.internal[item.Definition.Name] = item
	}
	return nil
}
func (r *Registry) LoadSession(ctx context.Context, scope core.ToolScope) error {
	return r.Refresh(ctx, "mcp", scope)
}
func (r *Registry) Get(scope core.ToolScope) ([]core.Tool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := toolSessionKey(scope)
	catalog, ok := r.sessions[key]
	if !ok {
		return nil, false
	}
	catalog.lastUsed = time.Now()
	r.sessions[key] = catalog
	out := make([]core.Tool, 0, len(catalog.tools)+len(r.internal))
	for _, item := range catalog.tools {
		out = append(out, item.tool)
	}
	for _, item := range r.internal {
		out = append(out, item.Definition)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, true
}
func (r *Registry) Resolve(scope core.ToolScope, n string) (core.ToolSource, core.Tool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item, ok := r.internal[n]; ok {
		return internalSource{name: n, handler: item.Handler}, item.Definition, true
	}
	key := toolSessionKey(scope)
	catalog, ok := r.sessions[key]
	if !ok {
		return nil, core.Tool{}, false
	}
	catalog.lastUsed = time.Now()
	r.sessions[key] = catalog
	item, ok := catalog.tools[n]
	if !ok {
		return nil, core.Tool{}, false
	}
	source := r.sources[item.source]
	if source == nil {
		return nil, core.Tool{}, false
	}
	return source, item.tool, true
}

type internalSource struct {
	name    string
	handler core.InternalToolHandler
}

func (s internalSource) Name() string { return "internal:" + s.name }

func (internalSource) ListTools(context.Context, core.ToolScope) ([]core.Tool, error) {
	return nil, errors.New("internal tool source does not list tools")
}

func (s internalSource) CallTool(ctx context.Context, scope core.ToolScope, _ string, arguments map[string]any) (core.ToolResult, error) {
	return s.handler(ctx, scope, arguments)
}
func (r *Registry) Refresh(ctx context.Context, source string, scope core.ToolScope) error {
	r.mu.RLock()
	registered := r.sources[source]
	r.mu.RUnlock()
	if registered == nil {
		return fmt.Errorf("tool source not registered: %s", source)
	}
	tools, err := registered.ListTools(ctx, scope)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := toolSessionKey(scope)
	catalog := r.sessions[key]
	next := make(map[string]sessionTool, len(tools))
	for name, item := range catalog.tools {
		if item.source != source {
			next[name] = item
		}
	}
	for _, tool := range tools {
		if tool.Name == "" {
			return fmt.Errorf("tool source %s returned an empty tool name", source)
		}
		if existing, exists := next[tool.Name]; exists && existing.source != source {
			return fmt.Errorf("tool %s is provided by both %s and %s", tool.Name, existing.source, source)
		}
		next[tool.Name] = sessionTool{tool: tool, source: source}
	}
	if _, exists := r.sessions[key]; !exists {
		r.evictOldestLocked()
	}
	r.sessions[key] = sessionCatalog{tools: next, lastUsed: time.Now()}
	return nil
}
func (r *Registry) Invalidate(scope core.ToolScope) {
	r.mu.Lock()
	delete(r.sessions, toolSessionKey(scope))
	r.mu.Unlock()
}

func toolSessionKey(scope core.ToolScope) sessionKey {
	return sessionKey{tenantID: scope.TenantID, userID: scope.UserID, sessionID: scope.SessionID}
}

func (r *Registry) evictOldestLocked() {
	if r.maxSessions < 1 || len(r.sessions) < r.maxSessions {
		return
	}
	var oldestKey sessionKey
	var oldest time.Time
	for key, catalog := range r.sessions {
		if oldest.IsZero() || catalog.lastUsed.Before(oldest) {
			oldestKey = key
			oldest = catalog.lastUsed
		}
	}
	delete(r.sessions, oldestKey)
}
