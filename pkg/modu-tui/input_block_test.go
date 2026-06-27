package modutui

import "testing"

func TestInputBlockEditsAtCursor(t *testing.T) {
	var input InputBlock
	input.Insert("abc")
	input.MoveLeft()
	input.Insert("X")
	if got, want := input.Value, "abXc"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
	input.Backspace()
	if got, want := input.Value, "abc"; got != want {
		t.Fatalf("after backspace = %q, want %q", got, want)
	}
	input.MoveHome()
	input.DeleteForward()
	if got, want := input.Value, "bc"; got != want {
		t.Fatalf("after delete = %q, want %q", got, want)
	}
}
