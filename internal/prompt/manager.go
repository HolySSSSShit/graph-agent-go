package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// Manager 只提供跨 Workflow 的全局提示模板。
// 提示正文位于 prompts/global.json，修改后无需重新编译。
type Manager struct {
	Directory string
	modules   *workflowModules
}

type workflowModules struct {
	mu      sync.RWMutex
	modules map[string]core.PromptManager
}

// NewManager 创建全局提示管理器。提示文件按请求读取，不缓存正文。
func NewManager(directory string) *Manager {
	return &Manager{Directory: directory, modules: &workflowModules{modules: make(map[string]core.PromptManager)}}
}

// RegisterWorkflow 注册一个 Workflow 的节点提示模块，并返回已合并的视图。
// Manager 只负责把全局 system/recovery/session 模板与模块模板合并。
func (m *Manager) RegisterWorkflow(id string, module core.PromptManager) (core.PromptManager, error) {
	if m == nil || strings.TrimSpace(id) == "" || module == nil {
		return nil, errors.New("workflow prompt module is not configured")
	}
	if m.modules == nil {
		m.modules = &workflowModules{modules: make(map[string]core.PromptManager)}
	}
	m.modules.mu.Lock()
	m.modules.modules[id] = module
	m.modules.mu.Unlock()
	return m.Workflow(id)
}

// Workflow 返回一个按 Workflow 隔离的提示视图。
func (m *Manager) Workflow(id string) (core.PromptManager, error) {
	if m == nil || m.modules == nil {
		return nil, fmt.Errorf("workflow prompt module %q is not registered", id)
	}
	m.modules.mu.RLock()
	module := m.modules.modules[id]
	m.modules.mu.RUnlock()
	if module == nil {
		return nil, fmt.Errorf("workflow prompt module %q is not registered", id)
	}
	return WorkflowManager{Global: *m, Module: module}, nil
}

// JSONModule 从指定 JSON 文件加载一个 Workflow 的节点提示模块。
// 它不携带任何业务名称，正文修改后无需重新编译。
type JSONModule struct {
	Directory       string
	File            string
	ProtocolSchemas map[string]any
}

func (m Manager) Get(_ context.Context, id, _ string) (core.PromptTemplate, error) {
	if !isGlobalPrompt(id) {
		return core.PromptTemplate{}, fmt.Errorf("global prompt template %q is not configured", id)
	}
	return loadTemplate(m.Directory, "global.json", id, nil)
}

// WorkflowManager 合并全局模板和一个已注册 Workflow 的节点模板。
type WorkflowManager struct {
	Global core.PromptManager
	Module core.PromptManager
}

func (m WorkflowManager) Get(ctx context.Context, id, variant string) (core.PromptTemplate, error) {
	if id == core.PromptSystem {
		global, err := m.Global.Get(ctx, id, variant)
		if err != nil {
			return core.PromptTemplate{}, err
		}
		workflow, err := m.Module.Get(ctx, id, variant)
		if err != nil {
			return core.PromptTemplate{}, err
		}
		global.Version = workflow.Version
		global.Sections = append(global.Sections, workflow.Sections...)
		return global, nil
	}
	if id == core.PromptExecutionRecovery || id == core.PromptSessionCompact {
		return m.Global.Get(ctx, id, variant)
	}
	return m.Module.Get(ctx, id, variant)
}

func (m JSONModule) Get(_ context.Context, id, _ string) (core.PromptTemplate, error) {
	if strings.TrimSpace(m.File) == "" {
		return core.PromptTemplate{}, errors.New("json prompt module file is not configured")
	}
	return loadTemplate(m.Directory, m.File, id, m.ProtocolSchemas)
}

func isGlobalPrompt(id string) bool {
	return id == core.PromptSystem || id == core.PromptExecutionRecovery || id == core.PromptSessionCompact
}

type fileTemplate struct {
	ID       string               `json:"id"`
	Version  string               `json:"version"`
	Sections []core.PromptSection `json:"sections"`
	Protocol fileProtocol         `json:"protocol"`
}

type filePromptSet struct {
	Templates []fileTemplate `json:"templates"`
}

type fileProtocol struct {
	Name    string               `json:"name"`
	Version string               `json:"version"`
	Format  string               `json:"format"`
	Fields  []core.ProtocolField `json:"fields"`
	Rules   []string             `json:"rules"`
}

func loadTemplate(directory, name, id string, schemas map[string]any) (core.PromptTemplate, error) {
	path, err := promptPath(directory, name)
	if err != nil {
		return core.PromptTemplate{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return core.PromptTemplate{}, fmt.Errorf("read prompt file %s: %w", path, err)
	}
	var set filePromptSet
	if err := json.Unmarshal(data, &set); err != nil {
		return core.PromptTemplate{}, fmt.Errorf("decode prompt file %s: %w", path, err)
	}
	for _, item := range set.Templates {
		if item.ID != id {
			continue
		}
		if item.Version == "" {
			item.Version = "v1"
		}
		return core.PromptTemplate{ID: item.ID, Version: item.Version, Sections: item.Sections, Protocol: bindProtocol(item.Protocol, schemas[item.Protocol.Name])}, nil
	}
	return core.PromptTemplate{}, fmt.Errorf("prompt template %q not found in %s", id, path)
}

func bindProtocol(value fileProtocol, schema any) core.ResponseProtocol {
	return core.ResponseProtocol{Name: value.Name, Version: value.Version, Format: value.Format, Fields: value.Fields, Rules: value.Rules, Schema: schema}
}

func promptPath(directory, name string) (string, error) {
	if directory != "" {
		return filepath.Join(directory, name), nil
	}
	candidates := make([]string, 0, 3)
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "prompts", name))
		for parent := filepath.Dir(cwd); parent != cwd; parent = filepath.Dir(parent) {
			candidates = append(candidates, filepath.Join(parent, "prompts", name))
			cwd = parent
		}
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		candidates = append(candidates, filepath.Join(dir, "prompts", name), filepath.Join(dir, "..", "prompts", name))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("prompt file %s not found; configure prompt directory", name)
}
