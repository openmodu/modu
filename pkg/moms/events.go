package moms

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// MomEvent is a scheduled event file.
type MomEvent struct {
	Type     string `json:"type"`     // "immediate" | "one-shot" | "periodic"
	ChatID   int64  `json:"chatId"`
	Text     string `json:"text"`
	At       string `json:"at,omitempty"`       // ISO 8601 for one-shot
	Schedule string `json:"schedule,omitempty"` // cron for periodic
	Timezone string `json:"timezone,omitempty"` // IANA tz for periodic
}

// EventTrigger is called when an event fires.
type EventTrigger func(chatID int64, filename, text string)

// EventsWatcher watches the events/ directory for JSON event files.
type EventsWatcher struct {
	eventsDir  string
	trigger    EventTrigger
	startTime  time.Time
	mu         sync.Mutex
	knownFiles map[string]struct{}
	timers     map[string]*time.Timer
	crons      map[string]cron.EntryID
	cronRunner *cron.Cron
	stopCh     chan struct{}
}

// NewEventsWatcher creates an EventsWatcher.
func NewEventsWatcher(workingDir string, trigger EventTrigger) *EventsWatcher {
	eventsDir := filepath.Join(workingDir, "events")
	return &EventsWatcher{
		eventsDir:  eventsDir,
		trigger:    trigger,
		knownFiles: make(map[string]struct{}),
		timers:     make(map[string]*time.Timer),
		crons:      make(map[string]cron.EntryID),
		cronRunner: cron.New(cron.WithLocation(time.Local)),
		stopCh:     make(chan struct{}),
	}
}

// Start begins watching. Non-blocking.
func (w *EventsWatcher) Start() {
	if err := os.MkdirAll(w.eventsDir, 0o755); err != nil {
		fmt.Printf("[moms/events] failed to create events dir: %v\n", err)
	}
	w.startTime = time.Now()
	w.cronRunner.Start()

	// Initial scan.
	w.scan()

	// Poll every second for new / deleted files.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-ticker.C:
				w.scan()
			}
		}
	}()

	fmt.Printf("[moms/events] watcher started, dir: %s\n", w.eventsDir)
}

// Stop cancels all scheduled events and stops watching.
func (w *EventsWatcher) Stop() {
	close(w.stopCh)
	w.cronRunner.Stop()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, t := range w.timers {
		t.Stop()
	}
}

// scan reads the events directory and processes new/deleted files.
func (w *EventsWatcher) scan() {
	entries, err := os.ReadDir(w.eventsDir)
	if err != nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	current := make(map[string]struct{})
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		current[e.Name()] = struct{}{}
		if _, known := w.knownFiles[e.Name()]; !known {
			w.handleFile(e.Name())
		}
	}

	// Handle deletions.
	for name := range w.knownFiles {
		if _, exists := current[name]; !exists {
			w.cancelScheduled(name)
			delete(w.knownFiles, name)
		}
	}
}

// handleFile processes a new event file. Caller holds mu.
func (w *EventsWatcher) handleFile(filename string) {
	path := filepath.Join(w.eventsDir, filename)

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var ev MomEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		fmt.Printf("[moms/events] failed to parse %s: %v\n", filename, err)
		w.deleteFile(filename)
		return
	}
	if ev.ChatID == 0 || ev.Text == "" {
		fmt.Printf("[moms/events] missing chatId or text in %s\n", filename)
		w.deleteFile(filename)
		return
	}

	w.knownFiles[filename] = struct{}{}

	switch ev.Type {
	case "immediate":
		info, err := os.Stat(path)
		if err != nil {
			return
		}
		if info.ModTime().Before(w.startTime) {
			fmt.Printf("[moms/events] stale immediate event, deleting: %s\n", filename)
			w.deleteFile(filename)
			return
		}
		fmt.Printf("[moms/events] executing immediate: %s\n", filename)
		w.trigger(ev.ChatID, filename, ev.Text)
		w.deleteFile(filename)
		delete(w.knownFiles, filename)

	case "one-shot":
		at, err := time.Parse(time.RFC3339, ev.At)
		if err != nil {
			fmt.Printf("[moms/events] invalid 'at' in %s: %v\n", filename, err)
			w.deleteFile(filename)
			return
		}
		delay := time.Until(at)
		if delay <= 0 {
			fmt.Printf("[moms/events] one-shot in the past, deleting: %s\n", filename)
			w.deleteFile(filename)
			delete(w.knownFiles, filename)
			return
		}
		name := filename
		text := ev.Text
		chatID := ev.ChatID
		fmt.Printf("[moms/events] scheduling one-shot %s in %s\n", filename, delay.Round(time.Second))
		t := time.AfterFunc(delay, func() {
			w.trigger(chatID, name, text)
			w.mu.Lock()
			w.deleteFile(name)
			delete(w.knownFiles, name)
			delete(w.timers, name)
			w.mu.Unlock()
		})
		w.timers[filename] = t

	case "periodic":
		loc := time.Local
		if ev.Timezone != "" {
			if l, err := time.LoadLocation(ev.Timezone); err == nil {
				loc = l
			}
		}
		name := filename
		text := ev.Text
		chatID := ev.ChatID
		entryID, err := w.cronRunner.AddFunc(ev.Schedule, func() {
			w.trigger(chatID, name, text)
		})
		if err != nil {
			fmt.Printf("[moms/events] invalid schedule in %s: %v\n", filename, err)
			w.deleteFile(filename)
			_ = loc
			return
		}
		w.crons[filename] = entryID
		fmt.Printf("[moms/events] scheduled periodic %s (%s)\n", filename, ev.Schedule)

	default:
		fmt.Printf("[moms/events] unknown event type %q in %s\n", ev.Type, filename)
		w.deleteFile(filename)
	}
}

// cancelScheduled cancels any timers/crons for a file.
func (w *EventsWatcher) cancelScheduled(filename string) {
	if t, ok := w.timers[filename]; ok {
		t.Stop()
		delete(w.timers, filename)
	}
	if id, ok := w.crons[filename]; ok {
		w.cronRunner.Remove(id)
		delete(w.crons, filename)
	}
}

// deleteFile removes an event file from disk.
func (w *EventsWatcher) deleteFile(filename string) {
	path := filepath.Join(w.eventsDir, filename)
	if err := os.Remove(path); err != nil && !isNotExist(err) {
		fmt.Printf("[moms/events] failed to delete %s: %v\n", filename, err)
	}
}

func isNotExist(err error) bool {
	return os.IsNotExist(err) || (err != nil && err.Error() == "file does not exist")
}

// Ensure fs.ErrNotExist is handled.
var _ = fs.ErrNotExist
