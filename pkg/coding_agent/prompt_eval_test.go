package coding_agent_test

import (
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/coding_agent/services/systemprompt"
	codingtools "github.com/openmodu/modu/pkg/coding_agent/tools"
	"github.com/openmodu/modu/pkg/evals"
)

// TestNativeToolPromptGuidanceEval is a deterministic modu_eval gate for the
// prompt contract that should steer models toward the native coding tools before
// they fall back to shell commands.
func TestNativeToolPromptGuidanceEval(t *testing.T) {
	evals.Run(t, "coding prompt: native tool guidance", func(e *evals.EvalT) {
		cwd := t.TempDir()
		prompt := systemprompt.NewBuilder(cwd).
			SetTools(codingtools.CodingTools(cwd)).
			SetModel(e.Model).
			Build()

		checks := []struct {
			name string
			want string
		}{
			{
				name: "system prompt says dedicated tools beat bash",
				want: "Do not use `bash` when a dedicated tool can do the job",
			},
			{
				name: "read is preferred over shell file readers",
				want: "Use `read` to inspect a specific file you already know about; do not use `cat`",
			},
			{
				name: "grep is preferred over shell grep or rg",
				want: "Use `grep` to search file contents; do not run `grep` or `rg` through `bash`",
			},
			{
				name: "find is preferred over shell find",
				want: "Use `find` to locate files by name or path pattern; do not run shell `find`",
			},
			{
				name: "edit is preferred for existing-file changes",
				want: "Use `edit` for targeted changes to existing files",
			},
			{
				name: "write is scoped to creation or full rewrite",
				want: "Use `write` only to create new files or completely rewrite a file",
			},
			{
				name: "read before edit or overwrite",
				want: "Read a file before editing or overwriting it",
			},
			{
				name: "bash tool repeats not-for-file-ops guidance",
				want: "Do not use bash for normal file reads, content search, file-pattern search, source edits, or file creation",
			},
			{
				name: "edit tool repeats read-first guidance",
				want: "Read the file first so old_text is based on the current contents",
			},
		}

		for _, check := range checks {
			evals.AssertT(e, check.name, check.want, strings.Contains(prompt, check.want))
		}
	})
}
