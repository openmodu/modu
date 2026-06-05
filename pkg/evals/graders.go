package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/providers"
)

const (
	// graderTimeout bounds a single grader call so a hung provider cannot wedge
	// the whole eval run.
	graderTimeout = 60 * time.Second
	// graderMaxAttempts retries transient provider/parse failures so one bad
	// response does not fail an otherwise-passing eval.
	graderMaxAttempts = 3
	// graderMaxTokens caps grader output. It must be large enough for a reasoning
	// model to finish its <think>/reasoning_content AND still emit the JSON
	// verdict: with too small a cap the model is cut off mid-think (finish_reason
	// "length") and returns empty content. The model stops early once the JSON is
	// out, so a generous cap costs nothing in the common case.
	graderMaxTokens = 4096
	// passScoreThreshold is the single source of truth for the score floor: a
	// rubric passes only when the grader marks pass=true AND score >= this. It is
	// injected into both the grader prompt and the verdict so they cannot drift.
	passScoreThreshold = 0.6
)

// thinkBlockRe matches reasoning-model <think>...</think> spans, which some
// providers embed in the message content ahead of the JSON verdict.
var thinkBlockRe = regexp.MustCompile(`(?is)<think>.*?</think>`)

// RubricResult is the structured result returned by the grader LLM.
type RubricResult struct {
	Reasoning string  `json:"reasoning"`
	Score     float64 `json:"score"`
	Pass      bool    `json:"pass"`
}

var rubricSystemPrompt = fmt.Sprintf(`You are grading model output against one rubric.
If the statement in the rubric is true for the output, the output passes.
Return only a JSON object with this exact shape:
{"reasoning":"short explanation","score":0.0,"pass":false}

Score must be from 0.0 to 1.0. Passing answers should usually score at least %.1f.`, passScoreThreshold)

// LLMRubric grades output with the configured grader LLM. Each attempt is
// bounded by graderTimeout; transient provider or parse failures are retried up
// to graderMaxAttempts so a single flaky response does not fail the eval.
func (e *Eval) LLMRubric(ctx context.Context, rubric, output string) (*RubricResult, error) {
	var lastErr error
	for attempt := 0; attempt < graderMaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result, err := e.gradeOnce(ctx, rubric, output)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("grade after %d attempts: %w", graderMaxAttempts, lastErr)
}

func (e *Eval) gradeOnce(ctx context.Context, rubric, output string) (*RubricResult, error) {
	ctx, cancel := context.WithTimeout(ctx, graderTimeout)
	defer cancel()

	temperature := 0.0
	maxTokens := graderMaxTokens
	resp, err := e.Grader.Chat(ctx, &providers.ChatRequest{
		Model:       e.GraderModel.ID,
		Temperature: &temperature, // deterministic grading
		MaxTokens:   &maxTokens,
		Messages: []providers.Message{
			{
				Role:    providers.RoleSystem,
				Content: rubricSystemPrompt,
			},
			{
				Role: providers.RoleUser,
				Content: fmt.Sprintf(
					"<Output>\n%s\n</Output>\n<Rubric>\n%s\n</Rubric>",
					output,
					rubric,
				),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("grade with llm: %w", err)
	}

	text := stripThinkBlocks(contentString(resp.Message.Content))
	var result RubricResult
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &result); err != nil {
		return nil, fmt.Errorf("decode grader result %q: %w", text, err)
	}
	return &result, nil
}

// gradeRubric grades and records one rubric, returning the verdict plus the
// grader result for messaging. The verdict (the recorded Pass) is the single
// authority: pass=true AND score >= passScoreThreshold. It Fatals only on a
// grader/transport error, never on a failing verdict.
func gradeRubric(e *EvalT, rubric, output string) (bool, *RubricResult) {
	e.Helper()

	result, err := e.LLMRubric(context.Background(), rubric, output)
	if err != nil {
		e.Fatalf("rubric grading failed: %v", err)
	}

	verdict := result.Pass && result.Score >= passScoreThreshold
	RecordScore(e, &EvalResult{
		Rubric:    rubric,
		Output:    output,
		Reasoning: result.Reasoning,
		Score:     result.Score,
		Pass:      verdict,
	})
	return verdict, result
}

// LLMRubricT grades output, records the result, and fails the test when the
// rubric does not pass.
func LLMRubricT(e *EvalT, rubric, output string) {
	e.Helper()
	if pass, result := gradeRubric(e, rubric, output); !pass {
		e.Fatalf("rubric failed (score %.2f): %s", result.Score, result.Reasoning)
	}
}

// LLMRubricSoft grades output and records the result but does NOT fail the test;
// it returns whether the rubric passed. Use it with `modu_eval check
// --min-pass-rate` so a probabilistic rubric gates on an aggregate pass rate
// across GOEVALS=N runs instead of failing the whole run on a single miss.
func LLMRubricSoft(e *EvalT, rubric, output string) bool {
	e.Helper()
	pass, _ := gradeRubric(e, rubric, output)
	return pass
}

func contentString(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

// extractJSONObject returns the first complete, brace-balanced JSON object in
// text. Grader models sometimes wrap the verdict in prose or a ```json fence,
// and reasoning models occasionally emit the same object twice; a naive
// first-{-to-last-} span would then include the gap between two objects and fail
// to parse. Scanning for the first balanced object (while respecting string
// literals and escapes) returns just the leading verdict. Falls back to the
// trimmed text when no balanced object is present.
func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	if start < 0 {
		return text
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return text
}

// stripThinkBlocks removes reasoning-model <think>...</think> spans so the JSON
// verdict that follows them parses cleanly.
func stripThinkBlocks(text string) string {
	return strings.TrimSpace(thinkBlockRe.ReplaceAllString(text, ""))
}
