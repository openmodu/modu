package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/config"
	"github.com/openmodu/modu/pkg/types"
)

var testServerBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "modu-mcp-test-server-")
	if err != nil {
		panic(err)
	}
	testServerBinary = filepath.Join(dir, "server")
	build := exec.Command("go", "build", "-o", testServerBinary, "./testdata/server")
	if output, err := build.CombinedOutput(); err != nil {
		_, _ = os.Stderr.Write(output)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestStartDiscoversFiltersAndCallsStdioTool(t *testing.T) {
	cwd := t.TempDir()
	manager, err := Start(context.Background(), map[string]config.MCPServerConfig{
		"echo": {
			Command:       testServerBinary,
			Env:           map[string]string{"MCP_TEST_VALUE": "configured"},
			Cwd:           cwd,
			Required:      true,
			EnabledTools:  []string{"echo", "fail"},
			DisabledTools: []string{"fail"},
		},
	}, StartOptions{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	tools := manager.Tools()
	if len(tools) != 1 || tools[0].Name() != "mcp__echo__echo" {
		t.Fatalf("discovered tools = %v, want only mcp__echo__echo", toolNames(tools))
	}
	if tools[0].Label() != "Echo" || !strings.Contains(tools[0].Description(), "Echo a value") {
		t.Fatalf("unexpected tool metadata: label=%q description=%q", tools[0].Label(), tools[0].Description())
	}
	schema, ok := tools[0].Parameters().(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Fatalf("unexpected tool schema: %#v", tools[0].Parameters())
	}
	result, err := tools[0].Execute(context.Background(), "call-1", map[string]any{"value": "hello"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || firstText(result) != "echo:hello" {
		t.Fatalf("unexpected tool result: %#v", result)
	}
	details, ok := result.Details.(ResultDetails)
	if !ok {
		t.Fatalf("result details type = %T, want ResultDetails", result.Details)
	}
	structured, ok := details.StructuredContent.(map[string]any)
	structuredCwd, _ := structured["cwd"].(string)
	if !ok || !sameDir(structuredCwd, cwd) || structured["env"] != "configured" || structured["value"] != "hello" {
		t.Fatalf("unexpected structured result: %#v", details.StructuredContent)
	}
}

func TestStartDiscoversAndCallsStreamableHTTPTool(t *testing.T) {
	t.Setenv("MCP_HTTP_TOKEN", "secret-token")
	t.Setenv("MCP_HTTP_DYNAMIC", "dynamic-value")

	server := mcp.NewServer(&mcp.Implementation{Name: "modu-http-test", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "echo",
		Title:       "HTTP Echo",
		Description: "Echo over Streamable HTTP",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
			"required": []string{"value"},
		},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		_ = json.Unmarshal(req.Params.Arguments, &args)
		value, _ := args["value"].(string)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "http:" + value}}}, nil
	})
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer secret-token" || req.Header.Get("X-Static") != "static-value" || req.Header.Get("X-Dynamic") != "dynamic-value" {
			http.Error(w, "missing configured headers", http.StatusUnauthorized)
			return
		}
		streamable.ServeHTTP(w, req)
	}))
	t.Cleanup(httpServer.Close)

	manager, err := Start(context.Background(), map[string]config.MCPServerConfig{
		"remote": {
			URL:               httpServer.URL,
			Required:          true,
			BearerTokenEnvVar: "MCP_HTTP_TOKEN",
			HTTPHeaders:       map[string]string{"X-Static": "static-value"},
			EnvHTTPHeaders:    map[string]string{"X-Dynamic": "MCP_HTTP_DYNAMIC"},
		},
	}, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	tools := manager.Tools()
	if len(tools) != 1 || tools[0].Name() != "mcp__remote__echo" || tools[0].Label() != "HTTP Echo" {
		t.Fatalf("streamable HTTP tools = %v", toolNames(tools))
	}
	result, err := tools[0].Execute(context.Background(), "http-call", map[string]any{"value": "hello"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || firstText(result) != "http:hello" {
		t.Fatalf("unexpected Streamable HTTP result: %#v", result)
	}
}

func TestStartRejectsAmbiguousOrMissingTransport(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.MCPServerConfig
	}{
		{name: "both", cfg: config.MCPServerConfig{Command: testServerBinary, URL: "https://example.com/mcp", Required: true}},
		{name: "neither", cfg: config.MCPServerConfig{Required: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Start(context.Background(), map[string]config.MCPServerConfig{"invalid": tc.cfg}, StartOptions{Cwd: t.TempDir()})
			if err == nil || !strings.Contains(err.Error(), "exactly one of command or url") {
				t.Fatalf("transport config error = %v", err)
			}
		})
	}
}

func TestStreamableHTTPHeadersDoNotLeakAcrossRedirectOrigins(t *testing.T) {
	var leakedAuthorization, leakedHeader string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		leakedAuthorization = req.Header.Get("Authorization")
		leakedHeader = req.Header.Get("X-Private")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client, err := configuredHTTPClient(nil, config.MCPServerConfig{
		HTTPHeaders: map[string]string{
			"Authorization": "Bearer secret",
			"X-Private":     "private-value",
		},
	}, source.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if leakedAuthorization != "" || leakedHeader != "" {
		t.Fatalf("configured MCP headers leaked across redirect origin: authorization=%q private=%q", leakedAuthorization, leakedHeader)
	}
}

func TestStartRejectsInvalidStreamableHTTPURL(t *testing.T) {
	_, err := Start(context.Background(), map[string]config.MCPServerConfig{
		"invalid": {URL: "ftp://example.com/mcp", Required: true},
	}, StartOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid Streamable HTTP url") {
		t.Fatalf("invalid Streamable HTTP error = %v", err)
	}
}

func TestStartMapsImageAndToolErrors(t *testing.T) {
	manager, err := Start(context.Background(), map[string]config.MCPServerConfig{
		"media": {Command: testServerBinary, Required: true},
	}, StartOptions{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	byName := map[string]types.Tool{}
	for _, tool := range manager.Tools() {
		byName[tool.Name()] = tool
	}
	imageResult, err := byName["mcp__media__image"].Execute(context.Background(), "call-image", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(imageResult.Content) != 1 {
		t.Fatalf("image content count = %d", len(imageResult.Content))
	}
	image, ok := imageResult.Content[0].(*types.ImageContent)
	if !ok || image.MimeType != "image/png" || image.Data != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03}) {
		t.Fatalf("unexpected image result: %#v", imageResult.Content[0])
	}
	failResult, err := byName["mcp__media__fail"].Execute(context.Background(), "call-fail", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !failResult.IsError || firstText(failResult) != "expected failure" {
		t.Fatalf("unexpected MCP tool error result: %#v", failResult)
	}
}

func TestToolCallHonorsConfiguredTimeout(t *testing.T) {
	manager, err := Start(context.Background(), map[string]config.MCPServerConfig{
		"slow": {Command: testServerBinary, Required: true, ToolTimeoutSec: 0.05, EnabledTools: []string{"slow"}},
	}, StartOptions{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	started := time.Now()
	_, err = manager.Tools()[0].Execute(context.Background(), "call-slow", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("slow tool error = %v, want deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("tool timeout took %s", elapsed)
	}
}

func TestRequiredAndOptionalStartupFailures(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-server")
	if _, err := Start(context.Background(), map[string]config.MCPServerConfig{
		"required": {Command: missing, Required: true},
	}, StartOptions{Cwd: t.TempDir()}); err == nil || !strings.Contains(err.Error(), `required`) {
		t.Fatalf("required startup error = %v", err)
	}

	manager, err := Start(context.Background(), map[string]config.MCPServerConfig{
		"optional": {Command: missing},
	}, StartOptions{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if len(manager.Tools()) != 0 || len(manager.Warnings()) != 1 || !strings.Contains(manager.Warnings()[0].Error(), `optional`) {
		t.Fatalf("optional failure: tools=%v warnings=%v", toolNames(manager.Tools()), manager.Warnings())
	}
}

func TestExportedToolNameUsesProviderSafeNamespace(t *testing.T) {
	if got, want := exportedToolName("issue tracker", "查找/issue"), "mcp__issue_tracker__issue"; got != want {
		t.Fatalf("exported tool name = %q, want %q", got, want)
	}
	longA := exportedToolName(strings.Repeat("server", 12), strings.Repeat("tool", 20)+"a")
	longB := exportedToolName(strings.Repeat("server", 12), strings.Repeat("tool", 20)+"b")
	if len(longA) != maxExportedToolNameLength || longA == longB {
		t.Fatalf("long exported names should be bounded and distinct: %q %q", longA, longB)
	}
}

func firstText(result types.ToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	text, _ := result.Content[0].(*types.TextContent)
	if text == nil {
		return ""
	}
	return text.Text
}

func toolNames(tools []types.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}

func sameDir(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
