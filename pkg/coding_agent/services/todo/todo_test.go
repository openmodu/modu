package todo

import "testing"

func TestTodoStoreGetSetCopies(t *testing.T) {
	ts := NewStore()
	in := []Item{{Content: "a", Status: "pending"}}
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
	ts := NewStore()
	calls := 0
	ts.OnChange = func() { calls++ }
	ts.Set([]Item{{Content: "x"}})
	if calls != 1 {
		t.Fatalf("expected onChange to fire once, got %d", calls)
	}
}

func TestTodoStoreNilOnChangeSafe(t *testing.T) {
	ts := NewStore() // no onChange wired
	ts.Set([]Item{{Content: "x"}})
	if len(ts.Get()) != 1 {
		t.Fatal("Set should work without an onChange callback")
	}
}
