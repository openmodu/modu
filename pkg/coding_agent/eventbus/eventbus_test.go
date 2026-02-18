package eventbus

import (
	"sync"
	"testing"
)

func TestEmitOn(t *testing.T) {
	bus := NewEventBus()
	var received any
	bus.On("test", func(data any) {
		received = data
	})
	bus.Emit("test", "hello")
	if received != "hello" {
		t.Fatalf("expected 'hello', got %v", received)
	}
}

func TestMultipleHandlers(t *testing.T) {
	bus := NewEventBus()
	var count int
	bus.On("ch", func(data any) { count++ })
	bus.On("ch", func(data any) { count++ })
	bus.Emit("ch", nil)
	if count != 2 {
		t.Fatalf("expected 2 handlers called, got %d", count)
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := NewEventBus()
	var called bool
	unsub := bus.On("test", func(data any) {
		called = true
	})
	unsub()
	bus.Emit("test", nil)
	if called {
		t.Fatal("handler should not be called after unsubscribe")
	}
}

func TestClear(t *testing.T) {
	bus := NewEventBus()
	var called bool
	bus.On("test", func(data any) {
		called = true
	})
	bus.Clear()
	bus.Emit("test", nil)
	if called {
		t.Fatal("handler should not be called after Clear")
	}
}

func TestPanicRecovery(t *testing.T) {
	bus := NewEventBus()
	var secondCalled bool
	bus.On("test", func(data any) {
		panic("boom")
	})
	bus.On("test", func(data any) {
		secondCalled = true
	})
	// Should not panic
	bus.Emit("test", nil)
	if !secondCalled {
		t.Fatal("second handler should still be called after first panics")
	}
}

func TestConcurrentEmit(t *testing.T) {
	bus := NewEventBus()
	var mu sync.Mutex
	count := 0
	bus.On("test", func(data any) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Emit("test", nil)
		}()
	}
	wg.Wait()

	mu.Lock()
	if count != 100 {
		t.Fatalf("expected 100, got %d", count)
	}
	mu.Unlock()
}

func TestEmitNoHandlers(t *testing.T) {
	bus := NewEventBus()
	// Should not panic
	bus.Emit("nonexistent", "data")
}
