package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"

	"github.com/BurntSushi/toml"
)

const (
	// 默认模型输出上限，给复杂分析和工具编排留出更充足的空间。
	defaultFlashMaxTokens = 20480
	defaultProMaxTokens   = 40960

	// 默认上下文预算。上下文仍会按类别和优先级裁剪，预算增大不代表无限拼接历史。
	defaultToolResultMaxTokens = 8192
	defaultCompactKeepMessages = 12
	defaultCompactMaxTokens    = 2048
)

// Config 汇总 Agent 运行所需的全部配置。
type Config struct {
	Server   ServerConfig   `toml:"server"`
	Models   ModelConfig    `toml:"model"`
	MCP      MCPConfig      `toml:"mcp"`
	Database DatabaseConfig `toml:"database"`
	Runtime  RuntimeConfig  `toml:"runtime"`
	Context  ContextConfig  `toml:"context"`
	Skills   SkillConfig    `toml:"skills"`
	Prompts  PromptConfig   `toml:"prompts"`
	Logging  LoggingConfig  `toml:"logging"`
}

type ServerConfig struct {
	Listen     string `toml:"listen"`
	LogLevel   string `toml:"log_level"`
	SSEEnabled bool   `toml:"sse_enabled"`
}

// ModelConfig 保存共享的提供商设置；路由使用 provider:model 引用。
type ModelConfig struct {
	Default   string                    `toml:"default"`
	Providers map[string]ProviderConfig `toml:"providers"`
	Roles     map[core.ModelRole]string `toml:"roles"`
	Image     ImageModelConfig          `toml:"image"`
	// Models 仅用于读取旧的本地配置；新配置使用 Providers。
	Models []ModelOption `toml:"models"`
}

// ImageModelConfig 描述图片生成接口、失败重试和备用模型。
type ImageModelConfig struct {
	BaseURL               string   `toml:"base_url"`
	Model                 string   `toml:"model"`
	Mode                  string   `toml:"mode"`
	FallbackModel         string   `toml:"fallback_model"`
	FallbackMode          string   `toml:"fallback_mode"`
	Quality               string   `toml:"quality"`
	URLs                  []string `toml:"urls"`
	RequestTimeoutSeconds int      `toml:"request_timeout_seconds"`
	RetryCount            int      `toml:"retry_count"`
}

// ModelOption 是解析后的提供商/模型组合。模型配置按提供商保存；该值只在路由解析后使用。
type ModelOption struct {
	Name              string  `toml:"name"`
	Provider          string  `toml:"provider"`
	BaseURL           string  `toml:"base_url"`
	APIKey            string  `toml:"api_key"`
	Temperature       float32 `toml:"temperature"`
	TopP              float32 `toml:"top_p"`
	MaxTokens         int     `toml:"max_tokens"`
	StructuredOutputs bool    `toml:"structured_outputs"`
	EnableThinking    *bool   `toml:"enable_thinking"`
	ThinkingBudget    int     `toml:"thinking_budget"`
}

// ProviderConfig 保存共享的连接和生成默认值。
// 各角色选择具体模型名称，此处不重复保存模型名。
type ProviderConfig struct {
	BaseURL           string  `toml:"base_url"`
	APIKey            string  `toml:"api_key"`
	Temperature       float32 `toml:"temperature"`
	TopP              float32 `toml:"top_p"`
	MaxTokens         int     `toml:"max_tokens"`
	StructuredOutputs bool    `toml:"structured_outputs"`
	EnableThinking    *bool   `toml:"enable_thinking"`
	ThinkingBudget    int     `toml:"thinking_budget"`
}

type MCPConfig struct {
	BaseURL               string `toml:"base_url"`
	Token                 string `toml:"token"`
	ProtocolVersion       string `toml:"protocol_version"`
	RequestTimeoutSeconds int    `toml:"request_timeout_seconds"`
	MaxResponseBytes      int64  `toml:"max_response_bytes"`
	ShadowDirectory       string `toml:"shadow_directory"`
}

type DatabaseConfig struct {
	Driver   string `toml:"driver"`
	URL      string `toml:"url"`
	Timezone string `toml:"timezone"`
}

type RuntimeConfig struct {
	MaxSteps              int `toml:"max_steps"`
	MaxToolCalls          int `toml:"max_tool_calls"`
	MaxRetries            int `toml:"max_retries"`
	RequestTimeoutSeconds int `toml:"request_timeout_seconds"`
}

type ContextConfig struct {
	MaxTokens           int `toml:"max_tokens"`
	ReserveTokens       int `toml:"reserve_tokens"`
	ToolResultMaxTokens int `toml:"tool_result_max_tokens"`
	CompactKeepMessages int `toml:"compact_keep_messages"`
	CompactMaxTokens    int `toml:"compact_max_tokens"`
}

type LoggingConfig struct {
	AccessFile    string `toml:"access_file"`
	AuditFile     string `toml:"audit_file"`
	DatabaseTrace bool   `toml:"database_trace"`
}

type SkillConfig struct {
	Enabled        bool    `toml:"enabled"`
	Directory      string  `toml:"directory"`
	Catalog        string  `toml:"catalog"`
	MaxItems       int     `toml:"max_items"`
	MaxTokenBudget int     `toml:"max_token_budget"`
	MinMatchScore  float64 `toml:"min_match_score"`
}

type PromptConfig struct {
	Directory string `toml:"directory"`
}

// Load 从 TOML 文件加载配置。AGENT_CONFIG_FILE 可覆盖默认文件路径。
func Load() Config {
	loadEnvFile()
	path := os.Getenv("AGENT_CONFIG_FILE")
	if path == "" {
		path = "config.toml"
	}
	cfg := defaultConfig()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg
	}
	normalizeLegacyModels(&cfg.Models)
	applyEnvironmentOverrides(&cfg)
	return cfg
}

// loadEnvFile 为本地开发加载 .env 中简单的 KEY=VALUE 项。
// 进程环境变量优先，文件不存在时忽略。
func loadEnvFile() {
	path := os.Getenv("AGENT_ENV_FILE")
	if path == "" {
		path = ".env"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || os.Getenv(key) != "" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		_ = os.Setenv(key, value)
	}
}

func normalizeLegacyModels(models *ModelConfig) {
	if models == nil || len(models.Models) == 0 {
		return
	}
	if models.Providers == nil {
		models.Providers = map[string]ProviderConfig{}
	}
	for _, option := range models.Models {
		if _, exists := models.Providers[option.Provider]; exists {
			continue
		}
		models.Providers[option.Provider] = ProviderConfig{BaseURL: option.BaseURL, APIKey: option.APIKey, Temperature: option.Temperature, TopP: option.TopP, MaxTokens: option.MaxTokens, StructuredOutputs: option.StructuredOutputs, EnableThinking: option.EnableThinking, ThinkingBudget: option.ThinkingBudget}
	}
}

// applyEnvironmentOverrides 允许密钥通过运行环境注入，避免把凭证写入配置文件或日志。
func applyEnvironmentOverrides(cfg *Config) {
	if cfg == nil {
		return
	}
	for provider, envPrefix := range map[string]string{"qwen": "QWEN", "gemini": "GEMINI", "openai": "OPENAI", "newapi": "NEW_API"} {
		if providerConfig, ok := cfg.Models.Providers[provider]; ok {
			if value := os.Getenv(envPrefix + "_API_KEY"); value != "" {
				providerConfig.APIKey = value
			}
			if value := os.Getenv(envPrefix + "_BASE_URL"); value != "" {
				providerConfig.BaseURL = value
			}
			cfg.Models.Providers[provider] = providerConfig
		}
	}
	if value := os.Getenv("QWEN_IMAGE_BASE_URL"); value != "" {
		cfg.Models.Image.BaseURL = value
	}
	if value := os.Getenv("QWEN_IMAGE_MODEL"); value != "" {
		cfg.Models.Image.Model = value
	}
	if value := os.Getenv("IMAGE_REQUEST_TIMEOUT_SECONDS"); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			cfg.Models.Image.RequestTimeoutSeconds = seconds
		}
	}
	for index := range cfg.Models.Models {
		option := &cfg.Models.Models[index]
		if value := os.Getenv(strings.ToUpper(option.Provider) + "_API_KEY"); value != "" {
			option.APIKey = value
		}
		if value := os.Getenv(strings.ToUpper(option.Provider) + "_BASE_URL"); value != "" {
			option.BaseURL = value
		}
	}
	if value := os.Getenv("MCP_TOKEN"); value != "" {
		cfg.MCP.Token = value
	}
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{Listen: "127.0.0.1:8081", LogLevel: "info", SSEEnabled: true},
		Models: ModelConfig{
			Default: "gemini:gemini-2.5-flash",
			Providers: map[string]ProviderConfig{
				"gemini": {BaseURL: "https://generativelanguage.googleapis.com/v1beta/models", MaxTokens: defaultFlashMaxTokens, Temperature: 0.2, TopP: 1},
			},
			Roles: map[core.ModelRole]string{
				core.ModelRoleDefault: "gemini:gemini-2.5-flash", core.ModelRoleIntent: "gemini:gemini-2.5-flash", core.ModelRoleSkill: "gemini:gemini-2.5-flash", core.ModelRoleReason: "gemini:gemini-2.5-flash", core.ModelRoleReasonEscalation: "gemini:gemini-2.5-pro", core.ModelRoleReview: "gemini:gemini-2.5-flash", core.ModelRoleFinalize: "gemini:gemini-2.5-flash", core.ModelRoleCompact: "gemini:gemini-2.5-flash", core.ModelRoleImage: "gemini:gemini-2.5-flash",
			},
			Image: ImageModelConfig{RequestTimeoutSeconds: 60, RetryCount: 1, Quality: "low", FallbackMode: "newapi-gemini", FallbackModel: "gemini-3.1-flash-image"},
		},
		MCP: MCPConfig{
			BaseURL:               "http://127.0.0.1:8080/mcp",
			Token:                 "demo-token",
			ProtocolVersion:       "2025-06-18",
			RequestTimeoutSeconds: 20,
			MaxResponseBytes:      1 << 20,
			ShadowDirectory:       "data/shadow",
		},
		Database: DatabaseConfig{Driver: "mock", URL: "mock://postgres", Timezone: core.DefaultTimezone},
		Runtime: RuntimeConfig{
			MaxSteps: 64, MaxToolCalls: 5, MaxRetries: 2, RequestTimeoutSeconds: 300,
		},
		Context: ContextConfig{
			MaxTokens:           core.DefaultContextMaxTokens,
			ReserveTokens:       core.DefaultContextReserveTokens,
			ToolResultMaxTokens: defaultToolResultMaxTokens,
			CompactKeepMessages: defaultCompactKeepMessages,
			CompactMaxTokens:    defaultCompactMaxTokens,
		},
		Skills:  SkillConfig{Enabled: false, Directory: "skills", Catalog: "catalog.json", MaxItems: 3, MaxTokenBudget: 4096, MinMatchScore: 0.5},
		Prompts: PromptConfig{Directory: "prompts"},
		Logging: LoggingConfig{AccessFile: "logs/access.log", AuditFile: "logs/audit.log", DatabaseTrace: true},
	}
}

func (c ModelConfig) Find(name string) (ModelOption, bool) {
	provider, modelName := splitModelRef(name)
	if provider == "" {
		for _, option := range c.Models {
			if option.Name == name {
				return option, true
			}
		}
		return ModelOption{}, false
	}
	providerConfig, ok := c.Providers[provider]
	if !ok || modelName == "" {
		return ModelOption{}, false
	}
	return ModelOption{Name: name, Provider: provider, BaseURL: providerConfig.BaseURL, APIKey: providerConfig.APIKey,
		Temperature: providerConfig.Temperature, TopP: providerConfig.TopP, MaxTokens: providerConfig.MaxTokens,
		StructuredOutputs: providerConfig.StructuredOutputs, EnableThinking: providerConfig.EnableThinking,
		ThinkingBudget: providerConfig.ThinkingBudget}, true
}

func splitModelRef(ref string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(ref), ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}
