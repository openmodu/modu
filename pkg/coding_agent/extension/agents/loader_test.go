package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProfile(t *testing.T, dir, filename, body string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func validProfile(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\nbody\n"
}

func TestLoadDirMultiSortsByFilename(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "zeta.md", validProfile("zeta", "z"))
	writeProfile(t, dir, "alpha.md", validProfile("alpha", "a"))
	writeProfile(t, dir, "mu.md", validProfile("mu", "m"))

	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	want := []string{"alpha", "mu", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("got %d profiles, want %d", len(got), len(want))
	}
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name, n)
		}
	}
}

func TestLoadDirFillsSourcePath(t *testing.T) {
	dir := t.TempDir()
	path := writeProfile(t, dir, "scout.md", validProfile("scout", "recon"))

	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(got) != 1 || got[0].SourcePath != path {
		t.Errorf("SourcePath=%q, want %q", got[0].SourcePath, path)
	}
}

func TestLoadDirSkipsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "ok.md", validProfile("ok", "yes"))
	writeProfile(t, dir, "README.txt", "not a profile")
	writeProfile(t, dir, ".hidden", "also not")

	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(got) != 1 || got[0].Name != "ok" {
		t.Errorf("expected only ok.md picked up, got: %v", got)
	}
}

func TestLoadDirSkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProfile(t, dir, "nested/inner.md", validProfile("inner", "x"))
	writeProfile(t, dir, "top.md", validProfile("top", "y"))

	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(got) != 1 || got[0].Name != "top" {
		t.Errorf("subdir entry should be ignored, got: %v", got)
	}
}

func TestLoadDirEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty dir should yield no profiles, got: %v", got)
	}
}

func TestLoadDirMissingPath(t *testing.T) {
	_, err := LoadDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatalf("expected error for missing path")
	}
}

func TestLoadDirParseErrorIncludesPath(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "good.md", validProfile("good", "x"))
	bad := writeProfile(t, dir, "bad.md", `---
name: missing description
---
body
`)

	_, err := LoadDir(dir)
	if err == nil {
		t.Fatalf("expected parse error from bad.md")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("error should mention bad file path %q, got: %v", bad, err)
	}
}

func TestLoadDirDuplicateName(t *testing.T) {
	dir := t.TempDir()
	first := writeProfile(t, dir, "a.md", validProfile("reviewer", "first"))
	second := writeProfile(t, dir, "b.md", validProfile("reviewer", "second"))

	_, err := LoadDir(dir)
	if err == nil {
		t.Fatalf("expected duplicate-name error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate', got: %v", err)
	}
	if !strings.Contains(err.Error(), first) || !strings.Contains(err.Error(), second) {
		t.Errorf("error should reference both paths, got: %v", err)
	}
}
