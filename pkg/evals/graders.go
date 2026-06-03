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
	// graderMaxTokens caps grader output; the rubric verdict is a small JSON object.
	graderMaxTokens = 512
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

const rubricSystemPrompt = `You are grading model output against one rubric.
If the statement in the rubric is true for the output, the output passes.
Return only a JSON object with this exact shape:
{"reasoning":"short explanation","score":0.0,"pass":false}

Score must be from 0.0 to 1.0. Passing answers should usually score at least 0.6.`

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

// LLMRubricT grades output, records the score, and fails the test when it does not pass.
func LLMRubricT(e *EvalT, rubric, output string) {
	e.Helper()

	result, err := e.LLMRubric(context.Background(), rubric, output)
	if err != nil {
		e.Fatalf("rubric grading failed: %v", err)
	}

	RecordScore(e, &EvalResult{
		Rubric:    rubric,
		Output:    output,
		Reasoning: result.Reasoning,
		Score:     result.Score,
		Pass:      result.Pass,
	})

	if !result.Pass {
		e.Fatalf("rubric failed: %s", result.Reasoning)
	}
	if result.Score < 0.6 {
		e.Fatalf("rubric score too low: %.2f: %s", result.Score, result.Reasoning)
	}
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

func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return text
	}
	return text[start : end+1]
}

// stripThinkBlocks removes reasoning-model <think>...</think> spans so the JSON
// verdict that follows them parses cleanly.
func stripThinkBlocks(text string) string {
	return strings.TrimSpace(thinkBlockRe.ReplaceAllString(text, ""))
}
