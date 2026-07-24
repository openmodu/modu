package main

import (
	"fmt"
	"sync"
	"time"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

const moduTUIDurationDebounce = 750 * time.Millisecond

type moduTUIDebounceTimer interface{ Stop() bool }

type moduTUIAgentDurationTracker struct {
	mu       sync.Mutex
	now      func() time.Time
	emit     func(modutui.Entry)
	debounce time.Duration
	schedule func(time.Duration, func()) moduTUIDebounceTimer

	started time.Time
	lastEnd time.Time
	active  bool
	gen     int
	timer   moduTUIDebounceTimer
}

func newModuTUIAgentDurationTracker(now func() time.Time, emit func(modutui.Entry)) *moduTUIAgentDurationTracker {
	if now == nil {
		now = time.Now
	}
	t := &moduTUIAgentDurationTracker{now: now, emit: emit, debounce: moduTUIDurationDebounce}
	t.schedule = func(d time.Duration, f func()) moduTUIDebounceTimer {
		return time.AfterFunc(d, f)
	}
	return t
}

func (t *moduTUIAgentDurationTracker) Handle(ev types.Event) {
	switch ev.Type {
	case types.EventTypeAgentStart:
		t.mu.Lock()
		t.gen++
		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
		if !t.active {
			t.started = t.now()
			t.active = true
		}
		t.mu.Unlock()
	case types.EventTypeAgentEnd:
		t.mu.Lock()
		if !t.active {
			t.mu.Unlock()
			return
		}
		t.lastEnd = t.now()
		gen := t.gen
		if t.timer != nil {
			t.timer.Stop()
		}
		t.timer = t.schedule(t.debounce, func() { t.finalize(gen) })
		t.mu.Unlock()
	}
}

func (t *moduTUIAgentDurationTracker) finalize(gen int) {
	t.mu.Lock()
	if !t.active || gen != t.gen {
		t.mu.Unlock()
		return
	}
	t.gen++
	total := t.lastEnd.Sub(t.started)
	t.active = false
	t.started = time.Time{}
	t.timer = nil
	emit := t.emit
	t.mu.Unlock()
	if emit != nil {
		emit(modutui.Entry{
			Role:  modutui.RoleAssistant,
			Nodes: []modutui.Node{modutui.TextNode{Text: "✓ Completed (" + formatModuTUIActivityDuration(total) + ")"}},
			Plain: true,
		})
	}
}

func formatModuTUIActivityDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d.Round(time.Second).Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds %= 60
	if seconds == 0 {
		return fmt.Sprintf("%dmin", minutes)
	}
	return fmt.Sprintf("%dmin %02ds", minutes, seconds)
}
