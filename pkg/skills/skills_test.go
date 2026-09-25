package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
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

func TestBuiltinSkillCreatorDiscoveredWithOfficialResources(t *testing.T) {
	m, agentDir, _ := newTestManager(t)

	skill, ok := m.Get("skill-creator")
	if !ok {
		t.Fatal("built-in skill-creator was not discovered")
	}
	if skill.Source != "builtin" {
		t.Fatalf("skill-creator source = %q, want builtin", skill.Source)
	}
	if !strings.Contains(skill.Description, "Create new skills") {
		t.Fatalf("unexpected skill-creator description: %q", skill.Description)
	}
	if !strings.Contains(skill.Content, "python -m scripts.run_loop") {
		t.Fatal("skill-creator content does not contain the official Claude Code workflow")
	}
	if !strings.HasPrefix(skill.FilePath, filepath.Join(agentDir, "builtin-skills")+string(filepath.Separator)) {
		t.Fatalf("skill-creator path = %q, want materialized under agent dir", skill.FilePath)
	}

	var got []string
	err := filepath.WalkDir(skill.BaseDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skill.BaseDir, path)
		if err != nil {
			return err
		}
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{
		"LICENSE.txt",
		"SKILL.md",
		"agents/analyzer.md",
		"agents/comparator.md",
		"agents/grader.md",
		"assets/eval_review.html",
		"eval-viewer/generate_review.py",
		"eval-viewer/viewer.html",
		"references/schemas.md",
		"scripts/__init__.py",
		"scripts/aggregate_benchmark.py",
		"scripts/generate_report.py",
		"scripts/improve_description.py",
		"scripts/package_skill.py",
		"scripts/quick_validate.py",
		"scripts/run_eval.py",
		"scripts/run_loop.py",
		"scripts/utils.py",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("materialized skill-creator files:\n got: %v\nwant: %v", got, want)
	}

	digest := sha256.New()
	for _, rel := range got {
		data, err := os.ReadFile(filepath.Join(skill.BaseDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		digest.Write([]byte(rel))
		digest.Write([]byte{0})
		digest.Write(data)
		digest.Write([]byte{0})
	}
	if gotDigest := hex.EncodeToString(digest.Sum(nil)); gotDigest != "887b2027e7eeb09e4e6bcc48f2a0f7f76adc8033462139260a77c6d39799b779" {
		t.Fatalf("official skill-creator digest = %s", gotDigest)
	}
}

func TestCustomSkillCreatorOverridesBuiltin(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		install    func(t *testing.T, agentDir, cwd, extraDir string)
		extraPaths func(extraDir string) []PathRef
	}{
		{
			name:   "project",
			source: "project",
			install: func(t *testing.T, _, cwd, _ string) {
				writeFile(t, filepath.Join(cwd, ".coding_agent", "skills", "skill-creator", "SKILL.md"),
					"---\ndescription: project override\n---\nproject")
			},
		},
		{
			name:   "user",
			source: "user",
			install: func(t *testing.T, agentDir, _, _ string) {
				writeFile(t, filepath.Join(agentDir, "skills", "skill-creator", "SKILL.md"),
					"---\ndescription: user override\n---\nuser")
			},
		},
		{
			name:   "package",
			source: "package",
			install: func(t *testing.T, _, _, extraDir string) {
				writeFile(t, filepath.Join(extraDir, "skill-creator", "SKILL.md"),
					"---\ndescription: package override\n---\npackage")
			},
			extraPaths: func(extraDir string) []PathRef {
				return []PathRef{{Path: extraDir, Source: "package"}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, agentDir, cwd := newTestManager(t)
			extraDir := t.TempDir()
			tt.install(t, agentDir, cwd, extraDir)
			if tt.extraPaths != nil {
				m.SetExtraPaths(tt.extraPaths(extraDir))
			}

			skill, ok := m.Get("skill-creator")
			if !ok {
				t.Fatal("skill-creator was not discovered")
			}
			if skill.Source != tt.source {
				t.Fatalf("skill-creator source = %q, want %q", skill.Source, tt.source)
			}
			if skill.Description != tt.source+" override" {
				t.Fatalf("skill-creator description = %q", skill.Description)
			}
		})
	}
}

func TestBuiltinSkillCreatorMaterializationIsConcurrentSafe(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			skill, ok := NewManager(agentDir, cwd).Get("skill-creator")
			if !ok {
				t.Error("built-in skill-creator missing after concurrent materialization")
				return
			}
			if skill.Source != "builtin" {
				t.Errorf("skill-creator source = %q, want builtin", skill.Source)
			}
		}()
	}
	wg.Wait()

	entries, err := os.ReadDir(filepath.Join(agentDir, "builtin-skills"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != builtinSkillCreatorCommit {
		t.Fatalf("unexpected built-in materializations: %#v", entries)
	}
}

func TestListAndPromptDoNotLoadSkillContent(t *testing.T) {
	m, agentDir, _ := newTestManager(t)

	writeFile(t, filepath.Join(agentDir, "skills", "lazy.md"),
		"---\ndescription: lazy skill\n---\nsecret body should stay out of indexes")

	list := m.List()
	var lazy *Skill
	for _, skill := range list {
		if skill.Name == "lazy" {
			lazy = skill
			break
		}
	}
	if lazy == nil {
		t.Fatalf("lazy skill missing from list: %#v", list)
	}
	if lazy.Content != "" {
		t.Fatalf("List should expose metadata only, got content %q", lazy.Content)
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

func TestFormatForPromptSortsSkillsByName(t *testing.T) {
	m, agentDir, _ := newTestManager(t)
	writeFile(t, filepath.Join(agentDir, "skills", "zeta.md"),
		"---\ndescription: last\n---\nbody")
	writeFile(t, filepath.Join(agentDir, "skills", "alpha.md"),
		"---\ndescription: first\n---\nbody")

	first := m.FormatForPrompt()
	second := m.FormatForPrompt()
	if first != second {
		t.Fatalf("skill prompt changed without filesystem changes:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	alpha := strings.Index(first, "<name>alpha</name>")
	zeta := strings.Index(first, "<name>zeta</name>")
	if alpha < 0 || zeta < 0 || alpha > zeta {
		t.Fatalf("skills are not sorted by name: %s", first)
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
