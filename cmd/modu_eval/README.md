# modu_eval

`modu_eval` runs LLM-backed eval tests and displays the generated `evals.jsonl`.

It is intended for behavior that is hard to assert with exact strings, such as
agent answer quality, tool-use quality, summary quality, or coding-agent output
quality.

## Concepts

- **Eval test**: a normal Go test that only runs when `GOEVALS=1` is set.
- **Model under test**: the provider/model that produces the output being evaluated.
- **Rubric**: a natural-language grading rule.
- **Grader model**: an LLM that checks whether the output satisfies each rubric.
- **`evals.jsonl`**: one JSON object per rubric result, written at the module root.

## Run

Run an eval and open the interactive viewer:

```bash
GOEVALS=1 \
EVAL_PROVIDER=lmstudio \
EVAL_BASE_URL=http://localhost:1234/v1 \
EVAL_MODEL=qwen/qwen3.6-35b-a3b \
go run ./cmd/modu_eval run -v ./pkg/agent -run Eval
```

Run in CI-style mode:

```bash
GOEVALS=1 \
EVAL_PROVIDER=lmstudio \
EVAL_BASE_URL=http://localhost:1234/v1 \
EVAL_MODEL=qwen/qwen3.6-35b-a3b \
go run ./cmd/modu_eval check -v ./pkg/agent -run Eval
```

`check` exits non-zero if `go test` failed or any test's pass rate is below the
threshold. The threshold defaults to `1.0` (every run of every test must pass).
For probabilistic evals, run each several times (`GOEVALS=5`), write them with
`LLMRubricSoft` (records but does not fail the test), and tolerate flakiness:

```bash
GOEVALS=5 ... go run ./cmd/modu_eval check --min-pass-rate 0.8 -run Eval
```

View an existing result file:

```bash
go run ./cmd/modu_eval view -f evals.jsonl
```

Print a plain text report instead of opening the TUI:

```bash
go run ./cmd/modu_eval view --plain -f evals.jsonl
```

Generate a GitHub-style comment:

```bash
go run ./cmd/modu_eval comment -v ./pkg/agent -run Eval
```

This writes `comment.md`.

## Environment

Main model:

```text
EVAL_PROVIDER      Provider id. Supports comma-separated values. Default: lmstudio
EVAL_BASE_URL      OpenAI-compatible base URL. Default depends on provider.
EVAL_API_KEY       API key for the eval provider.
EVAL_MODEL         Model under test. Required.
```

Provider-specific overrides are also supported:

```text
EVAL_OPENAI_BASE_URL
EVAL_OPENAI_API_KEY
EVAL_OPENAI_MODEL
EVAL_LMSTUDIO_BASE_URL
EVAL_LMSTUDIO_MODEL
```

Grader model:

```text
GRADER_PROVIDER    Grader provider. Defaults to the eval provider.
GRADER_BASE_URL    Grader OpenAI-compatible base URL.
GRADER_API_KEY     Grader API key.
GRADER_MODEL       Grader model. Defaults to EVAL_MODEL.
```

If `GRADER_*` is not set, the grader reuses the eval provider/model. For more
stable results, use a stronger grader model than the model under test.

## TUI

`run` and `view` open the TUI by default.

Keys:

```text
up/down or j/k     Move through rubric results
enter              Open detail view
esc                Return to list view
f                  Toggle failures-only filter
q or ctrl+c        Quit
```

The detail view shows provider, grader, rubric, output, reasoning, and score.

## Writing an Eval

Eval tests live beside the package they evaluate. Name them with `Eval` so they
can be selected with `-run Eval`.

Example:

```go
func TestBasicAgentResponseEval(t *testing.T) {
	evals.Run(t, "basic chinese factual answer", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		a := agent.NewAgent(types.Config{
			InitialState: &types.State{
				SystemPrompt: "你是一个简洁、准确的中文助手。",
				Model:        e.Model,
			},
		})

		if err := a.Prompt(context.Background(), "请用中文回答：法国的首都是哪里？"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}

		output := evals.LastAssistantText(a.GetState().Messages)
		if output == "" {
			e.Fatal("expected non-empty assistant output")
		}

		evals.LLMRubricT(e, "回答使用中文", output)
		evals.LLMRubricT(e, "回答明确指出法国首都是巴黎", output)
		evals.LLMRubricT(e, "回答没有声称法国首都是其他城市", output)
	})
}
```

Guidelines:

- Use normal Go assertions for deterministic behavior.
- Use rubrics for semantic quality that exact string assertions cannot capture.
- Keep rubrics specific and independently checkable.
- Prefer several narrow rubrics over one broad rubric.
- Do not rely only on the model saying it used a tool; assert real side effects
  or recorded tool calls when tool usage matters.

### Deterministic assertions

Besides `LLMRubricT`, `pkg/evals` provides deterministic checks that record a
pass/fail row without spending a grader-LLM call. Prefer them whenever a check
can be made exactly:

```go
evals.ContainsT(e, "巴黎", output)          // substring present
evals.NotContainsT(e, "伦敦", output)        // substring absent
evals.RegexpT(e, `^\d{4}-\d{2}-\d{2}$`, out) // matches pattern
evals.ToolCalledT(e, a.GetState().Messages, "read") // agent really called a tool
```

`ToolCalledT` inspects recorded tool calls (`evals.ToolCalls` /
`evals.ToolCalled` / `evals.ToolCallNames` are also exported), so it verifies
real tool usage rather than the model's prose.

### Flaky rubrics

`LLMRubricT` fails the test on a miss. For probabilistic rubrics that you want to
gate on an aggregate pass rate instead, use `LLMRubricSoft`, which records the
result and returns the verdict without failing the test:

```go
evals.LLMRubricSoft(e, "回答明确指出法国首都是巴黎", output)
```

Run with `GOEVALS=N` and gate via `modu_eval check --min-pass-rate`. A rubric
passes only when the grader returns `pass=true` and `score >= 0.6`.

## Examples

- `pkg/agent/agent_eval_test.go`: a basic factual-answer rubric eval.
- `pkg/agent/tool_eval_test.go`: tool-calling evals (assert the agent actually
  invokes a tool with `ToolCalledT` and grounds its answer in the result).
- `pkg/coding_agent/coding_eval_test.go`: coding evals that drive a real
  `CodingSession` in a temp dir and assert real side effects (file written/
  edited, valid Go) plus a rubric on the generated code.

## Files

- `pkg/evals`: test harness, provider setup, rubric grading, JSONL recording.
- `cmd/modu_eval`: CLI, TUI viewer, CI summary, comment generation.
- `evals.jsonl`: generated result file at the module root.
