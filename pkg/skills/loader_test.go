package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper to create a temp skill dir with a SKILL.md.
func createSkillDir(t *testing.T, base, name, content string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return dir
}

// ---------- SkillInfo.validate ----------

func TestValidate_EmptyName(t *testing.T) {
	info := SkillInfo{Name: "", Description: "some desc"}
	if err := info.validate(); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestValidate_NameTooLong(t *testing.T) {
	long := ""
	for i := 0; i < MaxNameLength+1; i++ {
		long += "a"
	}
	info := SkillInfo{Name: long, Description: "desc"}
	if err := info.validate(); err == nil {
		t.Errorf("expected error for name too long")
	}
}

func TestValidate_InvalidNamePattern(t *testing.T) {
	info := SkillInfo{Name: "my skill!", Description: "desc"}
	if err := info.validate(); err == nil {
		t.Error("expected error for invalid name pattern")
	}
}

func TestValidate_EmptyDescription(t *testing.T) {
	info := SkillInfo{Name: "my-skill", Description: ""}
	if err := info.validate(); err == nil {
		t.Error("expected error for empty description")
	}
}

func TestValidate_DescriptionTooLong(t *testing.T) {
	long := ""
	for i := 0; i < MaxDescriptionLength+1; i++ {
		long += "a"
	}
	info := SkillInfo{Name: "my-skill", Description: long}
	if err := info.validate(); err == nil {
		t.Error("expected error for description too long")
	}
}

func TestValidate_Valid(t *testing.T) {
	info := SkillInfo{Name: "my-skill", Description: "A valid description."}
	if err := info.validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------- extractFrontmatter / stripFrontmatter ----------

func TestExtractFrontmatter_Unix(t *testing.T) {
	sl := &SkillsLoader{}
	content := "---\nname: my-skill\ndescription: A test\n---\nBody text."
	fm := sl.extractFrontmatter(content)
	if fm != "name: my-skill\ndescription: A test" {
		t.Errorf("unexpected frontmatter: %q", fm)
	}
}

func TestExtractFrontmatter_Windows(t *testing.T) {
	sl := &SkillsLoader{}
	content := "---\r\nname: my-skill\r\ndescription: A test\r\n---\r\nBody."
	fm := sl.extractFrontmatter(content)
	if fm == "" {
		t.Error("expected non-empty frontmatter for Windows line endings")
	}
}

func TestExtractFrontmatter_ClassicMac(t *testing.T) {
	sl := &SkillsLoader{}
	content := "---\rname: my-skill\rdescription: A test\r---\rBody."
	fm := sl.extractFrontmatter(content)
	if fm == "" {
		t.Error("expected non-empty frontmatter for classic Mac line endings")
	}
}

func TestStripFrontmatter_RemovesHeader(t *testing.T) {
	sl := &SkillsLoader{}
	content := "---\nname: my-skill\n---\nBody text."
	stripped := sl.stripFrontmatter(content)
	if stripped != "Body text." {
		t.Errorf("unexpected stripped body: %q", stripped)
	}
}

func TestStripFrontmatter_NoFrontmatter(t *testing.T) {
	sl := &SkillsLoader{}
	content := "Just a body."
	stripped := sl.stripFrontmatter(content)
	if stripped != content {
		t.Errorf("expected unchanged content, got: %q", stripped)
	}
}

// ---------- ListSkills ----------

func TestListSkills_ReturnsValidSkill(t *testing.T) {
	tmp := t.TempDir()
	// globalSkills directory contains the skill folders directly.
	skillsDir := filepath.Join(tmp, "global")
	createSkillDir(t, skillsDir, "my-skill", "---\nname: my-skill\ndescription: Does stuff\n---\nContent")

	sl := NewSkillsLoader("", skillsDir, "")
	list := sl.ListSkills()
	if len(list) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(list))
	}
	if list[0].Name != "my-skill" {
		t.Errorf("expected name 'my-skill', got %q", list[0].Name)
	}
}

func TestListSkills_SkipsInvalid(t *testing.T) {
	tmp := t.TempDir()
	skillsDir := filepath.Join(tmp, "global")
	// Invalid: no SKILL.md
	if err := os.MkdirAll(filepath.Join(skillsDir, "no-skill-file"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Invalid metadata (empty name)
	createSkillDir(t, skillsDir, "bad-skill", "---\nname: \ndescription: desc\n---")

	sl := NewSkillsLoader("", skillsDir, "")
	list := sl.ListSkills()
	if len(list) != 0 {
		t.Errorf("expected 0 valid skills, got %d", len(list))
	}
}

// ---------- Priority order ----------

func TestListSkills_Priority_WorkspaceOverGlobal(t *testing.T) {
	globalDir := t.TempDir()
	workspaceRoot := t.TempDir()
	workspaceSkillsDir := filepath.Join(workspaceRoot, "skills")

	createSkillDir(t, globalDir, "shared-skill", "---\nname: shared-skill\ndescription: From global\n---")
	createSkillDir(t, workspaceSkillsDir, "shared-skill", "---\nname: shared-skill\ndescription: From workspace\n---")

	sl := NewSkillsLoader(workspaceRoot, globalDir, "")
	list := sl.ListSkills()

	// Should only appear once, from workspace.
	count := 0
	for _, s := range list {
		if s.Name == "shared-skill" {
			count++
			if s.Source != "workspace" {
				t.Errorf("expected source 'workspace', got %q", s.Source)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected skill to appear exactly once, count=%d", count)
	}
}

// ---------- LoadSkill ----------

func TestLoadSkill_ReturnsBodyWithoutFrontmatter(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global")
	createSkillDir(t, globalDir, "my-skill", "---\nname: my-skill\ndescription: desc\n---\nActual body.")

	sl := NewSkillsLoader("", globalDir, "")
	body, ok := sl.LoadSkill("my-skill")
	if !ok {
		t.Fatal("expected skill to be found")
	}
	if body != "Actual body." {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestLoadSkill_MissingSkill(t *testing.T) {
	sl := NewSkillsLoader("", t.TempDir(), "")
	_, ok := sl.LoadSkill("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent skill")
	}
}

// ---------- BuildSkillsSummary ----------

func TestBuildSkillsSummary_FormatsXML(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global")
	createSkillDir(t, globalDir, "my-skill", "---\nname: my-skill\ndescription: A cool skill\n---\nBody.")

	sl := NewSkillsLoader("", globalDir, "")
	summary := sl.BuildSkillsSummary()
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if !strings.Contains(summary, "<available_skills>") {
		t.Errorf("expected summary to contain <available_skills>, got: %q", summary[:min(80, len(summary))])
	}
	if !strings.Contains(summary, "The following skills provide specialized instructions") {
		t.Error("expected summary to contain intro text")
	}
	if !strings.Contains(summary, "my-skill") {
		t.Error("expected summary to contain skill name")
	}
	if !strings.Contains(summary, "A cool skill") {
		t.Error("expected summary to contain description")
	}
	if !strings.Contains(summary, "</available_skills>") {
		t.Error("expected summary to contain closing </available_skills>")
	}
}

func TestBuildSkillsSummary_EmptyWhenNoSkills(t *testing.T) {
	sl := NewSkillsLoader("", t.TempDir(), "")
	if sl.BuildSkillsSummary() != "" {
		t.Error("expected empty summary when no skills")
	}
}

// ---------- DisableModelInvocation ----------

func TestDisableModelInvocation_Parsed(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global")
	createSkillDir(t, globalDir, "hidden-skill",
		"---\nname: hidden-skill\ndescription: A hidden skill\ndisable-model-invocation: true\n---\nBody.")

	sl := NewSkillsLoader("", globalDir, "")
	list := sl.ListSkills()
	if len(list) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(list))
	}
	if !list[0].DisableModelInvocation {
		t.Error("expected DisableModelInvocation to be true")
	}
}

func TestBuildSkillsSummary_ExcludesDisabledSkills(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global")
	createSkillDir(t, globalDir, "visible-skill",
		"---\nname: visible-skill\ndescription: A visible skill\n---\nBody.")
	createSkillDir(t, globalDir, "hidden-skill",
		"---\nname: hidden-skill\ndescription: A hidden skill\ndisable-model-invocation: true\n---\nBody.")

	sl := NewSkillsLoader("", globalDir, "")
	summary := sl.BuildSkillsSummary()
	if !strings.Contains(summary, "visible-skill") {
		t.Error("expected visible-skill in summary")
	}
	if strings.Contains(summary, "hidden-skill") {
		t.Error("expected hidden-skill to be excluded from summary")
	}
}

func TestBuildSkillsSummary_EmptyWhenAllDisabled(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global")
	createSkillDir(t, globalDir, "only-skill",
		"---\nname: only-skill\ndescription: A skill\ndisable-model-invocation: true\n---\nBody.")

	sl := NewSkillsLoader("", globalDir, "")
	summary := sl.BuildSkillsSummary()
	if summary != "" {
		t.Errorf("expected empty summary when all skills disabled, got: %q", summary)
	}
}

func TestListSkills_HasBaseDir(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global")
	createSkillDir(t, globalDir, "my-skill", "---\nname: my-skill\ndescription: desc\n---\nBody.")

	sl := NewSkillsLoader("", globalDir, "")
	list := sl.ListSkills()
	if len(list) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(list))
	}
	if list[0].BaseDir == "" {
		t.Error("expected BaseDir to be set")
	}
	expected := filepath.Join(globalDir, "my-skill")
	if list[0].BaseDir != expected {
		t.Errorf("expected BaseDir=%q, got %q", expected, list[0].BaseDir)
	}
}

// ---------- helpers ----------

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
