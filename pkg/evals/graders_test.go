package evals

import (
	"context"
	"errors"
	"testing"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

type fakeResp struct {
	content string
	err     error
}

// fakeGrader returns scripted responses; the last entry repeats once exhausted.
type fakeGrader struct {
	responses []fakeResp
	calls     int
}

func (f *fakeGrader) ID() string { return "fake" }
func (f *fakeGrader) Stream(context.Context, *providers.ChatRequest) (types.EventStream, error) {
	return nil, nil
}
func (f *fakeGrader) Chat(context.Context, *providers.ChatRequest) (*providers.ChatResponse, error) {
	i := f.calls
	f.calls++
	if i >= len(f.responses) {
		i = len(f.responses) - 1
	}
	r := f.responses[i]
	if r.err != nil {
		return nil, r.err
	}
	return &providers.ChatResponse{Message: providers.Message{Content: r.content}}, nil
}

func newFakeEval(responses ...fakeResp) (*Eval, *fakeGrader) {
	fake := &fakeGrader{responses: responses}
	return &Eval{Grader: fake, GraderModel: &types.Model{ID: "grader"}}, fake
}

func TestLLMRubricRetriesThenSucceeds(t *testing.T) {
	e, fake := newFakeEval(
		fakeResp{err: errors.New("transient network error")},
		fakeResp{content: "<think>reasoning with a } brace</think>\n{\"reasoning\":\"ok\",\"score\":0.9,\"pass\":true}"},
	)

	res, err := e.LLMRubric(context.Background(), "rubric", "output")
	if err != nil {
		t.Fatalf("LLMRubric() error = %v", err)
	}
	if !res.Pass || res.Score != 0.9 {
		t.Fatalf("unexpected result: %#v", res)
	}
	if fake.calls != 2 {
		t.Fatalf("expected 2 grader calls (1 retry), got %d", fake.calls)
	}
}

func TestLLMRubricFailsAfterMaxAttempts(t *testing.T) {
	e, fake := newFakeEval(fakeResp{err: errors.New("provider down")})

	if _, err := e.LLMRubric(context.Background(), "rubric", "output"); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if fake.calls != graderMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", graderMaxAttempts, fake.calls)
	}
}

func TestStripThinkBlocks(t *testing.T) {
	in := "<think>\nmulti-line reasoning that even has a } brace\n</think>\n{\"pass\":true}"
	got := stripThinkBlocks(in)
	if got != `{"pass":true}` {
		t.Fatalf("stripThinkBlocks() = %q", got)
	}
}

func TestExtractJSONObject(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain json",
			in:   `{"pass":true}`,
			want: `{"pass":true}`,
		},
		{
			name: "fenced explanation",
			in:   "Here is the result:\n```json\n{\"pass\":true}\n```",
			want: `{"pass":true}`,
		},
		{
			name: "no json",
			in:   "not json",
			want: "not json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJSONObject(tc.in); got != tc.want {
				t.Fatalf("extractJSONObject() = %q, want %q", got, tc.want)
			}
		})
	}
}
