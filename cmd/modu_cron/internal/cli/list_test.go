package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
)

func TestListRendersAlignedTable(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := config.SaveRuntime(cfgPath, &config.Config{TasksFile: "tasks.yaml"}); err != nil {
		t.Fatalf("SaveRuntime: %v", err)
	}
	if err := config.SaveTasks(filepath.Join(dir, "tasks.yaml"), []config.Task{
		{
			ID:      "heartbeat",
			Cron:    "*/10 * * * * *",
			Prompt:  "say hello",
			Enabled: true,
		},
		{
			ID:      "weekly-report",
			Cron:    "0 0 9 * * MON",
			Prompt:  "生成一份很长很长的周报摘要，并且把多余内容截断，避免命令行表格被顶歪",
			Enabled: false,
		},
	}); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}

	var out bytes.Buffer
	if err := List(cfgPath, &out); err != nil {
		t.Fatalf("List: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"+",
		"| ID",
		"| CRON",
		"| ENABLED",
		"| PROMPT",
		"| heartbeat",
		"| weekly-report",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("list output missing %q\n--- output ---\n%s", want, got)
		}
	}
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "|") {
			t.Fatalf("expected table line, got %q\n--- output ---\n%s", line, got)
		}
	}
}

func TestWriteTableTruncatesWideText(t *testing.T) {
	var out bytes.Buffer
	writeTable(&out, []tableColumn{
		{Header: "NAME", Max: 6},
		{Header: "TEXT", Max: 8},
	}, [][]string{{"alpha", "中文内容很长"}})

	got := out.String()
	if !strings.Contains(got, "中文内…") {
		t.Fatalf("expected wide text to be truncated without breaking width\n--- output ---\n%s", got)
	}
}
