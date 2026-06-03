package coding_agent_test

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

func writeEvalFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// runGoTest runs `go test ./...` in dir and reports the combined output and
// whether it passed. This is the eval's own ground-truth check on the agent's
// implementation — independent of anything the agent claims.
func runGoTest(dir string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

const primeTestSource = `package mathutil

import "testing"

func TestIsPrime(t *testing.T) {
	cases := map[int]bool{
		-3: false, 0: false, 1: false, 2: true, 3: true, 4: false,
		17: true, 18: false, 19: true, 20: false, 97: true, 100: false,
	}
	for n, want := range cases {
		if got := IsPrime(n); got != want {
			t.Errorf("IsPrime(%d) = %v, want %v", n, got, want)
		}
	}
}
`

// TestCodingImplementToPassTestsEval is the strongest coding signal: the agent
// implements a function so that a provided test suite passes, may verify itself
// with bash, and the eval confirms correctness by actually running `go test`.
func TestCodingImplementToPassTestsEval(t *testing.T) {
	evals.Run(t, "coding: implement IsPrime to pass tests", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module mathutil\n\ngo 1.22\n")
		writeEvalFile(t, dir, "prime_test.go", primeTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"当前目录是一个 Go 模块，prime_test.go 里有针对 IsPrime(n int) bool 的测试，但实现还不存在。"+
				"请创建 prime.go（package mathutil）实现 IsPrime，使所有测试通过。"+
				"你可以用 bash 运行 `go test ./...` 自行验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		evals.ToolCalledT(e, sess.GetMessages(), "write")
		assertGoFileParses(e, filepath.Join(dir, "prime.go"))

		// Ground truth: the eval runs the tests itself.
		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes against the agent's implementation", out, passed)
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

const totalSource = `package calc

func Total(items []int) int {
	sum := 0
	for i := 0; i < len(items); i++ {
		sum = sum + items[i]
	}
	return sum
}
`

const totalTestSource = `package calc

import "testing"

func TestTotal(t *testing.T) {
	cases := []struct {
		in   []int
		want int
	}{
		{nil, 0}, {[]int{}, 0}, {[]int{5}, 5}, {[]int{1, 2, 3, 4}, 10}, {[]int{-1, 1}, 0},
	}
	for _, c := range cases {
		if got := Total(c.in); got != c.want {
			t.Errorf("Total(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
`

// TestCodingRefactorKeepsTestsGreenEval checks the agent can refactor existing
// code without breaking behavior: the seeded test suite must still pass.
func TestCodingRefactorKeepsTestsGreenEval(t *testing.T) {
	evals.Run(t, "coding: refactor keeps tests green", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module calc\n\ngo 1.22\n")
		writeEvalFile(t, dir, "calc.go", totalSource)
		writeEvalFile(t, dir, "calc_test.go", totalTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"calc.go 里的 Total 用的是 C 风格的索引循环。请用 Go 惯用法（range）重构它，"+
				"行为保持不变，calc_test.go 的测试必须仍然通过。可以用 bash 运行 `go test ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/rewrite calc.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		src := assertGoFileParses(e, filepath.Join(dir, "calc.go"))
		evals.ContainsT(e, "range", src) // actually refactored to a range loop

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... stays green after the refactor", out, passed)
	})
}

const configSource = `package app

// Config holds runtime tuning.
type Config struct {
	RetryLimit int
}

// DefaultRetryLimit is the retry count used when Config.RetryLimit is 0.
const DefaultRetryLimit = 7
`

// TestCodingReadComprehensionEval checks the agent reads the codebase to answer
// a factual question rather than guessing.
func TestCodingReadComprehensionEval(t *testing.T) {
	evals.Run(t, "coding: read and answer", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "config.go", configSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"这个项目里 DefaultRetryLimit 的默认值是多少？请查看 config.go 后回答这个数字。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		// The agent must actually inspect the file (via read, or bash/grep/find) —
		// not answer from thin air. Which inspection tool it picks is up to it.
		inspected := evals.ToolCalled(msgs, "read") || evals.ToolCalled(msgs, "bash") ||
			evals.ToolCalled(msgs, "grep") || evals.ToolCalled(msgs, "find")
		if !inspected {
			e.Fatalf("expected the agent to inspect config.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		output := evals.LastAssistantText(msgs)
		evals.ContainsT(e, "7", output)
		evals.LLMRubricT(e, "回答指出 DefaultRetryLimit 的值是 7", output)
	})
}

const strutilTestSource = `package strutil

import "testing"

func TestReverse(t *testing.T) {
	cases := map[string]string{"": "", "a": "a", "abc": "cba", "ab中c": "c中ba"}
	for in, want := range cases {
		if got := Reverse(in); got != want {
			t.Errorf("Reverse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsPalindrome(t *testing.T) {
	cases := map[string]bool{"": true, "a": true, "aba": true, "abc": false, "上海上": true}
	for in, want := range cases {
		if got := IsPalindrome(in); got != want {
			t.Errorf("IsPalindrome(%q) = %v, want %v", in, got, want)
		}
	}
}
`

// TestCodingMultiFunctionEval checks the agent can implement multiple functions
// with a non-trivial correctness constraint (rune-aware, not byte-aware) so that
// a multi-test suite passes.
func TestCodingMultiFunctionEval(t *testing.T) {
	evals.Run(t, "coding: implement Reverse + IsPalindrome", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module strutil\n\ngo 1.22\n")
		writeEvalFile(t, dir, "strutil_test.go", strutilTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"strutil_test.go 里有针对 Reverse(string) string 和 IsPalindrome(string) bool 的测试，"+
				"注意必须正确处理中文等多字节字符（按字符而非字节）。请创建 strutil.go（package strutil）"+
				"实现这两个函数使全部测试通过。可以用 bash 运行 `go test ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		evals.ToolCalledT(e, sess.GetMessages(), "write")
		assertGoFileParses(e, filepath.Join(dir, "strutil.go"))

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes for Reverse + IsPalindrome (rune-correct)", out, passed)
	})
}
