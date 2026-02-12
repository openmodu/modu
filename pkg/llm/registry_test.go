package llm

import "testing"

type testProvider struct {
	api Api
}

func (p *testProvider) Api() Api {
	return p.api
}

func (p *testProvider) Stream(model *Model, ctx *Context, opts *StreamOptions) (AssistantMessageEventStream, error) {
	return p.StreamSimple(model, ctx, nil)
}

func (p *testProvider) StreamSimple(model *Model, ctx *Context, opts *SimpleStreamOptions) (AssistantMessageEventStream, error) {
	stream := newTestStream()
	output := &AssistantMessage{
		Role:      "assistant",
		Api:       model.Api,
		Provider:  model.Provider,
		Model:     model.ID,
		Timestamp: 1,
	}
	stream.push(AssistantMessageEvent{Type: "done", Message: output, Partial: output})
	return stream, nil
}

func TestRegistryStreamSimple(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	provider := &testProvider{api: "openai-responses"}
	RegisterApiProvider(provider)

	model := &Model{Api: "openai-responses", Provider: "openai", ID: "gpt-test"}
	stream, err := StreamSimple(model, &Context{}, &SimpleStreamOptions{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	msg, err := stream.Result()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if msg == nil || msg.Model != "gpt-test" {
		t.Fatalf("unexpected message: %#v", msg)
	}
}

func TestRegistryUnregisterBySource(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	RegisterApiProviderWithSource(&testProvider{api: "openai-responses"}, "source-a")
	RegisterApiProviderWithSource(&testProvider{api: "openai-completions"}, "source-b")

	UnregisterApiProviders("source-a")

	_, ok := GetApiProvider("openai-responses")
	if ok {
		t.Fatalf("expected provider removed")
	}
	_, ok = GetApiProvider("openai-completions")
	if !ok {
		t.Fatalf("expected provider kept")
	}
}

func TestRegistryMissingProvider(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	model := &Model{Api: "openai-responses", Provider: "openai", ID: "gpt-test"}
	_, err := StreamSimple(model, &Context{}, &SimpleStreamOptions{})
	if err == nil {
		t.Fatalf("expected error")
	}
}

type testStream struct {
	ch     chan AssistantMessageEvent
	result chan result
}

type result struct {
	message *AssistantMessage
	err     error
}

func newTestStream() *testStream {
	return &testStream{
		ch:     make(chan AssistantMessageEvent, 1),
		result: make(chan result, 1),
	}
}

func (s *testStream) push(event AssistantMessageEvent) {
	if event.Type == "done" && event.Message != nil {
		s.result <- result{message: event.Message, err: nil}
	}
	if event.Type == "error" && event.Message != nil {
		s.result <- result{message: event.Message, err: event.Error}
	}
	s.ch <- event
}

func (s *testStream) Events() <-chan AssistantMessageEvent {
	return s.ch
}

func (s *testStream) Close() {
	close(s.ch)
}

func (s *testStream) Result() (*AssistantMessage, error) {
	r := <-s.result
	return r.message, r.err
}
