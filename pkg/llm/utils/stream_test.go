package utils

import (
	"testing"

	"github.com/crosszan/modu/pkg/llm"
)

func TestEventStreamResultDone(t *testing.T) {
	stream := NewEventStream()
	msg := &llm.AssistantMessage{Role: "assistant", Model: "m1"}
	stream.Push(llm.AssistantMessageEvent{Type: "done", Message: msg, Partial: msg})
	got, err := stream.Result()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got == nil || got.Model != "m1" {
		t.Fatalf("unexpected message: %#v", got)
	}
}

func TestEventStreamResultError(t *testing.T) {
	stream := NewEventStream()
	msg := &llm.AssistantMessage{Role: "assistant", ErrorMessage: "boom"}
	stream.Push(llm.AssistantMessageEvent{Type: "error", ErrorMessage: msg, Partial: msg})
	_, err := stream.Result()
	if err == nil {
		t.Fatalf("expected error")
	}
}
