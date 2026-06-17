package tui

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// Hybrid diff-renderer mode (gated by MODU_TUI_DIFF=1).
//
// bubbletea's WithoutRenderer treats the program as non-interactive: it skips
// initInput, so neither raw mode nor SIGWINCH-driven WindowSizeMsg are set up
// (see tty.go initTerminal / checkResize). We keep bubbletea purely for its
// input parser, command/async model, and Update loop — everything modu already
// depends on — and supply the missing terminal plumbing ourselves:
//   - put stdin in raw mode (bubbletea won't, under WithoutRenderer),
//   - enable bracketed paste + hide the hardware cursor (the renderer normally
//     emits these; nilRenderer does not),
//   - watch SIGWINCH ourselves and feed real sizes back as WindowSizeMsg,
//   - paint via diffRenderer after every Update.
//
// Output is then fully owned by diffRenderer, which clears+repaints on resize
// the way pi does, eliminating the inline relative-cursor ghosting.

// startDiffMode wires up the terminal plumbing bubbletea skips under
// WithoutRenderer and returns a cleanup func to run after prog.Run returns.
func startDiffMode(ctx context.Context, prog *tea.Program, root *bubbleTUI) func() {
	fd := int(os.Stdin.Fd())
	oldState, rawErr := term.MakeRaw(fd)

	// Hide the hardware cursor (we draw a fake one) and enable bracketed paste
	// so the input parser still emits tea.PasteMsg. Also push the kitty keyboard
	// progressive-enhancement flags (disambiguate escape codes): under
	// WithoutRenderer bubbletea never negotiates them, so without this Shift+Enter
	// is indistinguishable from Enter and the steer binding is dead. Flag 1 only
	// re-encodes otherwise-ambiguous keys (Esc, modified Enter/Tab), leaving plain
	// keys untouched; terminals without kitty support ignore the sequence.
	os.Stdout.WriteString("\x1b[?25l\x1b[?2004h\x1b[>1u")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	sendSize := func() {
		w, h, err := term.GetSize(fd)
		if err != nil || w <= 0 || h <= 0 {
			return
		}
		prog.Send(tea.WindowSizeMsg{Width: w, Height: h})
	}

	go func() {
		// Seed the real size immediately — bubbletea's startup WindowSizeMsg is
		// {0,0} here because ttyOutput is never set under WithoutRenderer.
		sendSize()
		for {
			select {
			case <-sigCh:
				sendSize()
			case <-ctx.Done():
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(sigCh)
			if root.renderer != nil {
				root.renderer.Finish()
			}
			// Pop kitty flags, disable paste, show cursor (reverse of startup).
			os.Stdout.WriteString("\x1b[<u\x1b[?2004l\x1b[?25h")
			if rawErr == nil && oldState != nil {
				_ = term.Restore(fd, oldState)
			}
		})
	}
}

// paint commits any completed turns to real scrollback, then repaints the small
// active region through the diff renderer. Called (throttled) after Update.
func (b *bubbleTUI) paint() {
	if b.renderer == nil {
		return
	}
	if len(b.pendingScroll) > 0 {
		b.renderer.InsertAbove(b.pendingScroll)
		b.pendingScroll = nil
	}
	lines := b.fullScreenLines()
	b.renderer.Render(lines, b.width, b.height)
	b.renderer.PlaceCaret(b.caretActive, b.caretRow, b.caretCol)
}

// fullScreenLines builds only the active region — the in-progress turn (live
// streaming block or activity), the slash selector, and the input/status chrome
// — as one slice of terminal lines.
//
// Completed turns and the header are NOT here: they are committed to real
// terminal scrollback via InsertAbove (see enqueueScrollback / printStringCmd).
// That is what lets a resize preserve history — the terminal reflows scrollback
// itself, and the renderer only ever owns this small, bottom-anchored frame.
func (b *bubbleTUI) fullScreenLines() []string {
	width := b.width
	if width <= 0 {
		width = 80
	}
	var lines []string
	add := func(s string) { lines = appendClampedLines(lines, s, width) }

	if live := b.renderInlineLive(); live != "" {
		add(live)
	}
	if sel := b.renderSlashSuggestions(); sel != "" {
		add(sel)
	}
	inputStartRow := len(lines)
	add(b.renderInputControl())
	add(b.renderStatusLine())

	// Caret: only the plain input draws a real cursor; popup/approval states
	// manage their own. Computed against inputStartRow so the renderer can
	// anchor the hardware cursor (and thus the IME) at the typed position.
	if b.isPlainInputState() {
		lineOffset, col := b.inputCaretPos()
		b.caretActive = true
		b.caretRow = inputStartRow + lineOffset
		b.caretCol = col
	} else {
		b.caretActive = false
	}
	return lines
}

// appendClampedLines splits s into lines and appends them to dst, soft-wrapping
// any line wider than width. Every emitted line is guaranteed to fit width, so
// the terminal never wraps a frame line and desyncs the diff renderer.
func appendClampedLines(dst []string, s string, width int) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return dst
	}
	for _, ln := range strings.Split(s, "\n") {
		if ansi.StringWidth(ln) > width {
			dst = append(dst, strings.Split(ansi.Wrap(ln, width, ""), "\n")...)
		} else {
			dst = append(dst, ln)
		}
	}
	return dst
}
