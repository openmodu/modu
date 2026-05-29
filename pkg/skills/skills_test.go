package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func newTestManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	agentDir := t.TempDir()
	cwd := t.TempDir()
	return NewManager(agentDir, cwd), agentDir, cwd
}

func TestDiscoverFlatAndSkillMd(t *testing.T) {
	m, agentDir, _ := newTestManager(t)

	writeFile(t, filepath.Join(agentDir, "skills", "flat.md"),
		"---\ndescription: flat skill\n---\nflat body")
	writeFile(t, filepath.Join(agentDir, "skills", "nested", "SKILL.md"),
		"---\ndescription: nested skill\n---\nnested body")

	if err := m.Discover(); err != nil {
		t.Fatal(err)
	}

	flat, ok := m.Get("flat")
	if !ok || flat.Description != "flat skill" || !strings.Contains(flat.Content, "flat body") {
		t.Fatalf("flat skill not discovered correctly: %#v", flat)
	}
	nested, ok := m.Get("nested")
	if !ok || nested.Description != "nested skill" {
		t.Fatalf("nested SKILL.md not discovered: %#v", nested)
	}
}

func TestListAndPromptDoNotLoadSkillContent(t *testing.T) {
	m, agentDir, _ := newTestManager(t)

	writeFile(t, filepath.Join(agentDir, "skills", "lazy.md"),
		"---\ndescription: lazy skill\n---\nsecret body should stay out of indexes")

	list := m.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(list))
	}
	if list[0].Content != "" {
		t.Fatalf("List should expose metadata only, got content %q", list[0].Content)
	}

	prompt := m.FormatForPrompt()
	if strings.Contains(prompt, "secret body") {
		t.Fatalf("FormatForPrompt should not include skill body: %s", prompt)
	}
	if !strings.Contains(prompt, "<name>lazy</name>") {
		t.Fatalf("FormatForPrompt should include skill metadata: %s", prompt)
	}
}

func TestGetLoadsLatestSkillContent(t *testing.T) {
	m, agentDir, _ := newTestManager(t)
	path := filepath.Join(agentDir, "skills", "dynamic.md")
	writeFile(t, path, "---\ndescription: dynamic skill\n---\nfirst body")

	first, ok := m.Get("dynamic")
	if !ok || !strings.Contains(first.Content, "first body") {
		t.Fatalf("expected first body, got ok=%v skill=%#v", ok, first)
	}

	writeFile(t, path, "---\ndescription: dynamic skill\n---\nsecond body")
	second, ok := m.Get("dynamic")
	if !ok || !strings.Contains(second.Content, "second body") {
		t.Fatalf("expected second body, got ok=%v skill=%#v", ok, second)
	}
}

func TestProjectOverridesGlobal(t *testing.T) {
	m, agentDir, cwd := newTestManager(t)

	writeFile(t, filepath.Join(agentDir, "skills", "dup.md"),
		"---\ndescription: global one\n---\nglobal")
	writeFile(t, filepath.Join(cwd, ".coding_agent", "skills", "dup.md"),
		"---\ndescription: project one\n---\nproject")

	if err := m.Discover(); err != nil {
		t.Fatal(err)
	}
	dup, ok := m.Get("dup")
	if !ok {
		t.Fatal("dup skill missing")
	}
	if dup.Source != "project" || dup.Description != "project one" {
		t.Fatalf("expected project to override global, got %#v", dup)
	}
}

func TestSkillMdRequiresDescription(t *testing.T) {
	m, agentDir, _ := newTestManager(t)
	writeFile(t, filepath.Join(agentDir, "skills", "nodesc", "SKILL.md"), "no frontmatter body")

	if err := m.Discover(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get("nodesc"); ok {
		t.Fatal("SKILL.md without description should be skipped")
	}
}

func TestInvalidNameRejected(t *testing.T) {
	m, agentDir, _ := newTestManager(t)
	writeFile(t, filepath.Join(agentDir, "skills", "bad.md"),
		"---\nname: has spaces\ndescription: d\n---\nbody")

	if err := m.Discover(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get("has spaces"); ok {
		t.Fatal("skill with invalid name should be skipped")
	}
}

func TestFormatForPromptExcludesDisabled(t *testing.T) {
	m, agentDir, _ := newTestManager(t)
	writeFile(t, filepath.Join(agentDir, "skills", "visible.md"),
		"---\ndescription: visible\n---\nbody")
	writeFile(t, filepath.Join(agentDir, "skills", "hidden.md"),
		"---\ndescription: hidden\ndisable-model-invocation: true\n---\nbody")

	out := m.FormatForPrompt()
	if !strings.Contains(out, "<name>visible</name>") {
		t.Fatalf("expected visible skill in prompt: %s", out)
	}
	if strings.Contains(out, "hidden") {
		t.Fatalf("disable-model-invocation skill should be excluded: %s", out)
	}
}

func TestExtraPathsDiscovery(t *testing.T) {
	m, _, _ := newTestManager(t)
	extraDir := t.TempDir()
	writeFile(t, filepath.Join(extraDir, "extra.md"),
		"---\ndescription: extra skill\n---\nbody")
	m.SetExtraPaths([]PathRef{{Path: extraDir, Source: "package"}})

	if err := m.Discover(); err != nil {
		t.Fatal(err)
	}
	if s, ok := m.Get("extra"); !ok || s.Source != "package" {
		t.Fatalf("extra path skill not discovered: %#v", s)
	}
}
