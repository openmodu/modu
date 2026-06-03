package coding_agent_test

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/evals"
	"github.com/openmodu/modu/pkg/providers"
)

// These evals drive a real CodingSession against the model under test in a
// throwaway directory: the coding tools (write/edit/bash/read) actually run, so
// the assertions check real side effects — a file created on disk, valid Go
// source, the right tool invoked — not just the model's prose. Gated by
// GOEVALS=1 like every other eval.

func newCodingEvalSession(e *evals.EvalT, dir string) *coding_agent.CodingSession {
	e.Helper()
	sess, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  filepath.Join(dir, ".coding_agent"),
		Model:     e.Model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		e.Fatalf("new coding session: %v", err)
	}
	return sess
}

// assertGoFileParses reads path, fails the eval if it is missing or does not
// parse as valid Go, and returns its source for further assertions.
func assertGoFileParses(e *evals.EvalT, path string) string {
	e.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		e.Fatalf("expected %s to exist: %v", filepath.Base(path), err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), path, data, parser.AllErrors); err != nil {
		e.Fatalf("%s is not valid Go: %v\n---\n%s", filepath.Base(path), err, data)
	}
	return string(data)
}

// TestCodingCreateFunctionEval: the agent must create a new Go file with a
// specified function using the write tool.
func TestCodingCreateFunctionEval(t *testing.T) {
	evals.Run(t, "coding: create Add function", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"在当前目录创建文件 add.go：package main，并实现函数 Add(a, b int) int 返回两数之和。"+
				"用 write 工具创建文件即可，不需要运行测试或其他命令。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		evals.ToolCalledT(e, sess.GetMessages(), "write")
		src := assertGoFileParses(e, filepath.Join(dir, "add.go"))
		evals.ContainsT(e, "func Add", src)
		evals.LLMRubricT(e, "代码定义了函数 Add，接收两个 int 参数并返回它们的和（a+b）", src)
	})
}

// TestCodingFixBugEval: the agent must fix a seeded bug in an existing file.
func TestCodingFixBugEval(t *testing.T) {
	evals.Run(t, "coding: fix Add bug", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		buggy := "package main\n\n// Add should return the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a - b\n}\n"
		if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(buggy), 0o644); err != nil {
			t.Fatalf("seed buggy file: %v", err)
		}
		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"add.go 里的 Add 函数有 bug：它应当返回 a 和 b 的和，但现在返回的是差。请修复它。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit or rewrite the file; tools called: %v", evals.ToolCallNames(msgs))
		}
		src := assertGoFileParses(e, filepath.Join(dir, "add.go"))
		evals.NotContainsT(e, "a - b", src)
		evals.LLMRubricT(e, "修复后的 Add 函数返回 a 与 b 的和（a+b），不再返回它们的差", src)
	})
}
