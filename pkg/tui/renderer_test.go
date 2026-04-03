package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestRendererPrintBannerAndSections(t *testing.T) {
	var out bytes.Buffer
	r := NewRenderer(&out)
	r.SetNoColor(true)

	r.PrintBanner("qwen/qwen3.5", "/tmp/project", "bot_name")
	r.PrintSection("Runtime Paths", []string{
		"root: /tmp/project/.coding_agent",
		"plans: /tmp/project/.coding_agent/plans",
	})

	got := out.String()
	for _, want := range []string{
		"Session",
		"modu_code",
		"model    qwen/qwen3.5",
		"cwd      /tmp/project",
		"telegram @bot_name",
		"/help commands",
		"Runtime Paths",
		"root: /tmp/project/.coding_agent",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestRendererPrintUserBlock(t *testing.T) {
	var out bytes.Buffer
	r := NewRenderer(&out)
	r.SetNoColor(true)

	r.PrintUser("please inspect the repo status carefully before committing")

	got := out.String()
	for _, want := range []string{
		"────────────────",
		"❯ please inspect the repo status carefully before committing",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}
