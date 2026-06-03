package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/providers"
)

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

// LLMRubric grades output with the configured grader LLM.
func (e *Eval) LLMRubric(ctx context.Context, rubric, output string) (*RubricResult, error) {
	resp, err := e.Grader.Chat(ctx, &providers.ChatRequest{
		Model: e.GraderModel.ID,
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

	text := contentString(resp.Message.Content)
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
