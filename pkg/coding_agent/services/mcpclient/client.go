// Package mcpclient connects coding-agent sessions to external MCP servers.
// It supports the current standard stdio and Streamable HTTP transports:
// lifecycle/version negotiation and JSON-RPC framing are owned by the official
// MCP Go SDK, while this package owns modu tool naming, filtering, timeouts,
// HTTP credentials, result conversion, and connection cleanup.
package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/config"
	"github.com/openmodu/modu/pkg/types"
)

const (
	defaultStartupTimeout      = 10 * time.Second
	defaultToolTimeout         = 60 * time.Second
	maxExportedToolNameLength  = 64
	exportedToolNameHashLength = 8
)

// StartOptions controls how configured MCP transports connect.
type StartOptions struct {
	// Cwd is the session working directory inherited by servers without an
	// explicit cwd.
	Cwd string
	// Stderr receives server diagnostics. Nil leaves child stderr discarded so
	// long-running servers cannot corrupt an interactive terminal UI.
	Stderr io.Writer
	// HTTPClient is the base client for Streamable HTTP connections. Nil uses
	// http.DefaultClient. The client is cloned before configured headers are
	// applied, so callers retain ownership of the original value.
	HTTPClient *http.Client
}

// ResultDetails preserves MCP-specific structured data alongside the normal
// content blocks sent back to the model.
type ResultDetails struct {
	Server            string         `json:"server"`
	Tool              string         `json:"tool"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	Meta              map[string]any `json:"meta,omitempty"`
}

// Manager owns all live MCP client sessions and their exported modu tools.
type Manager struct {
	mu       sync.RWMutex
	sessions []*mcp.ClientSession
	tools    []types.Tool
	warnings []error

	closeOnce sync.Once
	closeErr  error
}

// Start connects enabled servers, discovers all tool pages, and returns
// namespaced modu tools. A required server failure aborts the whole startup and
// closes servers that already connected. Optional server failures are retained
// in Warnings and do not prevent session creation.
func Start(ctx context.Context, servers map[string]config.MCPServerConfig, opts StartOptions) (*Manager, error) {
	manager := &Manager{}
	if len(servers) == 0 {
		return manager, nil
	}

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	exported := map[string]string{}
	for _, name := range names {
		serverCfg := servers[name]
		if !serverCfg.IsEnabled() {
			continue
		}
		session, tools, err := startServer(ctx, name, serverCfg, opts)
		if err == nil {
			candidateNames := map[string]struct{}{}
			for _, tool := range tools {
				if previous, exists := exported[tool.Name()]; exists {
					err = fmt.Errorf("exported tool %q collides with MCP server %q", tool.Name(), previous)
					break
				}
				if _, exists := candidateNames[tool.Name()]; exists {
					err = fmt.Errorf("server exports duplicate normalized tool name %q", tool.Name())
					break
				}
				candidateNames[tool.Name()] = struct{}{}
			}
			if err == nil {
				for toolName := range candidateNames {
					exported[toolName] = name
				}
			}
		}
		if err != nil {
			if session != nil {
				_ = session.Close()
			}
			wrapped := fmt.Errorf("mcp server %q: %w", name, err)
			if serverCfg.Required {
				_ = manager.Close()
				return nil, wrapped
			}
			manager.warnings = append(manager.warnings, wrapped)
			continue
		}
		manager.sessions = append(manager.sessions, session)
		manager.tools = append(manager.tools, tools...)
	}
	return manager, nil
}

func startServer(ctx context.Context, serverName string, serverCfg config.MCPServerConfig, opts StartOptions) (*mcp.ClientSession, []types.Tool, error) {
	command := strings.TrimSpace(serverCfg.Command)
	endpoint := strings.TrimSpace(serverCfg.URL)
	if (command == "") == (endpoint == "") {
		return nil, nil, errors.New("exactly one of command or url is required")
	}

	var transport mcp.Transport
	if command != "" {
		cmd := exec.Command(command, serverCfg.Args...)
		cmd.Dir = resolveServerCwd(opts.Cwd, serverCfg.Cwd)
		if len(serverCfg.Env) > 0 {
			cmd.Env = mergedEnvironment(serverCfg.Env)
		}
		if opts.Stderr != nil {
			cmd.Stderr = opts.Stderr
		}
		transport = &mcp.CommandTransport{Command: cmd}
	} else {
		httpClient, err := configuredHTTPClient(opts.HTTPClient, serverCfg, endpoint)
		if err != nil {
			return nil, nil, err
		}
		transport = &mcp.StreamableClientTransport{
			Endpoint:   endpoint,
			HTTPClient: httpClient,
		}
	}

	startupTimeout := secondsOrDefault(serverCfg.StartupTimeoutSec, defaultStartupTimeout)
	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "modu_code", Version: "0.1.0"}, nil)
	session, err := client.Connect(startupCtx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize MCP client: %w", err)
	}

	toolTimeout := secondsOrDefault(serverCfg.ToolTimeoutSec, defaultToolTimeout)
	var discovered []types.Tool
	for definition, listErr := range session.Tools(startupCtx, nil) {
		if listErr != nil {
			_ = session.Close()
			return nil, nil, fmt.Errorf("list tools: %w", listErr)
		}
		if definition == nil || !toolAllowed(definition.Name, serverCfg.EnabledTools, serverCfg.DisabledTools) {
			continue
		}
		if strings.TrimSpace(definition.Name) == "" {
			_ = session.Close()
			return nil, nil, errors.New("server returned a tool with an empty name")
		}
		discovered = append(discovered, newTool(serverName, definition, session, toolTimeout))
	}
	return session, discovered, nil
}

// Tools returns a stable snapshot of all successfully discovered tools.
func (m *Manager) Tools() []types.Tool {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]types.Tool(nil), m.tools...)
}

// Warnings returns optional-server startup failures.
func (m *Manager) Warnings() []error {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]error(nil), m.warnings...)
}

// ServerCount returns the number of successfully initialized servers.
func (m *Manager) ServerCount() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// Close gracefully closes every MCP client session once. Sessions are closed
// in reverse startup order.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.mu.Lock()
		sessions := append([]*mcp.ClientSession(nil), m.sessions...)
		m.sessions = nil
		m.mu.Unlock()
		var errs []error
		for i := len(sessions) - 1; i >= 0; i-- {
			if err := sessions[i].Close(); err != nil {
				errs = append(errs, err)
			}
		}
		m.closeErr = errors.Join(errs...)
	})
	return m.closeErr
}

type tool struct {
	serverName  string
	original    string
	exported    string
	label       string
	description string
	parameters  any
	session     *mcp.ClientSession
	timeout     time.Duration
}

func newTool(serverName string, definition *mcp.Tool, session *mcp.ClientSession, timeout time.Duration) types.Tool {
	label := strings.TrimSpace(definition.Title)
	if label == "" && definition.Annotations != nil {
		label = strings.TrimSpace(definition.Annotations.Title)
	}
	if label == "" {
		label = definition.Name
	}
	description := strings.TrimSpace(definition.Description)
	if description == "" {
		description = fmt.Sprintf("MCP tool %q", definition.Name)
	}
	description += fmt.Sprintf("\n\nProvided by MCP server %q.", serverName)
	parameters := definition.InputSchema
	if parameters == nil {
		parameters = map[string]any{"type": "object"}
	}
	return &tool{
		serverName:  serverName,
		original:    definition.Name,
		exported:    exportedToolName(serverName, definition.Name),
		label:       label,
		description: description,
		parameters:  parameters,
		session:     session,
		timeout:     timeout,
	}
}

func (t *tool) Name() string        { return t.exported }
func (t *tool) Label() string       { return t.label }
func (t *tool) Description() string { return t.description }
func (t *tool) Parameters() any     { return t.parameters }

func (t *tool) Execute(ctx context.Context, _ string, args map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	result, err := t.session.CallTool(callCtx, &mcp.CallToolParams{Name: t.original, Arguments: args})
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("mcp server %q tool %q: %w", t.serverName, t.original, err)
	}
	content := make([]types.ContentBlock, 0, len(result.Content))
	for _, item := range result.Content {
		content = append(content, convertContent(item)...)
	}
	if len(content) == 0 && result.StructuredContent != nil {
		if data, marshalErr := json.Marshal(result.StructuredContent); marshalErr == nil {
			content = append(content, &types.TextContent{Type: "text", Text: string(data)})
		}
	}
	return types.ToolResult{
		Content: content,
		Details: ResultDetails{
			Server:            t.serverName,
			Tool:              t.original,
			StructuredContent: result.StructuredContent,
			Meta:              map[string]any(result.Meta),
		},
		IsError: result.IsError,
	}, nil
}

func convertContent(item mcp.Content) []types.ContentBlock {
	switch value := item.(type) {
	case *mcp.TextContent:
		return []types.ContentBlock{&types.TextContent{Type: "text", Text: value.Text}}
	case *mcp.ImageContent:
		return []types.ContentBlock{&types.ImageContent{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(value.Data),
			MimeType: value.MIMEType,
		}}
	default:
		data, err := json.Marshal(item)
		if err != nil {
			return []types.ContentBlock{&types.TextContent{Type: "text", Text: fmt.Sprintf("%v", item)}}
		}
		return []types.ContentBlock{&types.TextContent{Type: "text", Text: string(data)}}
	}
}

func exportedToolName(serverName, toolName string) string {
	name := "mcp__" + normalizeName(serverName) + "__" + normalizeName(toolName)
	if len(name) <= maxExportedToolNameLength {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	suffix := "_" + hex.EncodeToString(digest[:exportedToolNameHashLength/2])
	return name[:maxExportedToolNameLength-len(suffix)] + suffix
}

func normalizeName(value string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(value) {
		valid := r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if valid {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		return "unnamed"
	}
	return name
}

func toolAllowed(name string, enabled, disabled []string) bool {
	if len(enabled) > 0 && !containsName(enabled, name) {
		return false
	}
	return !containsName(disabled, name)
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if strings.TrimSpace(name) == want {
			return true
		}
	}
	return false
}

func secondsOrDefault(seconds float64, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds * float64(time.Second))
}

func resolveServerCwd(sessionCwd, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return sessionCwd
	}
	if filepath.IsAbs(configured) || strings.TrimSpace(sessionCwd) == "" {
		return configured
	}
	return filepath.Join(sessionCwd, configured)
}

func mergedEnvironment(overrides map[string]string) []string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func configuredHTTPClient(base *http.Client, serverCfg config.MCPServerConfig, endpoint string) (*http.Client, error) {
	endpointURL, err := url.Parse(endpoint)
	if err != nil || endpointURL.Host == "" || endpointURL.Scheme != "http" && endpointURL.Scheme != "https" {
		return nil, fmt.Errorf("invalid Streamable HTTP url %q", endpoint)
	}
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	roundTripper := client.Transport
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}
	headers := make(http.Header)
	for name, value := range serverCfg.HTTPHeaders {
		headers.Set(name, value)
	}
	for name, envName := range serverCfg.EnvHTTPHeaders {
		if value, ok := os.LookupEnv(strings.TrimSpace(envName)); ok {
			headers.Set(name, value)
		}
	}
	if headers.Get("Authorization") == "" {
		if token := strings.TrimSpace(os.Getenv(strings.TrimSpace(serverCfg.BearerTokenEnvVar))); token != "" {
			headers.Set("Authorization", "Bearer "+token)
		}
	}
	client.Transport = headerRoundTripper{base: roundTripper, headers: headers, origin: endpointURL}
	return &client, nil
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers http.Header
	origin  *url.URL
}

func (t headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if sameOrigin(req.URL, t.origin) {
		for name, values := range t.headers {
			if clone.Header.Get(name) != "" {
				continue
			}
			clone.Header[name] = append([]string(nil), values...)
		}
	}
	return t.base.RoundTrip(clone)
}

func sameOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}
