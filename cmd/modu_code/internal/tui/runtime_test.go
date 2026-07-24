package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

type runtimeSessionStub struct {
	mu sync.Mutex

	promptStarted  chan struct{}
	promptRelease  chan struct{}
	promptOnce     sync.Once
	queued         bool
	continueCalls  int
	followUps      []string
	steers         []string
	abortCalls     int
	abortBashCalls int
}

func newRuntimeSessionStub() *runtimeSessionStub {
	return &runtimeSessionStub{
		promptStarted: make(chan struct{}),
		promptRelease: make(chan struct{}),
	}
}

func (s *runtimeSessionStub) PromptWithImages(ctx context.Context, text string, _ []types.ImageContent) error {
	s.promptOnce.Do(func() { close(s.promptStarted) })
	select {
	case <-s.promptRelease:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *runtimeSessionStub) Continue(context.Context) error {
	s.mu.Lock()
	s.continueCalls++
	s.queued = false
	s.mu.Unlock()
	return nil
}

func (s *runtimeSessionStub) FollowUpWithImages(text string, _ []types.ImageContent) error {
	s.mu.Lock()
	s.followUps = append(s.followUps, text)
	s.queued = true
	s.mu.Unlock()
	return nil
}

func (s *runtimeSessionStub) SteerWithImages(text string, _ []types.ImageContent) error {
	s.mu.Lock()
	s.steers = append(s.steers, text)
	s.queued = true
	s.mu.Unlock()
	return nil
}

func (s *runtimeSessionStub) HasQueuedMessages() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queued
}

func (s *runtimeSessionStub) Abort() {
	s.mu.Lock()
	s.abortCalls++
	s.mu.Unlock()
}

func (s *runtimeSessionStub) AbortBash() {
	s.mu.Lock()
	s.abortBashCalls++
	s.mu.Unlock()
}

func TestRuntimeFollowUpContinuesActivePrompt(t *testing.T) {
	session := newRuntimeSessionStub()
	var messagesMu sync.Mutex
	var messages []any
	runtime, err := NewRuntime(RuntimeOptions{
		Context: context.Background(),
		Session: session,
		Client: modutui.NewClient(func(message any) {
			messagesMu.Lock()
			messages = append(messages, message)
			messagesMu.Unlock()
		}),
		TerminalStatusTTL: time.Second,
		FormatDuration: func(time.Duration) string {
			return "1s"
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime.RunPrompt("first", nil)
	waitRuntimeSignal(t, session.promptStarted)
	runtime.QueueFollowUp("second", nil, true)
	waitRuntimeCondition(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return len(session.followUps) == 1
	})
	close(session.promptRelease)
	waitRuntimeCondition(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.continueCalls == 1 && !runtime.IsForegroundRunActive()
	})

	session.mu.Lock()
	if len(session.followUps) != 1 || session.followUps[0] != "second" {
		t.Fatalf("followUps = %#v", session.followUps)
	}
	session.mu.Unlock()
	messagesMu.Lock()
	defer messagesMu.Unlock()
	if !containsRuntimeStatus(messages, "queued") || !containsRuntimeStatus(messages, "✓ Completed 1s") {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestRuntimeSteerCancelsAndContinues(t *testing.T) {
	session := newRuntimeSessionStub()
	runtime, err := NewRuntime(RuntimeOptions{
		Context: context.Background(),
		Session: session,
		Client:  modutui.NewClient(func(any) {}),
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime.RunPrompt("first", nil)
	waitRuntimeSignal(t, session.promptStarted)
	runtime.QueueSteer("change direction", nil, true)
	waitRuntimeCondition(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.continueCalls == 1
	})

	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.steers) != 1 || session.steers[0] != "change direction" {
		t.Fatalf("steers = %#v", session.steers)
	}
	if session.abortCalls != 1 || session.abortBashCalls != 1 {
		t.Fatalf("abort calls = %d, bash = %d", session.abortCalls, session.abortBashCalls)
	}
}

func TestRuntimeInterruptCancelsWithoutContinuation(t *testing.T) {
	session := newRuntimeSessionStub()
	statuses := make(chan string, 8)
	runtime, err := NewRuntime(RuntimeOptions{
		Context: context.Background(),
		Session: session,
		Client: modutui.NewClient(func(message any) {
			if status, ok := runtimeStatusUpdate(message); ok {
				statuses <- status.Status
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime.RunPrompt("first", nil)
	waitRuntimeSignal(t, session.promptStarted)
	runtime.Interrupt()
	waitRuntimeCondition(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.abortCalls == 1 && !runtime.IsForegroundRunActive()
	})

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.continueCalls != 0 {
		t.Fatalf("continueCalls = %d", session.continueCalls)
	}
	if session.abortBashCalls != 1 {
		t.Fatalf("abortBashCalls = %d", session.abortBashCalls)
	}
}

func TestRuntimeRejectsRequiredQueueWhenIdle(t *testing.T) {
	session := newRuntimeSessionStub()
	statuses := make(chan string, 2)
	runtime, err := NewRuntime(RuntimeOptions{
		Session: session,
		Client: modutui.NewClient(func(message any) {
			if status, ok := runtimeStatusUpdate(message); ok {
				statuses <- status.Status
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime.QueueFollowUp("later", nil, true)
	select {
	case status := <-statuses:
		if status != "no active task to followup" {
			t.Fatalf("status = %q", status)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for idle status")
	}
}

func waitRuntimeSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime signal")
	}
}

func waitRuntimeCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for runtime condition")
}

func containsRuntimeStatus(messages []any, want string) bool {
	for _, message := range messages {
		if status, ok := runtimeStatusUpdate(message); ok && status.Status == want {
			return true
		}
	}
	return false
}

func runtimeStatusUpdate(message any) (modutui.SetStatusUpdate, bool) {
	envelope, ok := message.(modutui.UpdateMsg)
	if !ok {
		return modutui.SetStatusUpdate{}, false
	}
	status, ok := envelope.Update.(modutui.SetStatusUpdate)
	return status, ok
}
