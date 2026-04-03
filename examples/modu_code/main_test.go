package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/coding_agent/modes/rpc"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"
)

func TestHandleSlashHarnessInspectionCommands(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "repo")
	agentDir := filepath.Join(root, ".coding_agent")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configContent := `{
  "harness": {
    "logFiles": {
      "toolUse": "logs/tool-use.jsonl",
      "compact": "logs/compact.jsonl",
      "subagent": "logs/subagent.jsonl"
    },
    "artifactFiles": {
      "toolUse": "artifacts/tool-use-latest.json",
      "compact": "artifacts/compact-latest.json",
      "subagent": "artifacts/subagent-latest.json"
    },
    "bridgeDirs": {
      "toolUse": "bridge/tool-use",
      "compact": "bridge/compact",
      "subagent": "bridge/subagent"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(agentDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(agentDir, "bridge", "tool-use"),
		filepath.Join(agentDir, "bridge", "compact"),
		filepath.Join(agentDir, "bridge", "subagent"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(agentDir, "logs", "tool-use.jsonl"), []byte("{\"event\":\"post_tool_use\",\"tool\":\"echo\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "artifacts", "tool-use-latest.json"), []byte("{\"event\": \"post_tool_use\", \"tool\": \"echo\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "bridge", "tool-use", "1-post_tool_use.json"), []byte("{\"event\":\"post_tool_use\",\"tool\":\"echo\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "bridge", "compact", "2-post_compact.json"), []byte("{\"event\":\"post_compact\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "bridge", "subagent", "3-subagent_stop.json"), []byte("{\"event\":\"subagent_stop\",\"name\":\"reviewer\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeProjectKey := strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "_")
	if err := os.MkdirAll(filepath.Join(agentDir, "runtime", runtimeProjectKey, "actions", "tool_use"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "runtime", runtimeProjectKey, "actions", "tool_use", "latest.json"), []byte("{\"status\":\"ok\",\"stdout\":\"done\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "runtime", runtimeProjectKey, "index.json"), []byte("{\"last_events\":{\"session\":{\"event\":\"session_start\"},\"permission\":{\"event\":\"permission_denied\"}}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     testExampleModel(),
		GetAPIKey: func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	renderer := tui.NewRenderer(&out)
	renderer.SetNoColor(true)

	for _, line := range []string{"/runtime", "/git", "/dashboard", "/state", "/config", "/config-template", "/logs", "/artifacts", "/bridge", "/actions"} {
		handled, shouldExit := handleSlash(context.Background(), line, session, renderer, testExampleModel(), nil)
		if !handled || shouldExit {
			t.Fatalf("expected %s to be handled without exit", line)
		}
	}

	got := out.String()
	for _, want := range []string{
		"Runtime Paths",
		"git: not a repository",
		"Runtime Dashboard",
		"latest events:",
		"Runtime State",
		"Effective Config",
		"Default Config Template",
		"harness log files",
		"harness artifact files",
		"harness bridge directories",
		"Harness Action Status Files",
		filepath.Join(agentDir, "logs", "tool-use.jsonl"),
		filepath.Join(agentDir, "artifacts", "tool-use-latest.json"),
		filepath.Join(agentDir, "bridge", "tool-use"),
		filepath.Join(agentDir, "runtime", runtimeProjectKey, "actions", "tool_use", "latest.json"),
		"preview: {\"event\":\"post_tool_use\",\"tool\":\"echo\"}",
		"preview: {\"status\":\"ok\",\"stdout\":\"done\"}",
		"session: {\"category\":\"session\",\"event\":\"session_start\",\"source\":\"startup\"}",
		"- 1-post_tool_use.json",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

func testExampleModel() *types.Model {
	return &types.Model{
		ID:         "test-model",
		Name:       "Test Model",
		ProviderID: "test",
	}
}

func TestPrintModeSmoke(t *testing.T) {
	session := newExampleTestSession(t)

	var textOut bytes.Buffer
	if err := modes.RunPrint(context.Background(), modes.PrintOptions{
		Mode:     modes.PrintModeText,
		Messages: []string{"hello"},
		Output:   &textOut,
		Session:  session,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOut.String(), "assistant: hello") {
		t.Fatalf("expected text print output, got %q", textOut.String())
	}

	session = newExampleTestSession(t)
	var jsonOut bytes.Buffer
	if err := modes.RunPrint(context.Background(), modes.PrintOptions{
		Mode:     modes.PrintModeJSON,
		Messages: []string{"hello"},
		Output:   &jsonOut,
		Session:  session,
	}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(jsonOut.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected json print events, got %q", jsonOut.String())
	}
	if !strings.Contains(lines[0], `"type":"session_start"`) {
		t.Fatalf("expected session_start, got %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], `"type":"session_end"`) {
		t.Fatalf("expected session_end, got %q", lines[len(lines)-1])
	}
}

func TestRPCModeSmoke(t *testing.T) {
	session := newExampleTestSession(t)
	mode := rpc.NewRpcMode(session)

	input := strings.NewReader(strings.Join([]string{
		`{"id":"1","type":"get_state"}`,
		`{"id":"2","type":"get_commands"}`,
	}, "\n"))
	var output bytes.Buffer
	mode.SetIO(input, &output)

	if err := mode.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected rpc responses, got %q", output.String())
	}

	var resp1 rpc.RpcResponse
	if err := json.Unmarshal([]byte(lines[0]), &resp1); err != nil {
		t.Fatal(err)
	}
	if !resp1.Success || resp1.Command != rpc.RpcCmdGetState {
		t.Fatalf("unexpected first response: %#v", resp1)
	}

	var resp2 rpc.RpcResponse
	if err := json.Unmarshal([]byte(lines[1]), &resp2); err != nil {
		t.Fatal(err)
	}
	if !resp2.Success || resp2.Command != rpc.RpcCmdGetCommands {
		t.Fatalf("unexpected second response: %#v", resp2)
	}
}

func newExampleTestSession(t *testing.T) *coding_agent.CodingSession {
	t.Helper()

	root := t.TempDir()
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:      root,
		AgentDir: filepath.Join(root, ".coding_agent"),
		Model:    testExampleModel(),
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
		StreamFn: func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				last := llmCtx.Messages[len(llmCtx.Messages)-1]
				userText := ""
				if msg, ok := last.(types.UserMessage); ok {
					userText, _ = msg.Content.(string)
				}
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "assistant: " + userText}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
				stream.Close()
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}
