package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultCompactionUserAnchorBudget(t *testing.T) {
	cfg := Default()
	if cfg.CompactionSettings.PreserveUserMessagesTokens != 1024 {
		t.Fatalf("expected default user anchor budget 1024, got %d", cfg.CompactionSettings.PreserveUserMessagesTokens)
	}
}

func TestLoadPreservesCompactionUserAnchorBudgetWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	cwd := filepath.Join(dir, "repo")
	settingsDir := filepath.Join(cwd, ".coding_agent")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{
  "compactionSettings": {
    "preserveRecentMessages": 2
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(agentDir, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompactionSettings.PreserveRecentMessages != 2 {
		t.Fatalf("expected project preserveRecentMessages override, got %d", cfg.CompactionSettings.PreserveRecentMessages)
	}
	if cfg.CompactionSettings.PreserveUserMessagesTokens != 1024 {
		t.Fatalf("expected omitted user anchor budget to preserve default 1024, got %d", cfg.CompactionSettings.PreserveUserMessagesTokens)
	}
}

func TestLoadReadsGlobalConfigTomlSettings(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".modu")
	cwd := filepath.Join(dir, "repo")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalConfigPath(agentDir), []byte(`version = 2

[settings]
thinkingLevel = "high"
autoCompaction = false
disableWorkflows = true

[settings.features]
memoryTool = false

[settings.permissions]
defaultMode = "auto"

[settings.webSearch]
provider = "exa"
apiKeyEnv = "EXA_API_KEY"
searchType = "fast"

[settings.webFetch]
provider = "firecrawl"
apiKeyEnv = "FIRECRAWL_API_KEY"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(agentDir, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThinkingLevel != "high" {
		t.Fatalf("thinking level = %q, want high", cfg.ThinkingLevel)
	}
	if cfg.AutoCompaction {
		t.Fatal("auto compaction should be false from config.toml [settings]")
	}
	if !cfg.DisableWorkflows {
		t.Fatal("disableWorkflows should be true from config.toml [settings]")
	}
	if cfg.FeatureMemoryTool() {
		t.Fatal("memoryTool should be false from config.toml [settings.features]")
	}
	if cfg.Permissions.DefaultMode != "auto" {
		t.Fatalf("permissions.defaultMode = %q, want auto", cfg.Permissions.DefaultMode)
	}
	if cfg.WebSearch.Provider != "exa" || cfg.WebSearch.APIKeyEnv != "EXA_API_KEY" || cfg.WebSearch.SearchType != "fast" {
		t.Fatalf("webSearch config not loaded: %#v", cfg.WebSearch)
	}
	if cfg.WebFetch.Provider != "firecrawl" || cfg.WebFetch.APIKeyEnv != "FIRECRAWL_API_KEY" {
		t.Fatalf("webFetch config not loaded: %#v", cfg.WebFetch)
	}
}

func TestLoadReadsRootMCPServersFromGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".modu")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalConfigPath(agentDir), []byte(`version = 2

[settings]
disableWorkflows = true

[mcp_servers.echo]
command = "echo-server"
args = ["--stdio"]
env = { TOKEN = "secret" }
cwd = "/tmp/mcp"
enabled = false
required = true
startup_timeout_sec = 3.5
tool_timeout_sec = 7
enabled_tools = ["read", "search"]
disabled_tools = ["search"]

[mcp_servers.remote]
url = "https://example.com/mcp"
bearer_token_env_var = "REMOTE_MCP_TOKEN"
http_headers = { X-Static = "static" }
env_http_headers = { X-Dynamic = "REMOTE_MCP_HEADER" }
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(agentDir, "")
	if err != nil {
		t.Fatal(err)
	}
	server, ok := cfg.MCPServers["echo"]
	if !ok {
		t.Fatalf("root mcp_servers.echo not loaded: %#v", cfg.MCPServers)
	}
	if !cfg.DisableWorkflows {
		t.Fatal("global settings were not loaded alongside root mcp_servers")
	}
	if server.Command != "echo-server" || !reflect.DeepEqual(server.Args, []string{"--stdio"}) {
		t.Fatalf("unexpected MCP command config: %#v", server)
	}
	if server.Env["TOKEN"] != "secret" || server.Cwd != "/tmp/mcp" {
		t.Fatalf("unexpected MCP environment config: %#v", server)
	}
	if server.Enabled == nil || *server.Enabled || !server.Required {
		t.Fatalf("unexpected MCP enable/required config: %#v", server)
	}
	if server.StartupTimeoutSec != 3.5 || server.ToolTimeoutSec != 7 {
		t.Fatalf("unexpected MCP timeout config: %#v", server)
	}
	if !reflect.DeepEqual(server.EnabledTools, []string{"read", "search"}) || !reflect.DeepEqual(server.DisabledTools, []string{"search"}) {
		t.Fatalf("unexpected MCP tool filters: %#v", server)
	}
	remote, ok := cfg.MCPServers["remote"]
	if !ok {
		t.Fatalf("root mcp_servers.remote not loaded: %#v", cfg.MCPServers)
	}
	if remote.URL != "https://example.com/mcp" || remote.BearerTokenEnvVar != "REMOTE_MCP_TOKEN" {
		t.Fatalf("unexpected remote MCP config: %#v", remote)
	}
	if remote.HTTPHeaders["X-Static"] != "static" || remote.EnvHTTPHeaders["X-Dynamic"] != "REMOTE_MCP_HEADER" {
		t.Fatalf("unexpected remote MCP headers: %#v", remote)
	}
}

func TestLoadProjectMCPServersOverrideGlobalEntry(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".modu")
	cwd := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(cwd, ".coding_agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalConfigPath(agentDir), []byte(`[mcp_servers.echo]
command = "global-server"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectSettingsPath(cwd), []byte(`{
  "mcpServers": {
    "echo": {"command": "project-server", "args": ["--project"]},
    "local": {"command": "local-server"}
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(agentDir, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MCPServers["echo"].Command; got != "project-server" {
		t.Fatalf("project MCP override command = %q", got)
	}
	if got := cfg.MCPServers["local"].Command; got != "local-server" {
		t.Fatalf("project MCP server command = %q", got)
	}
}

func TestSaveGlobalConfigPreservesRootMCPServers(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".modu")
	path := GlobalConfigPath(agentDir)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[mcp_servers.echo]
command = "echo-server"
args = ["--stdio"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(agentDir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DisableWorkflows = true
	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"[mcp_servers.echo]", `command = "echo-server"`, `args = ["--stdio"]`, "[settings]", "disableWorkflows = true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %q:\n%s", want, text)
		}
	}
}

func TestLoadMigratesLegacyGlobalSettingsJSONIntoConfigToml(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".modu")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalConfigPath(agentDir), []byte(`version = 2
active = "deepseek"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LegacyGlobalSettingsPath(agentDir), []byte(`{"disableWorkflows":true,"permissions":{"defaultMode":"auto"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(agentDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DisableWorkflows || cfg.Permissions.DefaultMode != "auto" {
		t.Fatalf("legacy settings not loaded: %#v", cfg)
	}
	data, err := os.ReadFile(GlobalConfigPath(agentDir))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`active = "deepseek"`, `[settings]`, `disableWorkflows = true`, `[settings.permissions]`, `defaultMode = "auto"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("migrated config missing %q:\n%s", want, text)
		}
	}
}

func TestSaveGlobalConfigTomlOmitsDefaultsAndEmptySections(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".modu")
	cfg := Default()
	cfg.DisableWorkflows = true
	cfg.Features.MemoryTool = Ptr(false)
	cfg.Permissions.DenyTools = []string{"bash"}
	cfg.WebSearch.Provider = "exa"
	cfg.WebSearch.APIKeyEnv = "EXA_API_KEY"
	cfg.WebFetch.Provider = "firecrawl"
	cfg.WebFetch.APIKeyEnv = "FIRECRAWL_API_KEY"

	if err := Save(cfg, GlobalConfigPath(agentDir)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(GlobalConfigPath(agentDir))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`[settings]`, `disableWorkflows = true`, `[settings.features]`, `memoryTool = false`, `[settings.permissions]`, `denyTools = ["bash"]`, `[settings.webSearch]`, `provider = "exa"`, `apiKeyEnv = "EXA_API_KEY"`, `[settings.webFetch]`, `provider = "firecrawl"`, `apiKeyEnv = "FIRECRAWL_API_KEY"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"retrySettings", "harness", "autoCompaction = true", "thinkingLevel = \"medium\"", "todoTool", "taskOutputTool", "planMode", "worktreeMode"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("saved config should omit %q:\n%s", unwanted, text)
		}
	}
}
