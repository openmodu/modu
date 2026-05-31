package crontools

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
)

// callTool exercises one tool's Execute against a fresh cfgPath. Returns the
// flat text of the first TextContent block plus the raw result.
func callTool(t *testing.T, tool types.Tool, args map[string]any) (string, types.ToolResult) {
	t.Helper()
	res, err := tool.Execute(context.Background(), "call-1", args, nil)
	if err != nil {
		t.Fatalf("%s execute: %v", tool.Name(), err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s: empty content", tool.Name())
	}
	tc, ok := res.Content[0].(*types.TextContent)
	if !ok {
		t.Fatalf("%s: first block not TextContent: %T", tool.Name(), res.Content[0])
	}
	return tc.Text, res
}

func freshTools(t *testing.T) (cfgPath string, add, list, rm types.Tool) {
	t.Helper()
	cfgPath = filepath.Join(t.TempDir(), "config.yaml")
	tools := New(cfgPath)
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
	return cfgPath, tools[0], tools[1], tools[2]
}

func TestToolMetadata(t *testing.T) {
	_, add, list, rm := freshTools(t)
	if add.Name() != "cron_add" || list.Name() != "cron_list" || rm.Name() != "cron_remove" {
		t.Errorf("unexpected names: %s, %s, %s", add.Name(), list.Name(), rm.Name())
	}
	for _, tl := range []types.Tool{add, list, rm} {
		if tl.Label() == "" || tl.Description() == "" || tl.Parameters() == nil {
			t.Errorf("%s: empty metadata", tl.Name())
		}
	}
}

func TestAddHappyPath(t *testing.T) {
	cfgPath, add, _, _ := freshTools(t)
	text, _ := callTool(t, add, map[string]any{
		"id":         "daily",
		"cron":       "0 0 9 * * *",
		"prompt":     "summarize",
		"enabled":    true,
		"on_overlap": "queue",
		"channels":   []any{"ops", "mobile"},
	})
	if !strings.Contains(text, "added task \"daily\"") || !strings.Contains(text, "hot-reload") {
		t.Errorf("missing add/hot-reload phrasing: %q", text)
	}
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].ID != "daily" || cfg.Tasks[0].OnOverlap != config.OverlapQueue {
		t.Errorf("config not persisted correctly: %+v", cfg.Tasks)
	}
	if got := cfg.Tasks[0].NotificationChannels(); len(got) != 2 || got[0] != "ops" || got[1] != "mobile" {
		t.Errorf("channels not persisted correctly: %+v", got)
	}
}

func TestAddDefaults(t *testing.T) {
	cfgPath, add, _, _ := freshTools(t)
	// enabled & on_overlap omitted → defaults true / skip.
	callTool(t, add, map[string]any{
		"id":     "minimal",
		"cron":   "@every 1m",
		"prompt": "ping",
	})
	cfg, _ := config.Load(cfgPath)
	if !cfg.Tasks[0].Enabled {
		t.Error("enabled should default to true")
	}
	if cfg.Tasks[0].Policy() != config.OverlapSkip {
		t.Errorf("overlap should default to skip, got %q", cfg.Tasks[0].OnOverlap)
	}
}

func TestAddRejectsBadCron(t *testing.T) {
	cfgPath, add, _, _ := freshTools(t)
	text, _ := callTool(t, add, map[string]any{
		"id":     "x",
		"cron":   "not a cron",
		"prompt": "p",
	})
	if !strings.Contains(text, "invalid cron") {
		t.Errorf("expected invalid-cron error, got: %q", text)
	}
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != 0 {
		t.Error("nothing should have been persisted")
	}
}

func TestAddRejectsDuplicateID(t *testing.T) {
	cfgPath, add, _, _ := freshTools(t)
	callTool(t, add, map[string]any{"id": "x", "cron": "@every 1m", "prompt": "p"})
	text, _ := callTool(t, add, map[string]any{"id": "x", "cron": "@every 5m", "prompt": "q"})
	if !strings.Contains(text, "already exists") {
		t.Errorf("expected duplicate error, got: %q", text)
	}
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].Prompt != "p" {
		t.Errorf("second add should not have overwritten: %+v", cfg.Tasks)
	}
}

func TestAddRejectsMissingFields(t *testing.T) {
	_, add, _, _ := freshTools(t)
	text, _ := callTool(t, add, map[string]any{"id": "x"})
	if !strings.Contains(text, "required") {
		t.Errorf("expected missing-field error, got: %q", text)
	}
}

func TestAddRejectsBadOverlap(t *testing.T) {
	_, add, _, _ := freshTools(t)
	text, _ := callTool(t, add, map[string]any{
		"id":         "x",
		"cron":       "@every 1m",
		"prompt":     "p",
		"on_overlap": "explode",
	})
	if !strings.Contains(text, "skip|queue|kill") {
		t.Errorf("expected overlap-policy error, got: %q", text)
	}
}

func TestAddRejectsBadChannels(t *testing.T) {
	_, add, _, _ := freshTools(t)
	text, _ := callTool(t, add, map[string]any{
		"id":       "x",
		"cron":     "@every 1m",
		"prompt":   "p",
		"channels": []any{"ops", 12},
	})
	if !strings.Contains(text, "array of strings") {
		t.Errorf("expected channel error, got: %q", text)
	}
}

func TestListEmpty(t *testing.T) {
	_, _, list, _ := freshTools(t)
	text, _ := callTool(t, list, nil)
	if !strings.Contains(text, "no tasks") {
		t.Errorf("expected empty message, got: %q", text)
	}
}

func TestListShowsTasks(t *testing.T) {
	cfgPath, add, list, _ := freshTools(t)
	callTool(t, add, map[string]any{"id": "a", "cron": "@every 1m", "prompt": "alpha", "channels": []any{"ops"}})
	callTool(t, add, map[string]any{"id": "b", "cron": "@every 5m", "prompt": "beta", "enabled": false})
	text, res := callTool(t, list, nil)
	if !strings.Contains(text, "- a [@every 1m, on, skip, channels=ops]: alpha") {
		t.Errorf("missing task a row:\n%s", text)
	}
	if !strings.Contains(text, "- b [@every 5m, off, skip]: beta") {
		t.Errorf("missing task b row:\n%s", text)
	}
	details, _ := res.Details.(map[string]any)
	if details == nil || details["count"] != 2 {
		t.Errorf("expected count=2 in details, got %+v", res.Details)
	}
	_ = cfgPath
}

func TestListShowsConfiguredChannels(t *testing.T) {
	cfgPath, _, list, _ := freshTools(t)
	if err := config.Save(cfgPath, &config.Config{
		Channels: map[string]config.Channel{
			"tg":  {Type: "telegram"},
			"ops": {Type: "webhook"},
		},
		Tasks: []config.Task{
			{ID: "a", Cron: "@every 1m", Prompt: "alpha", Enabled: true, Channels: []string{"tg"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	text, _ := callTool(t, list, nil)
	if !strings.Contains(text, "configured channels: ops(webhook), tg(telegram)") {
		t.Errorf("missing configured channels:\n%s", text)
	}
}

func TestRemoveHappyPath(t *testing.T) {
	cfgPath, add, _, rm := freshTools(t)
	callTool(t, add, map[string]any{"id": "x", "cron": "@every 1m", "prompt": "p"})
	text, _ := callTool(t, rm, map[string]any{"id": "x"})
	if !strings.Contains(text, "removed task \"x\"") {
		t.Errorf("missing remove message: %q", text)
	}
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != 0 {
		t.Errorf("task should be gone, got: %+v", cfg.Tasks)
	}
}

func TestRemoveUnknownIDErrors(t *testing.T) {
	_, _, _, rm := freshTools(t)
	text, _ := callTool(t, rm, map[string]any{"id": "ghost"})
	if !strings.Contains(text, "not found") {
		t.Errorf("expected not-found, got: %q", text)
	}
}

func TestRemoveMissingID(t *testing.T) {
	_, _, _, rm := freshTools(t)
	text, _ := callTool(t, rm, map[string]any{})
	if !strings.Contains(text, "id is required") {
		t.Errorf("expected id-required, got: %q", text)
	}
}

// TestConcurrentAddsSerialize fires 50 parallel cron_add calls with distinct
// ids and verifies all 50 land in the file. Without the package-level mutex
// they'd interleave load-modify-save and most would be lost.
func TestConcurrentAddsSerialize(t *testing.T) {
	cfgPath, add, _, _ := freshTools(t)
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			id := "task-" + string(rune('a'+(i%26))) + "-" + intToStr(i)
			callTool(t, add, map[string]any{
				"id":     id,
				"cron":   "@every 1m",
				"prompt": "p",
			})
		}(i)
	}
	wg.Wait()
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != n {
		t.Errorf("expected %d tasks after concurrent adds, got %d", n, len(cfg.Tasks))
	}
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	if i < 0 {
		i = -i
	}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
