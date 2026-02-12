package llm

import "testing"

func TestCalculateCost(t *testing.T) {
	model := &Model{
		Cost: ModelCost{
			Input:      2,
			Output:     4,
			CacheRead:  1,
			CacheWrite: 3,
		},
	}
	usage := &Usage{
		Input:      1000,
		Output:     500,
		CacheRead:  200,
		CacheWrite: 50,
	}
	CalculateCost(model, usage)
	if usage.Cost.Input == 0 || usage.Cost.Output == 0 || usage.Cost.Total == 0 {
		t.Fatalf("unexpected cost: %#v", usage.Cost)
	}
}

func TestSupportsXHigh(t *testing.T) {
	if !SupportsXHigh(&Model{ID: "gpt-5.2-mini", Api: "openai-responses"}) {
		t.Fatalf("expected gpt-5.2 to support xhigh")
	}
	if !SupportsXHigh(&Model{ID: "opus-4.6", Api: "anthropic-messages"}) {
		t.Fatalf("expected opus-4.6 to support xhigh")
	}
	if SupportsXHigh(&Model{ID: "gpt-4o", Api: "openai-responses"}) {
		t.Fatalf("expected gpt-4o to not support xhigh")
	}
}

func TestModelsAreEqual(t *testing.T) {
	a := &Model{ID: "m1", Provider: "openai"}
	b := &Model{ID: "m1", Provider: "openai"}
	c := &Model{ID: "m2", Provider: "openai"}
	if !ModelsAreEqual(a, b) {
		t.Fatalf("expected equal")
	}
	if ModelsAreEqual(a, c) {
		t.Fatalf("expected not equal")
	}
}
