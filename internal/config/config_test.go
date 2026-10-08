package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTOMLConfig(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.toml")
	content := `[server]
listen = ":9000"
sse_enabled = true
[model]
default = "gemini-2.5-pro"
[[model.models]]
name = "gemini-2.5-pro"
provider = "gemini"
base_url = "https://example.invalid"
temperature = 0.1
top_p = 1.0
max_tokens = 4096
enable_thinking = false
[context]
max_tokens = 2048
reserve_tokens = 256
compact_keep_messages = 8
compact_max_tokens = 1536
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_CONFIG_FILE", path)
	cfg := Load()
	if cfg.Server.Listen != ":9000" || cfg.Models.Default != "gemini-2.5-pro" {
		t.Fatalf("配置未正确加载：%+v", cfg)
	}
	option, ok := cfg.Models.Find("gemini-2.5-pro")
	if !ok || option.MaxTokens != 4096 || option.Temperature != 0.1 {
		t.Fatalf("模型目录未正确加载：%+v", cfg.Models)
	}
	if option.EnableThinking == nil || *option.EnableThinking {
		t.Fatalf("模型思考模式配置未正确加载：%+v", option.EnableThinking)
	}
	if cfg.Context.MaxTokens != 2048 || cfg.Context.CompactKeepMessages != 8 || cfg.Context.CompactMaxTokens != 1536 {
		t.Fatalf("上下文配置未正确加载：%+v", cfg.Context)
	}
	if cfg.Context.ToolResultMaxTokens != defaultToolResultMaxTokens {
		t.Fatalf("工具结果预算默认值未保留：%+v", cfg.Context)
	}
}

func TestCoreSkillsAreDisabledByDefault(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Skills.Enabled || cfg.Skills.Directory != "skills" || cfg.Skills.Catalog != "catalog.json" || cfg.Skills.MaxItems <= 0 || cfg.Skills.MaxTokenBudget <= 0 {
		t.Fatalf("核心 Skills 默认配置无效：%+v", cfg.Skills)
	}
}

func TestDefaultMCPRequestTimeoutIsTwentySeconds(t *testing.T) {
	cfg := defaultConfig()
	if cfg.MCP.RequestTimeoutSeconds != 20 {
		t.Fatalf("MCP request timeout = %d, want 20", cfg.MCP.RequestTimeoutSeconds)
	}
}

func TestDefaultRuntimeRequestTimeoutIsFiveMinutes(t *testing.T) {
	if got := defaultConfig().Runtime.RequestTimeoutSeconds; got != 300 {
		t.Fatalf("runtime request timeout = %d, want 300", got)
	}
}

func TestDefaultImageConfiguration(t *testing.T) {
	image := defaultConfig().Models.Image
	if image.RequestTimeoutSeconds != 60 {
		t.Fatalf("image request timeout = %d, want 60", image.RequestTimeoutSeconds)
	}
	if image.Quality != "low" || image.FallbackMode != "newapi-gemini" || image.FallbackModel != "gemini-3.1-flash-image" {
		t.Fatalf("image fallback = %s/%s", image.FallbackMode, image.FallbackModel)
	}
}

func TestLoadInjectsGeminiAndMCPSecretsFromEnvironment(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.toml")
	content := `[model]
default = "gemini-2.5-flash"
[[model.models]]
name = "gemini-2.5-flash"
provider = "gemini"
base_url = "https://example.invalid"
api_key = ""
[mcp]
token = ""
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_CONFIG_FILE", path)
	t.Setenv("GEMINI_API_KEY", "gemini-test-key")
	t.Setenv("MCP_TOKEN", "mcp-test-token")
	cfg := Load()
	option, ok := cfg.Models.Find("gemini-2.5-flash")
	if !ok || option.APIKey != "gemini-test-key" || cfg.MCP.Token != "mcp-test-token" {
		t.Fatalf("environment secrets were not applied: %+v", cfg)
	}
}

func TestLoadReadsDotEnvWithoutOverwritingProcessEnvironment(t *testing.T) {
	directory := t.TempDir()
	envPath := filepath.Join(directory, ".env")
	if err := os.WriteFile(envPath, []byte("QWEN_API_KEY=from-file\nMCP_TOKEN='file-token'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_ENV_FILE", envPath)
	t.Setenv("QWEN_API_KEY", "from-process")
	t.Setenv("MCP_TOKEN", "")
	loadEnvFile()
	if got := os.Getenv("QWEN_API_KEY"); got != "from-process" {
		t.Fatalf("process environment was overwritten: %q", got)
	}
	if got := os.Getenv("MCP_TOKEN"); got != "file-token" {
		t.Fatalf("dotenv value was not loaded: %q", got)
	}
}
