package coding_agent_test

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// runGoBuild runs `go build ./...` in dir and reports the combined output and
// whether it compiled. Used by evals whose ground truth is "the package builds".
func runGoBuild(dir string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// runGoTestRace runs `go test -race ./...` in dir and reports the combined
// output and whether it passed cleanly. Used by the concurrency eval: a data
// race makes the detector report and the command fail, so a green run is exact
// ground truth that the agent's fix is actually race-free.
func runGoTestRace(dir string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-race", "./...")
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

const counterSource = `package store

type Counter struct {
	n int
}

func (c *Counter) Inc()       { c.n++ }
func (c *Counter) Value() int { return c.n }
`

const reportSource = `package store

import "fmt"

func Report(c *Counter) string {
	return fmt.Sprintf("count=%d", c.Value())
}
`

// The test calls c.IsEven() directly (forces a change in counter.go) AND expects
// Report to include even-ness (forces a change in report.go), so the task cannot
// be completed by editing a single file.
const counterTestSource = `package store

import "testing"

func TestIsEven(t *testing.T) {
	c := &Counter{}
	c.Inc()
	c.Inc()
	if !c.IsEven() {
		t.Fatal("Counter at 2 should be even")
	}
	c.Inc()
	if c.IsEven() {
		t.Fatal("Counter at 3 should be odd")
	}
}

func TestReport(t *testing.T) {
	c := &Counter{}
	c.Inc()
	c.Inc()
	if got := Report(c); got != "count=2 even=true" {
		t.Fatalf("Report = %q, want %q", got, "count=2 even=true")
	}
	c.Inc()
	if got := Report(c); got != "count=3 even=false" {
		t.Fatalf("Report = %q, want %q", got, "count=3 even=false")
	}
}
`

// TestCodingCrossFileEval checks the agent can make a coordinated change across
// two files: add IsEven to Counter (counter.go) and surface it in Report
// (report.go) so the test suite passes.
func TestCodingCrossFileEval(t *testing.T) {
	evals.Run(t, "coding: coordinated cross-file change", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module store\n\ngo 1.22\n")
		writeEvalFile(t, dir, "counter.go", counterSource)
		writeEvalFile(t, dir, "report.go", reportSource)
		writeEvalFile(t, dir, "counter_test.go", counterTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"counter_test.go 当前失败。它直接调用了 Counter 的 IsEven() 方法，并要求 Report 输出形如 "+
				"`count=2 even=true`。请实现：在 counter.go 给 Counter 增加 IsEven() bool 方法，"+
				"并在 report.go 更新 Report 让它带上 even=true/false。这需要同时改这两个文件。"+
				"可以用 bash 运行 `go test ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/write files; tools called: %v", evals.ToolCallNames(msgs))
		}
		counterSrc := assertGoFileParses(e, filepath.Join(dir, "counter.go"))
		assertGoFileParses(e, filepath.Join(dir, "report.go"))
		evals.ContainsT(e, "IsEven", counterSrc) // the new method really landed in counter.go

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes after the coordinated two-file change", out, passed)
	})
}

// A small multi-file package where the answer lives in one file among several;
// the agent is not told which, so it must search rather than read a named file.
const searchServerSource = `package app

func StartServer() string {
	return "listening"
}
`

const searchLimitsSource = `package app

// MaxConnections is the hard cap on concurrent connections.
const MaxConnections = 512

// idleTimeoutSeconds is unrelated noise to make the search non-trivial.
const idleTimeoutSeconds = 90
`

const searchUtilSource = `package app

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
`

// TestCodingSearchAcrossFilesEval checks the agent searches a multi-file package
// to find where a symbol is defined and reports its value, instead of being
// handed the file. It must use a search/read tool, not guess.
func TestCodingSearchAcrossFilesEval(t *testing.T) {
	evals.Run(t, "coding: search across files", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module app\n\ngo 1.22\n")
		writeEvalFile(t, dir, "server.go", searchServerSource)
		writeEvalFile(t, dir, "limits.go", searchLimitsSource)
		writeEvalFile(t, dir, "util.go", searchUtilSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"这个包里定义了一个常量 MaxConnections。它的值是多少？请在代码里查找后回答这个数字。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		inspected := evals.ToolCalled(msgs, "read") || evals.ToolCalled(msgs, "bash") ||
			evals.ToolCalled(msgs, "grep") || evals.ToolCalled(msgs, "find")
		if !inspected {
			e.Fatalf("expected the agent to search the package; tools called: %v", evals.ToolCallNames(msgs))
		}
		output := evals.LastAssistantText(msgs)
		evals.ContainsT(e, "512", output)
		evals.LLMRubricT(e, "回答指出 MaxConnections 的值是 512", output)
	})
}

const renameFormatSource = `package text

// Fmt wraps s in square brackets.
func Fmt(s string) string {
	return "[" + s + "]"
}
`

const renameWrapSource = `package text

// Wrap delegates to Fmt.
func Wrap(s string) string {
	return Fmt(s)
}
`

// The test references the NEW name Format (not yet defined) and Wrap, so the
// package does not compile until Fmt is renamed to Format in BOTH files.
const renameTestSource = `package text

import "testing"

func TestFormat(t *testing.T) {
	if got := Format("x"); got != "[x]" {
		t.Errorf("Format(%q) = %q, want %q", "x", got, "[x]")
	}
}

func TestWrap(t *testing.T) {
	if got := Wrap("y"); got != "[y]" {
		t.Errorf("Wrap(%q) = %q, want %q", "y", got, "[y]")
	}
}
`

// TestCodingRenameSymbolEval checks the agent renames a function and updates its
// call site in another file — a coordinated rename that only succeeds if both
// the definition and the caller are changed. Ground truth is `go test`.
func TestCodingRenameSymbolEval(t *testing.T) {
	evals.Run(t, "coding: rename symbol across files", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module text\n\ngo 1.22\n")
		writeEvalFile(t, dir, "format.go", renameFormatSource)
		writeEvalFile(t, dir, "wrap.go", renameWrapSource)
		writeEvalFile(t, dir, "format_test.go", renameTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"请把函数 Fmt 重命名为 Format，包括它在其他文件里的所有调用处。"+
				"format_test.go 的测试引用的是新名字 Format，重命名后测试应当全部通过。"+
				"可以用 bash 运行 `go test ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		formatSrc := assertGoFileParses(e, filepath.Join(dir, "format.go"))
		wrapSrc := assertGoFileParses(e, filepath.Join(dir, "wrap.go"))
		evals.ContainsT(e, "func Format", formatSrc)
		evals.NotContainsT(e, "func Fmt", formatSrc) // old name is gone
		evals.ContainsT(e, "Format(", wrapSrc)       // caller updated to the new name

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes after the cross-file rename", out, passed)
	})
}

// TestCodingBashInspectEval checks the agent uses bash to inspect a file and
// reports a fact derived from running a command, rather than guessing. The file
// has a known, exact line count.
func TestCodingBashInspectEval(t *testing.T) {
	evals.Run(t, "coding: inspect via bash", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		// Exactly 7 lines, each newline-terminated.
		writeEvalFile(t, dir, "data.txt", "a\nb\nc\nd\ne\nf\ng\n")

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"data.txt 一共有多少行？请用 bash 命令（例如 wc -l）查一下再回答，只回答数字。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		evals.ToolCalledT(e, msgs, "bash")
		output := evals.LastAssistantText(msgs)
		evals.ContainsT(e, "7", output)
	})
}

const discountBuggySource = `package billing

// ApplyDiscount returns price reduced by percent (0-100).
// BUG: it divides by 10 instead of 100, so the discount is 10x too large.
func ApplyDiscount(price, percent int) int {
	return price - price*percent/10
}
`

const discountTestSource = `package billing

import "testing"

func TestApplyDiscount(t *testing.T) {
	cases := []struct {
		price, percent, want int
	}{
		{100, 0, 100},
		{100, 10, 90},
		{200, 25, 150},
		{100, 100, 0},
	}
	for _, c := range cases {
		if got := ApplyDiscount(c.price, c.percent); got != c.want {
			t.Errorf("ApplyDiscount(%d, %d) = %d, want %d", c.price, c.percent, got, c.want)
		}
	}
}
`

// TestCodingDebugFailingTestEval is a debugging loop: a seeded test already fails
// because of a bug in the implementation. The agent must run the tests, locate
// the bug, fix it, and turn the suite green. The eval runs `go test` as the
// authority — independent of whatever the agent reports.
func TestCodingDebugFailingTestEval(t *testing.T) {
	evals.Run(t, "coding: debug a failing test", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module billing\n\ngo 1.22\n")
		writeEvalFile(t, dir, "discount.go", discountBuggySource)
		writeEvalFile(t, dir, "discount_test.go", discountTestSource)

		// Sanity: the suite really is red before the agent touches it.
		if _, passed := runGoTest(dir); passed {
			t.Fatal("seed bug should make the test fail before the agent runs")
		}

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"discount_test.go 当前是失败的（red）。请运行 `go test ./...` 找出 discount.go 里 ApplyDiscount 的 bug 并修复，"+
				"让所有测试通过。可以反复用 bash 运行测试来验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/write discount.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		assertGoFileParses(e, filepath.Join(dir, "discount.go"))

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes after the bug fix", out, passed)
	})
}

const panicBuggySource = `package safe

// First returns the first element of s and true, or 0 and false if s is empty.
// BUG: it indexes s[0] unconditionally and panics on an empty slice.
func First(s []int) (int, bool) {
	return s[0], true
}
`

const panicTestSource = `package safe

import "testing"

func TestFirst(t *testing.T) {
	if v, ok := First([]int{7, 8}); !ok || v != 7 {
		t.Fatalf("First([7 8]) = (%d, %v), want (7, true)", v, ok)
	}
	// Must not panic on empty input; must report ok=false.
	if v, ok := First(nil); ok || v != 0 {
		t.Fatalf("First(nil) = (%d, %v), want (0, false)", v, ok)
	}
}
`

// TestCodingFixPanicEval is a runtime-bug debugging loop (distinct from the
// logic-bug case): a seeded test crashes with an index-out-of-range panic. The
// agent must add the empty-slice guard so the suite goes green. `go test` is the
// authority — a panicking test reports FAIL, so the gate is exact.
func TestCodingFixPanicEval(t *testing.T) {
	evals.Run(t, "coding: fix a runtime panic", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module safe\n\ngo 1.22\n")
		writeEvalFile(t, dir, "safe.go", panicBuggySource)
		writeEvalFile(t, dir, "safe_test.go", panicTestSource)

		// Sanity: the suite really panics/fails before the agent touches it.
		if _, passed := runGoTest(dir); passed {
			t.Fatal("seed bug should make the test panic/fail before the agent runs")
		}

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"safe_test.go 当前失败：First 在传入空切片时会 panic（index out of range）。"+
				"请修复 safe.go 里的 First，空切片时返回 (0, false)，非空时返回首元素和 true，"+
				"让所有测试通过。可以用 bash 运行 `go test ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/write safe.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		assertGoFileParses(e, filepath.Join(dir, "safe.go"))

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes after the panic fix", out, passed)
	})
}

const raceCounterSource = `package counter

// Counter is incremented concurrently in the tests.
// BUG: Inc has a data race — n++ is unsynchronized.
type Counter struct {
	n int
}

func (c *Counter) Inc()       { c.n++ }
func (c *Counter) Value() int { return c.n }
`

const raceCounterTestSource = `package counter

import (
	"sync"
	"testing"
)

func TestCounterConcurrent(t *testing.T) {
	c := &Counter{}
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Inc()
		}()
	}
	wg.Wait()
	if got := c.Value(); got != 200 {
		t.Fatalf("Value() = %d, want 200", got)
	}
}
`

// TestCodingFixDataRaceEval checks the agent makes a type concurrency-safe: a
// seeded Counter has an unsynchronized increment that the race detector flags.
// The agent must add proper locking. Ground truth is `go test -race ./...`,
// which both detects the race and verifies the count is still correct.
func TestCodingFixDataRaceEval(t *testing.T) {
	evals.Run(t, "coding: fix a data race", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module counter\n\ngo 1.22\n")
		writeEvalFile(t, dir, "counter.go", raceCounterSource)
		writeEvalFile(t, dir, "counter_test.go", raceCounterTestSource)

		// Sanity: the race detector really flags the seed before the agent runs.
		if _, passed := runGoTestRace(dir); passed {
			t.Fatal("seed bug should fail under -race before the agent runs")
		}

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"counter.go 里的 Counter 在并发调用 Inc 时存在数据竞争，counter_test.go 用 `go test -race ./...` 会失败。"+
				"请给 Counter 加锁（例如 sync.Mutex）让 Inc 和 Value 并发安全，行为不变，"+
				"使 `go test -race ./...` 通过。可以用 bash 自行运行验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/write counter.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		assertGoFileParses(e, filepath.Join(dir, "counter.go"))

		out, passed := runGoTestRace(dir)
		evals.AssertT(e, "go test -race ./... passes after the concurrency fix", out, passed)
	})
}

const divideStubSource = `package mathx

// Divide returns a divided by b.
// TODO: when b == 0, return a non-nil error instead of panicking.
func Divide(a, b int) (int, error) {
	return a / b, nil
}
`

const divideTestSource = `package mathx

import "testing"

func TestDivide(t *testing.T) {
	if q, err := Divide(10, 2); err != nil || q != 5 {
		t.Fatalf("Divide(10, 2) = (%d, %v), want (5, nil)", q, err)
	}
	// Division by zero must be reported as an error, not panic.
	if _, err := Divide(1, 0); err == nil {
		t.Fatal("Divide(1, 0) should return a non-nil error")
	}
}
`

// TestCodingErrorHandlingEval checks the agent adds proper error handling: the
// stub divides without guarding b == 0 and panics on the zero case. The agent
// must return an error instead. `go test ./...` (which fails on the panic) is the
// authority.
func TestCodingErrorHandlingEval(t *testing.T) {
	evals.Run(t, "coding: add missing error handling", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module mathx\n\ngo 1.22\n")
		writeEvalFile(t, dir, "divide.go", divideStubSource)
		writeEvalFile(t, dir, "divide_test.go", divideTestSource)

		// Sanity: the zero case panics, so the suite is red to start.
		if _, passed := runGoTest(dir); passed {
			t.Fatal("seed should fail (panic on divide-by-zero) before the agent runs")
		}

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"divide.go 里的 Divide 在 b == 0 时会 panic。请补全错误处理：当 b == 0 时返回一个非 nil 的 error，"+
				"正常情况返回商和 nil。divide_test.go 的测试应当全部通过。可以用 bash 运行 `go test ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/write divide.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		assertGoFileParses(e, filepath.Join(dir, "divide.go"))

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes after adding error handling", out, passed)
	})
}

// buildBigSource returns a large Go file: many filler functions surrounding one
// buggy target (Apply, which should add 10 but adds 1). The size forces a real
// local edit rather than eyeballing a tiny file.
func buildBigSource(fillerCount int) string {
	var b strings.Builder
	b.WriteString("package bigpkg\n\n")
	for i := 0; i < fillerCount; i++ {
		fmt.Fprintf(&b, "// Filler%02d is unrelated padding.\nfunc Filler%02d() int { return %d }\n\n", i, i, i*7)
	}
	b.WriteString("// Apply adds 10 to x.\n// BUG: it adds 1 instead of 10.\nfunc Apply(x int) int {\n\treturn x + 1\n}\n")
	return b.String()
}

const bigPkgTestSource = `package bigpkg

import "testing"

func TestApply(t *testing.T) {
	if got := Apply(5); got != 15 {
		t.Fatalf("Apply(5) = %d, want 15", got)
	}
}
`

// TestCodingLargeFileLocalEditEval checks the agent makes a surgical, local edit
// to one function buried in a large file — using the edit tool, leaving the
// dozens of surrounding functions byte-for-byte intact. Correctness is gated by
// `go test`; the surrounding-code integrity is checked by func count + sentinels.
func TestCodingLargeFileLocalEditEval(t *testing.T) {
	evals.Run(t, "coding: surgical edit in a large file", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		const fillerCount = 60
		writeEvalFile(t, dir, "go.mod", "module bigpkg\n\ngo 1.22\n")
		writeEvalFile(t, dir, "big.go", buildBigSource(fillerCount))
		writeEvalFile(t, dir, "big_test.go", bigPkgTestSource)

		// Sanity: the seed bug makes the suite red first.
		if _, passed := runGoTest(dir); passed {
			t.Fatal("seed Apply bug should fail before the agent runs")
		}

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"big.go 是个很大的文件，里面有很多 FillerNN 函数。其中只有 Apply(x int) int 有 bug："+
				"它应当返回 x+10，现在返回的是 x+1。请只用 edit 工具对 Apply 做局部修改，"+
				"不要重写整个文件，也不要改动任何 FillerNN 函数。big_test.go 的测试应当通过。"+
				"可以用 bash 运行 `go test ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		// The capability under test is a *local* edit, not a full-file rewrite.
		evals.ToolCalledT(e, msgs, "edit")
		src := assertGoFileParses(e, filepath.Join(dir, "big.go"))

		// All surrounding functions survive: count unchanged + sentinels present.
		wantFuncs := fillerCount + 1 // fillers + Apply
		evals.AssertT(e, "no functions added or removed in big.go",
			fmt.Sprintf("found %d func decls, want %d", strings.Count(src, "func "), wantFuncs),
			strings.Count(src, "func ") == wantFuncs)
		evals.ContainsT(e, "func Filler00()", src)
		evals.ContainsT(e, "func Filler59()", src)

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes after the local edit", out, passed)
	})
}

const depMainSource = `package main

import "fmt"

// Run should greet "world" using the example.com/greetlib module.
// TODO: depend on example.com/greetlib (source in ./greetlib) and return
// greetlib.Greet("world").
func Run() string {
	return "todo"
}

func main() { fmt.Println(Run()) }
`

const depMainTestSource = `package main

import "testing"

func TestRun(t *testing.T) {
	if got := Run(); got != "Hello, world!" {
		t.Fatalf("Run() = %q, want %q", got, "Hello, world!")
	}
}
`

const depLibSource = `package greetlib

// Greet returns a greeting for name.
func Greet(name string) string {
	return "Hello, " + name + "!"
}
`

// TestCodingAddDependencyEval checks the agent wires up a new module dependency:
// it must add a require + a local replace to go.mod and import the module in
// code. The dependency is a sibling module reached via a local replace, so the
// build is fully offline (no proxy/network). Ground truth is `go test`, plus a
// check that the go.mod directives actually landed.
func TestCodingAddDependencyEval(t *testing.T) {
	evals.Run(t, "coding: add a go.mod dependency", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		// The app module that must take on the dependency.
		writeEvalFile(t, dir, "go.mod", "module app\n\ngo 1.22\n")
		writeEvalFile(t, dir, "main.go", depMainSource)
		writeEvalFile(t, dir, "main_test.go", depMainTestSource)
		// A sibling local module to depend on (its own go.mod => separate module,
		// excluded from app's ./...).
		libDir := filepath.Join(dir, "greetlib")
		if err := os.MkdirAll(libDir, 0o755); err != nil {
			t.Fatalf("mkdir greetlib: %v", err)
		}
		writeEvalFile(t, libDir, "go.mod", "module example.com/greetlib\n\ngo 1.22\n")
		writeEvalFile(t, libDir, "greet.go", depLibSource)

		// Sanity: Run returns the stub, so the suite is red before the agent runs.
		if _, passed := runGoTest(dir); passed {
			t.Fatal("seed stub should fail before the agent runs")
		}

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"请让 app 模块依赖本地模块 example.com/greetlib（源码在 ./greetlib 目录，不要联网下载）："+
				"在 go.mod 里加上对 example.com/greetlib 的 require，并用 replace 指向 ./greetlib；"+
				"然后在 main.go 里 import 它，让 Run() 返回 greetlib.Greet(\"world\")。"+
				"main_test.go 的测试应当通过。可以用 bash 运行 `go test ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		assertGoFileParses(e, filepath.Join(dir, "main.go"))
		gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			e.Fatalf("read go.mod: %v", err)
		}
		evals.ContainsT(e, "example.com/greetlib", string(gomod))
		evals.ContainsT(e, "replace", string(gomod))

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes with the new local dependency", out, passed)
	})
}

const geomDupSource = `package geom

// Area clamps both sides to [1,100] then multiplies.
func Area(w, h int) int {
	if w < 1 {
		w = 1
	}
	if w > 100 {
		w = 100
	}
	if h < 1 {
		h = 1
	}
	if h > 100 {
		h = 100
	}
	return w * h
}

// Perimeter clamps both sides to [1,100] then sums.
func Perimeter(w, h int) int {
	if w < 1 {
		w = 1
	}
	if w > 100 {
		w = 100
	}
	if h < 1 {
		h = 1
	}
	if h > 100 {
		h = 100
	}
	return 2 * (w + h)
}
`

const geomTestSource = `package geom

import "testing"

func TestArea(t *testing.T) {
	cases := []struct{ w, h, want int }{
		{5, 4, 20}, {0, 200, 100}, {-5, 50, 50}, {100, 100, 10000},
	}
	for _, c := range cases {
		if got := Area(c.w, c.h); got != c.want {
			t.Errorf("Area(%d,%d) = %d, want %d", c.w, c.h, got, c.want)
		}
	}
}

func TestPerimeter(t *testing.T) {
	cases := []struct{ w, h, want int }{
		{5, 4, 18}, {0, 200, 202}, {-5, 50, 102},
	}
	for _, c := range cases {
		if got := Perimeter(c.w, c.h); got != c.want {
			t.Errorf("Perimeter(%d,%d) = %d, want %d", c.w, c.h, got, c.want)
		}
	}
}
`

// TestCodingExtractFunctionEval is a multi-step refactor: identical clamp-to-
// [1,100] logic is duplicated in Area and Perimeter. The agent must extract it
// into a shared helper and reuse it in both, with behavior unchanged. `go test`
// guards behavior; a rise in func-decl count confirms a helper was introduced;
// a (clear-cut) rubric confirms the duplication was actually DRY'd up.
func TestCodingExtractFunctionEval(t *testing.T) {
	evals.Run(t, "coding: extract a shared helper (DRY refactor)", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module geom\n\ngo 1.22\n")
		writeEvalFile(t, dir, "geom.go", geomDupSource)
		writeEvalFile(t, dir, "geom_test.go", geomTestSource)

		// Sanity: the suite is green before the refactor (behavior is the invariant).
		if out, passed := runGoTest(dir); !passed {
			t.Fatalf("seed should pass before refactor:\n%s", out)
		}

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"geom.go 里的 Area 和 Perimeter 都重复了同一段「把数值夹在 [1,100] 区间」的逻辑。"+
				"请把这段重复逻辑提取成一个公共的辅助函数，并在 Area 和 Perimeter 里复用它，"+
				"对外行为保持不变。geom_test.go 的测试必须仍然全部通过。可以用 bash 运行 `go test ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/write geom.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		src := assertGoFileParses(e, filepath.Join(dir, "geom.go"))

		// A helper was introduced: Area + Perimeter + at least one more func.
		evals.AssertT(e, "a helper function was extracted (>=3 func decls)",
			fmt.Sprintf("found %d func decls", strings.Count(src, "func ")),
			strings.Count(src, "func ") >= 3)
		evals.LLMRubricT(e,
			"代码把原本在 Area 和 Perimeter 里重复的「夹值到 [1,100]」逻辑提取成了一个公共辅助函数，"+
				"并在两个函数里都调用了它，消除了重复", src)

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... stays green after the extract-function refactor", out, passed)
	})
}

const greetSource = `package greet

import "fmt"

// unusedLegacyHelper is pre-existing dead code. It is not referenced anywhere,
// but the task does NOT ask to remove it — a surgical agent must leave it alone.
func unusedLegacyHelper(name string) string {
	return "legacy:" + name
}

// Hello greets a single person.
func Hello(name string) string {
	return fmt.Sprintf("Hello, %s!", name)
}
`

// TestCodingSurgicalNoDeadCodeRemovalEval encodes repo guideline #3 (surgical
// changes): asked only to ADD a function, the agent must not also delete the
// pre-existing unused helper it happens to notice. The new function must work and
// the untouched dead code must still be present.
func TestCodingSurgicalNoDeadCodeRemovalEval(t *testing.T) {
	evals.Run(t, "coding: surgical, keep unrelated dead code", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module greet\n\ngo 1.22\n")
		writeEvalFile(t, dir, "greet.go", greetSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"请在 greet.go 里新增一个函数 Goodbye(name string) string，返回形如 \"Goodbye, X!\" 的字符串。"+
				"只做这一件事，不要改动文件里其他已有的代码。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		src := assertGoFileParses(e, filepath.Join(dir, "greet.go"))
		evals.ContainsT(e, "func Goodbye", src)
		// The unrelated pre-existing dead code must survive untouched.
		evals.ContainsT(e, "unusedLegacyHelper", src)
		evals.LLMRubricT(e,
			"代码新增了 Goodbye 函数返回 \"Goodbye, X!\"，并且保留了原有的 unusedLegacyHelper 和 Hello 函数没有删除", src)
	})
}

const brokenBuildSource = `package shape

import "math"

// Area returns the area of a circle with the given radius.
// BUG: this file does not compile — Pi is referenced unqualified and the
// import is therefore unused. The agent must make the package build.
func Area(radius float64) float64 {
	return Pi * radius * radius
}

var _ = math.Sqrt
`

// TestCodingCompileErrorRecoveryEval gives the agent code that does not compile
// and asks it to make the package build. The eval uses `go build ./...` as the
// ground-truth gate.
func TestCodingCompileErrorRecoveryEval(t *testing.T) {
	evals.Run(t, "coding: fix a compile error", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module shape\n\ngo 1.22\n")
		writeEvalFile(t, dir, "shape.go", brokenBuildSource)

		// Sanity: it really doesn't build before the agent runs.
		if _, ok := runGoBuild(dir); ok {
			t.Fatal("seed file should fail to compile before the agent runs")
		}

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"shape.go 目前编译不过。请修复它让 `go build ./...` 成功，Area 仍应返回圆的面积（π·r²）。"+
				"可以用 bash 运行 `go build ./...` 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/write shape.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		assertGoFileParses(e, filepath.Join(dir, "shape.go"))

		out, ok := runGoBuild(dir)
		evals.AssertT(e, "go build ./... succeeds after the fix", out, ok)
	})
}

const romanStubSource = `package roman

// RomanToInt converts a Roman numeral to its integer value.
// TODO: implement so the tests in roman_test.go pass.
func RomanToInt(s string) int {
	return 0
}
`

const romanTestSource = `package roman

import "testing"

func TestRomanToInt(t *testing.T) {
	cases := map[string]int{
		"I": 1, "III": 3, "IV": 4, "IX": 9, "LVIII": 58,
		"XL": 40, "XC": 90, "CD": 400, "CM": 900, "MCMXCIV": 1994, "MMXXIV": 2024,
	}
	for in, want := range cases {
		if got := RomanToInt(in); got != want {
			t.Errorf("RomanToInt(%q) = %d, want %d", in, got, want)
		}
	}
}
`

// TestCodingTDDEval is a red->green TDD task: a stub implementation makes the
// test compile but fail; the agent must implement RomanToInt so every case
// passes. The eval runs the tests itself as the authority.
func TestCodingTDDEval(t *testing.T) {
	evals.Run(t, "coding: TDD RomanToInt red to green", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module roman\n\ngo 1.22\n")
		writeEvalFile(t, dir, "roman.go", romanStubSource)
		writeEvalFile(t, dir, "roman_test.go", romanTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"roman.go 里的 RomanToInt 目前只是返回 0 的桩，roman_test.go 的测试因此全部失败（red）。"+
				"请用 TDD 的方式实现 RomanToInt，让所有测试通过（green）。"+
				"可以反复用 bash 运行 `go test ./...` 迭代验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/write roman.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		assertGoFileParses(e, filepath.Join(dir, "roman.go"))

		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... goes green after implementing RomanToInt", out, passed)
	})
}

// TestCodingPreserveUnrelatedFileEval checks surgical scope: the agent fixes the
// requested Go bug while leaving an unrelated sentinel file byte-for-byte intact.
func TestCodingPreserveUnrelatedFileEval(t *testing.T) {
	evals.Run(t, "coding: preserve unrelated file", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		buggy := "package main\n\n// Add should return the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a - b\n}\n"
		sentinel := "DO NOT MODIFY\nsentinel=2026-06-04\n"
		writeEvalFile(t, dir, "add.go", buggy)
		writeEvalFile(t, dir, "notes.txt", sentinel)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"只修复 add.go：Add(a, b int) int 应返回 a+b。不要修改 notes.txt，也不要改无关文件。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit/write add.go; tools called: %v", evals.ToolCallNames(msgs))
		}
		src := assertGoFileParses(e, filepath.Join(dir, "add.go"))
		evals.NotContainsT(e, "a - b", src)
		evals.LLMRubricT(e, "Add 函数返回 a 与 b 的和（a+b），没有引入额外无关逻辑", src)

		data, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
		if err != nil {
			e.Fatalf("read notes.txt: %v", err)
		}
		evals.AssertT(e, "unrelated notes.txt is unchanged", string(data), string(data) == sentinel)
	})
}

const hiddenSlugTestSource = `package slug

import "testing"

func TestSlugifyHiddenCases(t *testing.T) {
	cases := map[string]string{
		"": "",
		"Hello, World!": "hello-world",
		"Multiple   Spaces": "multiple-spaces",
		"Already--Slug": "already-slug",
		"Trim Me ": "trim-me",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
`

// TestCodingAddsImplementationAndTestsEval checks the agent writes both
// production code and a focused unit test, then the eval adds hidden tests as
// the ground-truth correctness gate.
func TestCodingAddsImplementationAndTestsEval(t *testing.T) {
	evals.Run(t, "coding: add implementation and tests", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module slug\n\ngo 1.22\n")

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"创建一个 Go 包 slug：实现 Slugify(s string) string，并同时新增 slug_test.go。"+
				"要求：转小写；连续空白或连字符折叠成一个连字符；去掉逗号、感叹号等标点；去掉首尾连字符。"+
				"请用 go test ./... 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		evals.ToolCalledT(e, sess.GetMessages(), "write")
		assertGoFileParses(e, filepath.Join(dir, "slug.go"))
		testSrc := assertGoFileParses(e, filepath.Join(dir, "slug_test.go"))
		evals.ContainsT(e, "TestSlugify", testSrc)

		writeEvalFile(t, dir, "slug_hidden_test.go", hiddenSlugTestSource)
		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes with hidden Slugify cases", out, passed)
	})
}

const clampSource = `package mathutil

// Clamp returns n unchanged for now.
// TODO: clamp n into the inclusive [min, max] range.
func Clamp(n, min, max int) int {
	return n
}
`

const clampHiddenTestSource = `package mathutil

import "testing"

func TestClampHiddenCases(t *testing.T) {
	cases := []struct {
		n, min, max int
		want        int
	}{
		{5, 1, 10, 5},
		{-2, 0, 10, 0},
		{12, 0, 10, 10},
		{7, 7, 7, 7},
	}
	for _, c := range cases {
		if got := Clamp(c.n, c.min, c.max); got != c.want {
			t.Errorf("Clamp(%d, %d, %d) = %d, want %d", c.n, c.min, c.max, got, c.want)
		}
	}
}
`

// TestCodingPkgChangeUpdatesReadmeEval encodes the repository rule that changes
// under pkg should update the relevant README/doc as part of acceptance.
func TestCodingPkgChangeUpdatesReadmeEval(t *testing.T) {
	evals.Run(t, "coding: pkg change updates README", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		pkgDir := filepath.Join(dir, "pkg", "mathutil")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatalf("mkdir pkg/mathutil: %v", err)
		}
		writeEvalFile(t, dir, "go.mod", "module example.com/project\n\ngo 1.22\n")
		writeEvalFile(t, pkgDir, "mathutil.go", clampSource)
		writeEvalFile(t, pkgDir, "README.md", "# mathutil\n\nSmall integer helpers.\n")

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"这是 pkg/mathutil 公共包。请实现 mathutil.go 里的 Clamp(n, min, max int) int，"+
				"把 n 限制在闭区间 [min, max] 内；同时更新 pkg/mathutil/README.md 记录 Clamp 的行为。"+
				"可以用 go test ./... 验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		assertGoFileParses(e, filepath.Join(pkgDir, "mathutil.go"))
		readme, err := os.ReadFile(filepath.Join(pkgDir, "README.md"))
		if err != nil {
			e.Fatalf("read README.md: %v", err)
		}
		evals.ContainsT(e, "Clamp", string(readme))
		evals.LLMRubricT(e, "README 说明了 Clamp 会把输入限制在 min 到 max 的闭区间内", string(readme))

		writeEvalFile(t, pkgDir, "mathutil_hidden_test.go", clampHiddenTestSource)
		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes with hidden Clamp cases", out, passed)
	})
}

const shapeTestSource = `package shapes

import (
	"math"
	"testing"
)

func TestRectangleArea(t *testing.T) {
	var s Shape = Rectangle{Width: 3, Height: 4}
	if got := s.Area(); got != 12 {
		t.Errorf("Rectangle.Area() = %v, want 12", got)
	}
}

func TestCircleArea(t *testing.T) {
	var s Shape = Circle{Radius: 10}
	if got := s.Area(); math.Abs(got-math.Pi*100) > 1e-9 {
		t.Errorf("Circle.Area() = %v, want %v", got, math.Pi*100)
	}
}
`

// TestCodingImplementInterfaceEval: the agent must define an interface and two
// concrete types that satisfy it so a provided test suite passes. Ground truth is
// `go test`, which only compiles if Shape/Rectangle/Circle exist with the right
// method set — an exact, prose-independent judge.
func TestCodingImplementInterfaceEval(t *testing.T) {
	evals.Run(t, "coding: implement interface to pass tests", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module shapes\n\ngo 1.22\n")
		writeEvalFile(t, dir, "shape_test.go", shapeTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"当前目录是 Go 模块（package shapes），shape_test.go 已有测试但实现不存在。"+
				"请创建 shape.go：定义接口 Shape（方法 Area() float64），以及 Rectangle{Width, Height float64} "+
				"和 Circle{Radius float64} 两个类型，都实现 Shape（矩形面积=宽*高，圆面积=π*r*r）。"+
				"可以用 bash 运行 `go test ./...` 自行验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		evals.ToolCalledT(e, sess.GetMessages(), "write")
		assertGoFileParses(e, filepath.Join(dir, "shape.go"))
		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes against the agent's interface implementation", out, passed)
	})
}

const genericMapTestSource = `package genmap

import (
	"fmt"
	"testing"
)

func TestMapIntToString(t *testing.T) {
	got := Map([]int{1, 2, 3}, func(n int) string { return fmt.Sprintf("#%d", n) })
	want := []string{"#1", "#2", "#3"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMapDouble(t *testing.T) {
	got := Map([]int{2, 4, 6}, func(n int) int { return n * 2 })
	want := []int{4, 8, 12}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}
`

// TestCodingGenericsEval: the agent must implement a generic function with two
// type parameters so a provided test that instantiates it at different types
// compiles and passes. Ground truth is `go test`.
func TestCodingGenericsEval(t *testing.T) {
	evals.Run(t, "coding: implement generic Map to pass tests", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module genmap\n\ngo 1.22\n")
		writeEvalFile(t, dir, "mapfn_test.go", genericMapTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"当前目录是 Go 模块（package genmap），mapfn_test.go 已有测试但实现不存在。"+
				"请创建 mapfn.go，实现泛型函数 Map[T, U any](s []T, f func(T) U) []U："+
				"对切片 s 的每个元素调用 f，返回结果组成的新切片。"+
				"可以用 bash 运行 `go test ./...` 自行验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		evals.ToolCalledT(e, sess.GetMessages(), "write")
		assertGoFileParses(e, filepath.Join(dir, "mapfn.go"))
		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes against the agent's generic Map", out, passed)
	})
}

const jsonTagsTestSource = `package jsontags

import (
	"encoding/json"
	"testing"
)

func TestUserJSONTags(t *testing.T) {
	u := User{UserName: "alice", IsActive: true, AgeYears: 30}
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	want := "{\"user_name\":\"alice\",\"is_active\":true,\"age_years\":30}"
	if got != want {
		t.Errorf("json.Marshal(User) = %s, want %s", got, want)
	}
}
`

// TestCodingJSONTagsEval: the agent must define a struct with json field tags so
// json.Marshal emits exactly the snake_case keys the test expects. Ground truth is
// `go test` — JSON key order follows struct field order, so the match is exact.
func TestCodingJSONTagsEval(t *testing.T) {
	evals.Run(t, "coding: struct json tags to pass tests", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module jsontags\n\ngo 1.22\n")
		writeEvalFile(t, dir, "user_test.go", jsonTagsTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"当前目录是 Go 模块（package jsontags），user_test.go 已有测试但 User 类型不存在。"+
				"请创建 user.go，定义结构体 User，字段为 UserName string、IsActive bool、AgeYears int，"+
				"并通过 json 标签让 json.Marshal 输出的键名分别是 user_name、is_active、age_years。"+
				"可以用 bash 运行 `go test ./...` 自行验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		evals.ToolCalledT(e, sess.GetMessages(), "write")
		assertGoFileParses(e, filepath.Join(dir, "user.go"))
		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes with the agent's json-tagged struct", out, passed)
	})
}

const contextTimeoutTestSource = `package slowwork

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDoWorkTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := DoWork(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DoWork err = %v, want context.DeadlineExceeded", err)
	}
}

func TestDoWorkCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := DoWork(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != 42 {
		t.Errorf("DoWork = %d, want 42", got)
	}
}
`

// TestCodingContextTimeoutEval: the agent must implement a function that honors
// context cancellation — returning ctx.Err() when the deadline fires before the
// simulated work finishes, and the result otherwise. Ground truth is `go test`,
// which only goes green if the select-on-ctx.Done idiom is actually used (a plain
// time.Sleep that ignores ctx fails the timeout case).
func TestCodingContextTimeoutEval(t *testing.T) {
	evals.Run(t, "coding: respect context timeout", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module slowwork\n\ngo 1.22\n")
		writeEvalFile(t, dir, "work_test.go", contextTimeoutTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"当前目录是 Go 模块（package slowwork），work_test.go 已有测试但实现不存在。"+
				"请创建 work.go，实现 DoWork(ctx context.Context) (int, error)："+
				"它模拟一个约 200 毫秒的任务；如果 ctx 在任务完成前被取消或超时，立即返回 0 和 ctx.Err()；"+
				"否则任务完成后返回 42 和 nil。必须真正响应 ctx（用 select 监听 ctx.Done()，不要用会忽略 ctx 的 time.Sleep）。"+
				"可以用 bash 运行 `go test ./...` 自行验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		evals.ToolCalledT(e, sess.GetMessages(), "write")
		assertGoFileParses(e, filepath.Join(dir, "work.go"))
		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes against the agent's context-aware DoWork", out, passed)
	})
}

const errorWrapTestSource = `package configload

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadWrapsNotFound(t *testing.T) {
	_, err := Load("missing")
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v should wrap ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("err %q should mention the key", err.Error())
	}
}

func TestLoadFound(t *testing.T) {
	got, err := Load("greeting")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "hello" {
		t.Errorf("Load(greeting) = %q, want hello", got)
	}
}
`

// TestCodingErrorWrapEval: the agent must wrap a sentinel error with %w so that
// errors.Is still matches it through the added context. Ground truth is `go test`
// — errors.Is only passes when the wrapping uses %w, not %v or string concat.
func TestCodingErrorWrapEval(t *testing.T) {
	evals.Run(t, "coding: wrap sentinel error with %w", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module configload\n\ngo 1.22\n")
		writeEvalFile(t, dir, "load_test.go", errorWrapTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"当前目录是 Go 模块（package configload），load_test.go 已有测试但实现不存在。"+
				"请创建 load.go：定义哨兵错误 ErrNotFound，以及函数 Load(key string) (string, error)。"+
				"当 key 为 \"greeting\" 时返回 \"hello\" 和 nil；其它 key 返回一个错误——"+
				"用 fmt.Errorf 配合 %w 包装 ErrNotFound，并在错误信息里包含该 key（让 errors.Is(err, ErrNotFound) 成立）。"+
				"可以用 bash 运行 `go test ./...` 自行验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		evals.ToolCalledT(e, sess.GetMessages(), "write")
		assertGoFileParses(e, filepath.Join(dir, "load.go"))
		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes with errors.Is matching the wrapped ErrNotFound", out, passed)
	})
}

const offByOneSource = `package window

// SumWindow returns the sum of the sub-slice nums[start : start+length].
func SumWindow(nums []int, start, length int) int {
	sum := 0
	for i := start; i <= start+length; i++ {
		sum += nums[i]
	}
	return sum
}
`

const offByOneTestSource = `package window

import "testing"

func TestSumWindow(t *testing.T) {
	nums := []int{10, 20, 30, 40, 50}
	cases := []struct {
		start, length, want int
	}{
		{1, 2, 50},  // 20+30
		{0, 3, 60},  // 10+20+30
		{2, 1, 30},  // 30
	}
	for _, c := range cases {
		if got := SumWindow(nums, c.start, c.length); got != c.want {
			t.Errorf("SumWindow(%d, %d) = %d, want %d", c.start, c.length, got, c.want)
		}
	}
}
`

// TestCodingFixOffByOneEval: the agent must fix a classic off-by-one in a loop
// bound (i <= start+length should be i < start+length), which currently sums one
// element too many. Ground truth is `go test`: the seeded code is red, and only a
// correct boundary fix turns it green.
func TestCodingFixOffByOneEval(t *testing.T) {
	evals.Run(t, "coding: fix off-by-one loop bound", func(e *evals.EvalT) {
		providers.Register(e.Provider)
		dir := t.TempDir()
		writeEvalFile(t, dir, "go.mod", "module window\n\ngo 1.22\n")
		writeEvalFile(t, dir, "window.go", offByOneSource)
		writeEvalFile(t, dir, "window_test.go", offByOneTestSource)

		sess := newCodingEvalSession(e, dir)
		defer sess.Close("eval complete")

		err := sess.Prompt(context.Background(),
			"window.go 里的 SumWindow 应返回 nums[start:start+length] 的和，但有一个 off-by-one bug，"+
				"多累加了一个元素。请修复它，使 window_test.go 的测试通过。"+
				"可以用 bash 运行 `go test ./...` 自行验证。")
		if err != nil {
			e.Fatalf("prompt: %v", err)
		}

		msgs := sess.GetMessages()
		if !evals.ToolCalled(msgs, "edit") && !evals.ToolCalled(msgs, "write") {
			e.Fatalf("expected the agent to edit or rewrite the file; tools called: %v", evals.ToolCallNames(msgs))
		}
		assertGoFileParses(e, filepath.Join(dir, "window.go"))
		out, passed := runGoTest(dir)
		evals.AssertT(e, "go test ./... passes after the off-by-one fix", out, passed)
	})
}
