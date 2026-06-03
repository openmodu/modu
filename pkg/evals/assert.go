package evals

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openmodu/modu/pkg/types"
)

// Deterministic assertions complement LLMRubricT: use them whenever a check can
// be made exactly, so you spend grader-LLM calls only on genuinely semantic
// quality. Each records a pass/fail row (score 1.0 / 0.0) to evals.jsonl just
// like the rubric grader, then fails the test on a miss.

func recordDeterministic(e *EvalT, rubric, output string, pass bool) {
	e.Helper()
	score := 0.0
	reasoning := "deterministic assertion failed"
	if pass {
		score = 1.0
		reasoning = "deterministic assertion passed"
	}
	RecordScore(e, &EvalResult{
		Rubric:    rubric,
		Output:    output,
		Reasoning: reasoning,
		Score:     score,
		Pass:      pass,
	})
}

// ContainsT asserts output contains substr.
func ContainsT(e *EvalT, substr, output string) {
	e.Helper()
	pass := strings.Contains(output, substr)
	recordDeterministic(e, fmt.Sprintf("output contains %q", substr), output, pass)
	if !pass {
		e.Fatalf("expected output to contain %q", substr)
	}
}

// NotContainsT asserts output does not contain substr.
func NotContainsT(e *EvalT, substr, output string) {
	e.Helper()
	pass := !strings.Contains(output, substr)
	recordDeterministic(e, fmt.Sprintf("output does not contain %q", substr), output, pass)
	if !pass {
		e.Fatalf("expected output not to contain %q", substr)
	}
}

// RegexpT asserts output matches the given regular expression.
func RegexpT(e *EvalT, pattern, output string) {
	e.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		e.Fatalf("invalid regexp %q: %v", pattern, err)
	}
	pass := re.MatchString(output)
	recordDeterministic(e, fmt.Sprintf("output matches /%s/", pattern), output, pass)
	if !pass {
		e.Fatalf("expected output to match /%s/", pattern)
	}
}

// ToolCalledT asserts the agent actually called the named tool. Prefer this
// over a rubric when tool usage matters: it checks recorded calls, not the
// model's claim that it used a tool.
func ToolCalledT(e *EvalT, messages []types.AgentMessage, name string) {
	e.Helper()
	pass := ToolCalled(messages, name)
	called := strings.Join(ToolCallNames(messages), ", ")
	recordDeterministic(e, fmt.Sprintf("agent called tool %q", name), called, pass)
	if !pass {
		e.Fatalf("expected agent to call tool %q; tools called: [%s]", name, called)
	}
}
