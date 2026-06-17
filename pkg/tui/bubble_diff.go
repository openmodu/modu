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
	// Resize: the diff renderer can't reflow the pre-wrapped lines already in
	// native scrollback (hanging indents orphan their tail). Throw the screen +
	// scrollback away and re-render the whole transcript fresh at the new width
	// so everything re-wraps cleanly (pi-style full repaint).
	if b.lastPaintW > 0 && (b.width != b.lastPaintW || b.height != b.lastPaintH) {
		above := b.rerenderScrollback()
		b.renderer.HardClear()
		b.pendingScroll = nil
		b.resetStreamTracking()
		if len(above) > 0 {
			b.renderer.InsertAbove(above, b.width)
		}
	}
	b.lastPaintW, b.lastPaintH = b.width, b.height
	b.commitStreamingPrefix()
	if len(b.pendingScroll) > 0 {
		b.renderer.InsertAbove(b.pendingScroll, b.width)
		b.pendingScroll = nil
	}
	lines := b.fullScreenLines()
	// Cap the painted frame to the screen height (show the tail). commitStreaming
	// Prefix keeps the live region small by spilling settled blocks to scrollback,
	// but the in-progress block before its first boundary (or any unsplittable
	// block) can still momentarily exceed the screen. Painting more rows than the
	// screen makes the terminal SCROLL the excess into native scrollback as a
	// "Working …" snapshot the renderer can't reclaim. Capping keeps every painted
	// frame fully owned by the diff renderer; the real content is already in
	// scrollback via InsertAbove.
	if h := b.height; h > 0 && len(lines) > h {
		drop := len(lines) - h
		lines = lines[drop:]
		if b.caretActive {
			if b.caretRow -= drop; b.caretRow < 0 {
				b.caretActive = false
			}
		}
	}
	b.renderer.Render(lines, b.width, b.height)
	b.renderer.PlaceCaret(b.caretActive, b.caretRow, b.caretCol)
}

// liveBlockIndex returns the index of the block currently drawn in the active
// frame (the streaming assistant block, or a tool block with a running tool), or
// -1 if the live region is just the activity line. Mirrors renderInlineLive so
// rerenderScrollback doesn't commit a block that is also shown live (which would
// duplicate it on resize).
func (b *bubbleTUI) liveBlockIndex() int {
	if b.model.state != uiStateQuerying {
		return -1
	}
	blocks := b.model.blocks
	if len(blocks) == 0 {
		return -1
	}
	last := len(blocks) - 1
	blk := blocks[last]
	if blk.Kind == "assistant" && blk.Streaming {
		return last
	}
	if blk.Kind == "tool" {
		for _, t := range blk.Tools {
			if t.Status == "running" {
				return last
			}
		}
	}
	return -1
}

// rerenderScrollback rebuilds the entire committed transcript (header + every
// completed block, with turn separators) freshly wrapped at the current width.
// Used on resize to replace the stale pre-wrapped native scrollback. The
// in-progress streaming block is skipped — it is drawn in the active frame and
// re-committed as it grows. Per-turn "Completed (…)" summaries are not retained
// (transient), so they drop on resize; the durable content (prompts, replies,
// tool output) all re-wraps correctly with its hanging indent intact.
func (b *bubbleTUI) rerenderScrollback() []string {
	width := b.width
	if width <= 0 {
		width = 80
	}
	liveIdx := b.liveBlockIndex()
	var out []string
	out = appendClampedLines(out, b.renderInlineHeader(), width)
	out = append(out, "")
	n := len(b.model.blocks)
	for i := 0; i < n; i++ {
		blk := b.model.blocks[i]
		if i == liveIdx {
			continue // drawn in the active frame, not scrollback
		}
		if blk.Kind == "user" && i > 0 {
			out = appendClampedLines(out, uiDimText.Render(strings.Repeat("─", turnSeparatorWidth)), width)
			out = append(out, "")
		}
		rendered := b.model.renderSingleBlock(blk)
		if strings.TrimSpace(stripANSIForGoTUI(rendered)) == "" {
			continue
		}
		out = appendClampedLines(out, rendered, width)
		out = append(out, "")
	}
	return out
}

// streamBlockClampedLines renders the assistant block and width-clamps it to the
// exact lines the live frame would show (matching fullScreenLines' appendClamped
// Lines), so a committed prefix and the live tail line up perfectly.
func (b *bubbleTUI) streamBlockClampedLines(block uiBlock) []string {
	width := b.width
	if width <= 0 {
		width = 80
	}
	return appendClampedLines(nil, b.model.renderSingleBlock(block), width)
}

// streamChromeRows approximates the non-block rows of the live frame (activity
// line + input + status) when deciding whether the frame would overflow.
const streamChromeRows = 4

func (b *bubbleTUI) resetStreamTracking() {
	b.streamBlockIdx = -1
	b.streamCommitN = 0
	b.streamCommittedContent = ""
	b.streamLines = nil
}

// commitStreamingPrefix bounds the live region during a streaming response: it
// commits the streaming assistant block's settled markdown blocks to native
// scrollback, keeping only the in-progress tail live. Without this a long reply
// grows the live frame past the screen, its top scrolls into scrollback the diff
// renderer can't clear, and every resize stacks a ghost copy.
//
// Commits are tied to CONTENT boundaries, not rendered-line counts: glamour
// re-renders the whole block on every token (line indices and wrapping shift), so
// committing by line count duplicates content. Instead we render the stable
// content prefix (everything up to the last blank-line block separator, never
// inside an open code fence), confirm it is an append-only prefix of the full
// render, and commit only the new lines. The block's finalize-time tail commit
// happens in printAssistantTailCmd; an interrupted stream is flushed here.
func (b *bubbleTUI) commitStreamingPrefix() {
	b.streamLines = nil
	blocks := b.model.blocks
	idx := len(blocks) - 1
	streaming := idx >= 0 && blocks[idx].Kind == "assistant" &&
		blocks[idx].Streaming && b.model.state == uiStateQuerying
	if !streaming {
		if b.streamBlockIdx >= 0 && b.streamBlockIdx < len(blocks) && b.streamCommitN > 0 {
			lines := b.streamBlockClampedLines(blocks[b.streamBlockIdx])
			if b.streamCommitN < len(lines) {
				b.pendingScroll = append(b.pendingScroll, lines[b.streamCommitN:]...)
				b.pendingScroll = append(b.pendingScroll, "")
			}
		}
		b.resetStreamTracking()
		return
	}
	if idx != b.streamBlockIdx {
		b.resetStreamTracking()
		b.streamBlockIdx = idx
	}
	full := blocks[idx]
	fullLines := b.streamBlockClampedLines(full)
	b.streamLines = fullLines
	if b.streamCommitN > len(fullLines) {
		b.streamCommitN = len(fullLines)
	}
	// Only start spilling to scrollback once the whole live frame (block + the
	// activity/input/status chrome ≈ 4 rows) would overflow the screen — short
	// replies that fit stay whole (committed once at MessageEnd).
	if b.height <= 0 || len(fullLines)+streamChromeRows <= b.height {
		return
	}
	cut := lastStableBlockEnd(full.Content)
	if cut <= len(b.streamCommittedContent) {
		return // no new settled block (in-progress block still open / too big)
	}
	prefix := full
	prefix.Content = full.Content[:cut]
	prefLines := b.streamBlockClampedLines(prefix)
	// Confirm the prefix render is an append-only prefix of the full render before
	// committing — otherwise glamour reflowed an earlier line and committing would
	// duplicate. Skip this round if so (try again next paint).
	if len(prefLines) <= b.streamCommitN || len(prefLines) > len(fullLines) {
		return
	}
	for i := range prefLines {
		if prefLines[i] != fullLines[i] {
			return
		}
	}
	b.pendingScroll = append(b.pendingScroll, prefLines[b.streamCommitN:]...)
	b.streamCommitN = len(prefLines)
	b.streamCommittedContent = full.Content[:cut]
}

// lastStableBlockEnd returns the byte offset after the last blank-line block
// separator in content that is NOT inside an open ``` code fence — i.e. the end
// of the last settled top-level markdown block. Content before it renders
// identically whether or not more content follows, so it is safe to commit.
// Returns 0 when nothing is settled (or a fence is still open).
func lastStableBlockEnd(content string) int {
	inFence := false
	last := 0
	pos := 0
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
		}
		pos += len(ln)
		if i < len(lines)-1 {
			pos++ // the '\n'
		}
		if !inFence && strings.TrimSpace(ln) == "" {
			last = pos
		}
	}
	if inFence {
		return 0
	}
	return last
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
