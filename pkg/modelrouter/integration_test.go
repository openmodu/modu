package modelrouter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Run with MODELROUTER_AGENT_INTEGRATION=1 when the Codex CLI is installed.
func TestCodexCLIThroughGateway(t *testing.T) {
	if os.Getenv("MODELROUTER_AGENT_INTEGRATION") != "1" {
		t.Skip("opt-in agent integration")
	}
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex CLI not installed")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/chat/completions" || !strings.Contains(string(body), `"model":"model"`) || !strings.Contains(string(body), `"Reply hello`) {
			t.Errorf("Codex request was not translated into Chat Completions")
		}
		_, _ = io.WriteString(w, `{"id":"chat-test","choices":[{"message":{"role":"assistant","content":"hello from fake model"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`)
	}))
	defer upstream.Close()
	cfg := Config{Providers: []Provider{{ID: "fake", BaseURL: upstream.URL + "/v1", Models: []string{"model"}}}}
	router, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router.Handler())
	defer server.Close()
	home := t.TempDir()
	manager := AgentManager{Home: home, GatewayURL: server.URL, Config: cfg}
	if err := manager.Use("codex", "fake/model"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "-m", "fake/model", "-")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"))
	cmd.Stdin = strings.NewReader("Reply hello. Do not use tools.")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "hello from fake model") {
		t.Fatal(fmt.Errorf("codex CLI failed: %v\n%s", err, out))
	}
}

// Run with MODELROUTER_AGENT_INTEGRATION=1 when Claude Code is installed.
func TestClaudeCLIThroughGateway(t *testing.T) {
	if os.Getenv("MODELROUTER_AGENT_INTEGRATION") != "1" {
		t.Skip("opt-in agent integration")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude CLI not installed")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		modelOK := strings.Contains(string(body), `"model":"model"`)
		promptOK := strings.Contains(string(body), `Reply hello`)
		if r.URL.Path != "/v1/chat/completions" || !modelOK || !promptOK {
			t.Errorf("Claude request translation: path=%q model=%v prompt=%v", r.URL.Path, modelOK, promptOK)
		}
		_, _ = io.WriteString(w, `{"id":"chat-test","choices":[{"message":{"role":"assistant","content":"hello from fake model"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`)
	}))
	defer upstream.Close()
	cfg := Config{Providers: []Provider{{ID: "fake", BaseURL: upstream.URL + "/v1", Models: []string{"model"}}}}
	router, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router.Handler())
	defer server.Close()
	home := t.TempDir()
	manager := AgentManager{Home: home, GatewayURL: server.URL, Config: cfg}
	if err := manager.Use("claude", "fake/model"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", "--output-format", "text", "--no-session-persistence", "--strict-mcp-config", "--tools", "")
	cmd.Dir = home
	cmd.Stdin = strings.NewReader("Reply hello. Do not use tools.")
	cmd.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "hello from fake model") {
		t.Fatal(fmt.Errorf("claude CLI failed: %v\n%s", err, out))
	}
}

func TestCodexToolRoundTrip(t *testing.T) {
	if os.Getenv("MODELROUTER_AGENT_INTEGRATION") != "1" {
		t.Skip("opt-in agent integration")
	}
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex CLI not installed")
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"id":"chat-tool","choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":5}}`)
			return
		}
		if !strings.Contains(string(body), `"role":"tool"`) || !strings.Contains(string(body), `"tool_call_id":"call_1"`) {
			t.Error("Codex tool result did not reach the upstream Chat request")
		}
		_, _ = io.WriteString(w, `{"id":"chat-final","choices":[{"message":{"role":"assistant","content":"tool round trip complete"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := Config{Providers: []Provider{{ID: "fake", BaseURL: upstream.URL + "/v1", Models: []string{"model"}}}}
	router, _ := New(cfg)
	server := httptest.NewServer(router.Handler())
	defer server.Close()
	home := t.TempDir()
	manager := AgentManager{Home: home, GatewayURL: server.URL, Config: cfg}
	if err := manager.Use("codex", "fake/model"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "-m", "fake/model", "-")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"))
	cmd.Stdin = strings.NewReader("Print the current directory, then reply done.")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "tool round trip complete") || calls.Load() < 2 {
		t.Fatalf("Codex tool round trip failed: calls=%d, err=%v\n%s", calls.Load(), err, out)
	}
}

func TestClaudeToolRoundTrip(t *testing.T) {
	if os.Getenv("MODELROUTER_AGENT_INTEGRATION") != "1" {
		t.Skip("opt-in agent integration")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude CLI not installed")
	}
	home := t.TempDir()
	file := filepath.Join(home, "note.txt")
	if err := os.WriteFile(file, []byte("test note"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			args := fmt.Sprintf(`{"file_path":%q}`, file)
			fmt.Fprintf(w, `{"id":"chat-tool","choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"Read","arguments":%q}}]},"finish_reason":"tool_calls"}]}`, args)
			return
		}
		if !strings.Contains(string(body), `"role":"tool"`) || !strings.Contains(string(body), "test note") {
			t.Error("Claude tool result did not reach the upstream Chat request")
		}
		_, _ = io.WriteString(w, `{"id":"chat-final","choices":[{"message":{"role":"assistant","content":"claude tool round trip complete"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := Config{Providers: []Provider{{ID: "fake", BaseURL: upstream.URL + "/v1", Models: []string{"model"}}}}
	router, _ := New(cfg)
	server := httptest.NewServer(router.Handler())
	defer server.Close()
	manager := AgentManager{Home: home, GatewayURL: server.URL, Config: cfg}
	if err := manager.Use("claude", "fake/model"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", "--output-format", "text", "--no-session-persistence", "--strict-mcp-config", "--tools", "Read")
	cmd.Dir = home
	cmd.Stdin = strings.NewReader("Read note.txt, then reply done.")
	cmd.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "claude tool round trip complete") || calls.Load() < 2 {
		t.Fatalf("Claude tool round trip failed: calls=%d, err=%v\n%s", calls.Load(), err, out)
	}
}
