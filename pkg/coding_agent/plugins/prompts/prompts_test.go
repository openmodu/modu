package prompts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExpand(t *testing.T) {
	cases := []struct {
		name    string
		content string
		input   string
		want    string
	}{
		{"input placeholder", "Do {{input}} now", "the thing", "Do the thing now"},
		{"args placeholder", "Run {{args}}", "x y", "Run x y"},
		{"no placeholder appends input", "Base prompt", "extra", "Base prompt\n\nextra"},
		{"no placeholder empty input", "Base prompt", "", "Base prompt"},
		{"trims input", "{{input}}", "  spaced  ", "spaced"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := &Template{Content: tc.content}
			if got := tmpl.Expand(tc.input); got != tc.want {
				t.Fatalf("Expand(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestDiscoverProjectOverridesUser(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(agentDir, "prompts", "dup.md"), "user body")
	writeFile(t, filepath.Join(cwd, ".coding_agent", "prompts", "dup.md"), "project body")

	m := NewManager(agentDir, cwd)
	tmpl, ok := m.Get("dup")
	if !ok {
		t.Fatal("dup template missing")
	}
	if tmpl.Source != "project" || tmpl.Content != "project body" {
		t.Fatalf("expected project to override user, got %#v", tmpl)
	}
}

func TestDiscoverFrontmatterNameDescriptionMetadata(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(agentDir, "prompts", "file.md"),
		"---\nname: renamed\ndescription: a desc\nmodel: opus\n---\nthe body")

	m := NewManager(agentDir, cwd)
	// File is file.md but frontmatter renames it to "renamed".
	tmpl, ok := m.Get("renamed")
	if !ok {
		t.Fatalf("expected template under frontmatter name 'renamed'")
	}
	if tmpl.Description != "a desc" {
		t.Fatalf("description = %q", tmpl.Description)
	}
	if tmpl.Content != "the body" {
		t.Fatalf("content should be frontmatter body, got %q", tmpl.Content)
	}
	if tmpl.Metadata["model"] != "opus" {
		t.Fatalf("expected metadata model=opus, got %#v", tmpl.Metadata)
	}
}

func TestExtraPathsOverrideBuiltins(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(agentDir, "prompts", "x.md"), "builtin body")

	extraDir := t.TempDir()
	writeFile(t, filepath.Join(extraDir, "x.md"), "extra body")

	m := NewManager(agentDir, cwd)
	m.SetExtraPaths([]resource.ResourceRef{{Path: extraDir, Source: "package"}})

	tmpl, ok := m.Get("x")
	if !ok {
		t.Fatal("x template missing")
	}
	if tmpl.Source != "package" || tmpl.Content != "extra body" {
		t.Fatalf("expected extra path to override builtin, got %#v", tmpl)
	}
}

func TestListSortedByName(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(agentDir, "prompts", "bbb.md"), "b")
	writeFile(t, filepath.Join(agentDir, "prompts", "aaa.md"), "a")
	writeFile(t, filepath.Join(agentDir, "prompts", "ccc.md"), "c")

	m := NewManager(agentDir, cwd)
	list := m.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 templates, got %d", len(list))
	}
	if list[0].Name != "aaa" || list[1].Name != "bbb" || list[2].Name != "ccc" {
		t.Fatalf("expected sorted names, got %s,%s,%s", list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestGetMissing(t *testing.T) {
	m := NewManager(t.TempDir(), t.TempDir())
	if _, ok := m.Get("nope"); ok {
		t.Fatal("expected missing template to return ok=false")
	}
}

func TestDiscoverRediscoversOnGet(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	m := NewManager(agentDir, cwd)
	if _, ok := m.Get("late"); ok {
		t.Fatal("template should not exist yet")
	}
	writeFile(t, filepath.Join(agentDir, "prompts", "late.md"), "added later")
	if _, ok := m.Get("late"); !ok {
		t.Fatal("Get should rediscover newly added template")
	}
}
