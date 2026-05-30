package coding_agent

import "testing"

func TestTodoStoreGetSetCopies(t *testing.T) {
	ts := newTodoStore()
	in := []TodoItem{{Content: "a", Status: "pending"}}
	ts.Set(in)

	// Mutating the input slice must not affect stored state.
	in[0].Status = "done"
	got := ts.Get()
	if len(got) != 1 || got[0].Status != "pending" {
		t.Fatalf("store should copy input, got %#v", got)
	}

	// Mutating the returned slice must not affect stored state either.
	got[0].Content = "mutated"
	if ts.Get()[0].Content != "a" {
		t.Fatal("store should return a copy, not the backing slice")
	}
}

func TestTodoStoreOnChangeFires(t *testing.T) {
	ts := newTodoStore()
	calls := 0
	ts.onChange = func() { calls++ }
	ts.Set([]TodoItem{{Content: "x"}})
	if calls != 1 {
		t.Fatalf("expected onChange to fire once, got %d", calls)
	}
}

func TestTodoStoreNilOnChangeSafe(t *testing.T) {
	ts := newTodoStore() // no onChange wired
	ts.Set([]TodoItem{{Content: "x"}})
	if len(ts.Get()) != 1 {
		t.Fatal("Set should work without an onChange callback")
	}
}
