package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

// RuntimeSession is the agent lifecycle surface needed by the interactive TUI.
// It intentionally excludes presentation, panels, and command handling.
type RuntimeSession interface {
	PromptWithImages(context.Context, string, []types.ImageContent) error
	Continue(context.Context) error
	FollowUpWithImages(string, []types.ImageContent) error
	SteerWithImages(string, []types.ImageContent) error
	HasQueuedMessages() bool
	Abort()
	AbortBash()
}

type RuntimeOptions struct {
	Context           context.Context
	Session           RuntimeSession
	Client            modutui.Client
	TerminalStatusTTL time.Duration
	Now               func() time.Time
	FormatDuration    func(time.Duration) string
	RefreshFooter     func()
}

// Runtime owns the foreground agent lifecycle for a TUI host: prompt
// cancellation, continuation, follow-up, steer, busy state, and terminal
// status. It does not build transcript, tool, or panel presentation data.
type Runtime struct {
	ctx               context.Context
	session           RuntimeSession
	client            modutui.Client
	terminalStatusTTL time.Duration
	now               func() time.Time
	formatDuration    func(time.Duration) string
	refreshFooter     func()

	promptMu                  sync.Mutex
	currentCancel             context.CancelFunc
	currentPromptID           int
	nextPromptID              int
	continueQueuedAfterCancel bool

	foregroundMu   sync.Mutex
	foregroundRuns int
}

func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	if options.Session == nil {
		return nil, fmt.Errorf("runtime session is required")
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.FormatDuration == nil {
		options.FormatDuration = func(duration time.Duration) string {
			return duration.Round(time.Second).String()
		}
	}
	return &Runtime{
		ctx:               options.Context,
		session:           options.Session,
		client:            options.Client,
		terminalStatusTTL: options.TerminalStatusTTL,
		now:               options.Now,
		formatDuration:    options.FormatDuration,
		refreshFooter:     options.RefreshFooter,
	}, nil
}

// PromptMutex exposes the existing session-wide prompt serialization boundary
// used by channel bridges. Callers must not use it for UI state.
func (r *Runtime) PromptMutex() *sync.Mutex {
	if r == nil {
		return nil
	}
	return &r.promptMu
}

func (r *Runtime) IsPromptActive() bool {
	if r == nil {
		return false
	}
	r.promptMu.Lock()
	defer r.promptMu.Unlock()
	return r.currentCancel != nil
}

func (r *Runtime) IsForegroundRunActive() bool {
	if r == nil {
		return false
	}
	r.foregroundMu.Lock()
	defer r.foregroundMu.Unlock()
	return r.foregroundRuns > 0
}

// Go runs host work outside Bubble Tea's update loop and reports panics in the
// transcript instead of leaving the terminal in a stuck busy state.
func (r *Runtime) Go(name string, run func()) {
	if r == nil || run == nil {
		return
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				r.client.AppendEntry(modutui.Entry{
					Role: modutui.RoleAssistant,
					Nodes: []modutui.Node{modutui.TextNode{
						Text: fmt.Sprintf("internal panic in %s: %v", name, recovered),
					}},
				})
				r.client.SetStatus("internal panic", r.terminalStatusTTL)
			}
		}()
		run()
	}()
}

func (r *Runtime) RunPrompt(text string, images []types.ImageContent) {
	if r == nil {
		return
	}
	r.Run(func(ctx context.Context) error {
		return r.session.PromptWithImages(ctx, text, images)
	})
}

// Run takes ownership of one foreground agent turn and any queued continuation
// turns that follow it.
func (r *Runtime) Run(run func(context.Context) error) {
	if r == nil || run == nil {
		return
	}
	r.markForegroundStart()
	r.Go("agent loop", func() {
		r.client.SetBusy(true)
		r.client.SetStatus("running", 0)
		defer func() {
			if r.markForegroundDone() {
				r.client.SetBusy(false)
			}
		}()

		nextRun := run
		for {
			promptCtx, cancel := context.WithCancel(r.ctx)
			started := r.now()
			promptID := r.beginPrompt(cancel)
			err := nextRun(promptCtx)
			steeringCancel := r.finishPrompt(promptID, err)
			cancel()

			if r.session.HasQueuedMessages() && (err == nil || steeringCancel) {
				r.client.SetStatus("running", 0)
				nextRun = r.session.Continue
				continue
			}

			r.finishRun(started, err)
			return
		}
	})
}

func (r *Runtime) QueueFollowUp(text string, images []types.ImageContent, requireActive bool) {
	if r == nil {
		return
	}
	r.Go("follow-up queue", func() {
		if requireActive && !r.IsPromptActive() {
			r.client.SetStatus("no active task to followup", 0)
			return
		}
		if !r.IsPromptActive() {
			r.RunPrompt(text, images)
			return
		}
		if err := r.session.FollowUpWithImages(text, images); err != nil {
			r.client.SetStatus("error: "+err.Error(), r.terminalStatusTTL)
			return
		}
		r.client.SetStatus("queued", 0)
	})
}

func (r *Runtime) QueueSteer(text string, images []types.ImageContent, requireActive bool) {
	if r == nil {
		return
	}
	r.Go("steer queue", func() {
		if requireActive && !r.IsPromptActive() {
			r.client.SetStatus("no active task to steer", 0)
			return
		}
		if !r.IsPromptActive() {
			r.RunPrompt(text, images)
			return
		}
		if err := r.session.SteerWithImages(text, images); err != nil {
			r.client.SetStatus("error: "+err.Error(), r.terminalStatusTTL)
			return
		}
		r.promptMu.Lock()
		cancel := r.currentCancel
		r.continueQueuedAfterCancel = true
		r.promptMu.Unlock()
		if cancel != nil {
			cancel()
		}
		r.session.Abort()
		r.session.AbortBash()
		r.client.SetStatus("steering", 0)
	})
}

func (r *Runtime) Interrupt() {
	if r == nil {
		return
	}
	r.Go("interrupt", func() {
		r.promptMu.Lock()
		cancel := r.currentCancel
		r.continueQueuedAfterCancel = false
		r.promptMu.Unlock()
		if cancel != nil {
			cancel()
		}
		r.session.Abort()
		r.session.AbortBash()
		r.client.SetStatus("interrupting", 0)
	})
}

func (r *Runtime) beginPrompt(cancel context.CancelFunc) int {
	r.promptMu.Lock()
	defer r.promptMu.Unlock()
	r.nextPromptID++
	r.currentPromptID = r.nextPromptID
	r.currentCancel = cancel
	return r.currentPromptID
}

func (r *Runtime) finishPrompt(promptID int, err error) bool {
	r.promptMu.Lock()
	defer r.promptMu.Unlock()
	if r.currentPromptID == promptID {
		r.currentCancel = nil
		r.currentPromptID = 0
	}
	steeringCancel := errors.Is(err, context.Canceled) && r.continueQueuedAfterCancel
	r.continueQueuedAfterCancel = false
	return steeringCancel
}

func (r *Runtime) finishRun(started time.Time, err error) {
	switch {
	case err != nil && !errors.Is(err, context.Canceled):
		r.client.AppendEntry(modutui.Entry{
			Role: modutui.RoleAssistant,
			Nodes: []modutui.Node{modutui.MarkdownNode{
				Text: "error: " + err.Error(),
			}},
		})
		r.client.SetStatus("error", r.terminalStatusTTL)
	case errors.Is(err, context.Canceled):
		r.client.SetStatus("interrupted", r.terminalStatusTTL)
	default:
		r.client.SetStatus("✓ Completed "+r.formatDuration(r.now().Sub(started)), r.terminalStatusTTL)
	}
	if r.refreshFooter != nil {
		r.refreshFooter()
	}
}

func (r *Runtime) markForegroundStart() {
	r.foregroundMu.Lock()
	r.foregroundRuns++
	r.foregroundMu.Unlock()
}

func (r *Runtime) markForegroundDone() bool {
	r.foregroundMu.Lock()
	defer r.foregroundMu.Unlock()
	if r.foregroundRuns > 0 {
		r.foregroundRuns--
	}
	return r.foregroundRuns == 0
}
