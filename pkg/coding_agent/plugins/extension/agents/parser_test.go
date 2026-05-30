package agents

import (
	"strings"
	"testing"
)

func parse(t *testing.T, body string) *Profile {
	t.Helper()
	p, err := ParseProfile(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseProfile: %v\n--- input ---\n%s", err, body)
	}
	return p
}

func mustFail(t *testing.T, body, wantSubstr string) {
	t.Helper()
	_, err := ParseProfile(strings.NewReader(body))
	if err == nil {
		t.Fatalf("expected error containing %q, got nil\n--- input ---\n%s", wantSubstr, body)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error %q does not contain %q", err.Error(), wantSubstr)
	}
}

func TestParseProfileAllKnownFields(t *testing.T) {
	body := `---
name: scout
description: Fast codebase recon
tools: [read, grep, bash]
thinking: low
model: claude-sonnet-4-6
---

You are a scouting subagent.

Follow these rules.
`
	p := parse(t, body)
	if p.Name != "scout" {
		t.Errorf("Name=%q", p.Name)
	}
	if p.Description != "Fast codebase recon" {
		t.Errorf("Description=%q", p.Description)
	}
	if got := p.Tools; len(got) != 3 || got[0] != "read" || got[2] != "bash" {
		t.Errorf("Tools=%v", got)
	}
	if p.Thinking != "low" {
		t.Errorf("Thinking=%q", p.Thinking)
	}
	if p.Model != "claude-sonnet-4-6" {
		t.Errorf("Model=%q", p.Model)
	}
	if !strings.HasPrefix(p.SystemPrompt, "You are a scouting") {
		t.Errorf("SystemPrompt should start with body text, got: %q", p.SystemPrompt)
	}
	if strings.HasSuffix(p.SystemPrompt, "\n") {
		t.Errorf("SystemPrompt should be trimmed, got trailing newline")
	}
}

func TestParseProfileToolsCSV(t *testing.T) {
	body := `---
name: x
description: y
tools: "read, grep, bash"
---
body
`
	p := parse(t, body)
	if len(p.Tools) != 3 || p.Tools[1] != "grep" {
		t.Errorf("CSV tools not parsed: %v", p.Tools)
	}
}

func TestParseProfileToolsWildcard(t *testing.T) {
	body := `---
name: x
description: y
tools: ["*"]
---
body
`
	p := parse(t, body)
	if !p.AllTools() {
		t.Errorf("expected AllTools()=true for tools=[\"*\"], got Tools=%v", p.Tools)
	}
}

func TestParseProfileToolsWildcardStringForm(t *testing.T) {
	body := `---
name: x
description: y
tools: "*"
---
body
`
	p := parse(t, body)
	if !p.AllTools() {
		t.Errorf("expected AllTools()=true for tools: \"*\", got Tools=%v", p.Tools)
	}
}

func TestParseProfileWildcardMixedIsNotAll(t *testing.T) {
	body := `---
name: x
description: y
tools: ["*", "bash"]
---
body
`
	p := parse(t, body)
	if p.AllTools() {
		t.Errorf("mixed wildcard + tool should NOT count as AllTools(): %v", p.Tools)
	}
}

func TestParseProfileUnknownFieldsLandInExtra(t *testing.T) {
	body := `---
name: x
description: y
output: context.md
inheritSkills: false
nested:
  foo: 1
  bar: [a, b]
---
body
`
	p := parse(t, body)
	if p.Extra["output"] != "context.md" {
		t.Errorf("Extra[output]=%v", p.Extra["output"])
	}
	if p.Extra["inheritSkills"] != false {
		t.Errorf("Extra[inheritSkills]=%v", p.Extra["inheritSkills"])
	}
	nested, ok := p.Extra["nested"].(map[string]any)
	if !ok {
		t.Fatalf("Extra[nested] not a map: %T", p.Extra["nested"])
	}
	if nested["foo"] != 1 {
		t.Errorf("Extra.nested.foo=%v", nested["foo"])
	}
}

func TestParseProfileMissingName(t *testing.T) {
	mustFail(t, `---
description: y
---
body
`, "name")
}

func TestParseProfileMissingDescription(t *testing.T) {
	mustFail(t, `---
name: x
---
body
`, "description")
}

func TestParseProfileMissingOpeningDelim(t *testing.T) {
	mustFail(t, `name: x
description: y
---
body
`, "opening")
}

func TestParseProfileMissingClosingDelim(t *testing.T) {
	mustFail(t, `---
name: x
description: y
body without closing
`, "closing")
}

func TestParseProfileNonStringName(t *testing.T) {
	mustFail(t, `---
name: [1, 2]
description: y
---
body
`, "name")
}

func TestParseProfileNonStringTool(t *testing.T) {
	mustFail(t, `---
name: x
description: y
tools: [1, 2]
---
body
`, "tools[0]")
}

func TestParseProfileToolsBadType(t *testing.T) {
	mustFail(t, `---
name: x
description: y
tools:
  key: value
---
body
`, "tools must be")
}

func TestParseProfileCRLFEndings(t *testing.T) {
	// Windows-style line endings should not break delimiter detection.
	body := "---\r\nname: x\r\ndescription: y\r\n---\r\nbody\r\n"
	p := parse(t, body)
	if p.Name != "x" || p.SystemPrompt != "body" {
		t.Errorf("CRLF parse wrong: name=%q prompt=%q", p.Name, p.SystemPrompt)
	}
}

func TestParseProfileTrailingDelimNoBody(t *testing.T) {
	// File that ends exactly at the closing delimiter with no body.
	body := "---\nname: x\ndescription: y\n---"
	p := parse(t, body)
	if p.SystemPrompt != "" {
		t.Errorf("expected empty SystemPrompt, got %q", p.SystemPrompt)
	}
}

func TestParseProfileEmptyExtra(t *testing.T) {
	body := `---
name: x
description: y
---
body
`
	p := parse(t, body)
	if len(p.Extra) != 0 {
		t.Errorf("Extra should be empty when only known fields used, got: %v", p.Extra)
	}
}
