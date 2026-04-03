package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// ErrInterrupt is returned by ReadLine when the user presses Ctrl+C.
var ErrInterrupt = fmt.Errorf("interrupt")

// Input provides line-based terminal input with history and raw-mode editing
// (cursor movement, history navigation, Ctrl+A/E/K/U).
//
// When stdin is a terminal it always operates in raw mode, drawing the prompt
// and editing cursor directly via ANSI escape codes — no Screen required.
// When stdin is not a terminal (pipes, tests) it falls back to a simple
// cooked-mode read.
type Input struct {
	in      *os.File  // raw stdin for raw-mode reads
	out     io.Writer // prompt output
	screen  *Screen   // optional viewport (legacy Screen mode, unused in inline mode)
	history []string
	maxHist int
	noColor bool

	// OnCtrlR is called when the user presses Ctrl+R (expand last tool).
	OnCtrlR func()

	// OnPromptChange, if non-nil, is called whenever the inline prompt changes.
	// text is the full prompt+buffer to display; empty string means "clear".
	// Used to keep the prompt painted at the bottom during AI streaming.
	OnPromptChange func(text string)

	// typeAhead holds characters the user typed during RunScrollLoop.
	// rawReadLine picks them up as the initial buffer on the next call.
	typeAhead []rune

	// lastPrompt is the prompt string from the most recent ReadLine call.
	lastPrompt string

	// ApprovalRequests, if non-nil, receives tool approval requests during streaming.
	// The caller must send a decision string back on the Response channel.
	ApprovalRequests chan ApprovalRequest

	// ResizeCh, if non-nil, receives a signal when the terminal has been resized.
	// The readline and scroll loops redraw themselves in response.
	// Callers should send to this channel from a SIGWINCH handler.
	ResizeCh <-chan struct{}

	// boxDrawn tracks whether the 3-line input box (prompt / separator / hint)
	// has been drawn and not yet cleared.  Inline mode only.
	boxDrawn bool
}

func (i *Input) decoratePrompt(prompt string) string {
	return styled(i.noColor, ansiBrightBlack, "│ ") + prompt
}

// ApprovalRequest is sent on Input.ApprovalRequests when a tool needs user approval.
type ApprovalRequest struct {
	ToolName   string
	ToolCallID string
	Args       map[string]any
	// Response must receive exactly one decision: "allow", "allow_always", "deny", "deny_always".
	Response chan<- string
	// Cancel, if non-nil, is closed by the caller when the decision has already
	// been made externally (e.g. via Telegram). printApproval will dismiss the
	// prompt and return without sending to Response.
	Cancel <-chan struct{}
}

// NewInput creates an Input reading from in and writing the prompt to out.
// When in is a terminal, raw-mode line editing is used automatically.
func NewInput(in io.Reader, out io.Writer) *Input {
	f, _ := in.(*os.File)
	return &Input{
		in:      f,
		out:     out,
		maxHist: 200,
		noColor: shouldDisableColor(out),
	}
}

// NewInputWithScreen creates an Input backed by a Screen viewport.
// Kept for callers that want the Screen-based layout.
func NewInputWithScreen(in io.Reader, s *Screen) *Input {
	f, _ := in.(*os.File)
	return &Input{
		in:      f,
		out:     s.out,
		screen:  s,
		maxHist: 200,
		noColor: s.noColor,
	}
}

// ReadLine displays prompt and returns the next line of input.
// Returns ("", ErrInterrupt) on Ctrl+C and ("", io.EOF) on Ctrl+D / EOF.
func (i *Input) ReadLine(prompt string) (string, error) {
	styledPrompt := styled(i.noColor, ansiBold+ansiGreen, prompt)

	// Use raw mode whenever stdin is an interactive terminal.
	if i.in != nil && isTerminalFd(uintptr(i.in.Fd())) {
		return i.rawReadLine(styledPrompt)
	}

	// Non-interactive fallback (pipe / test).
	fmt.Fprint(i.out, styledPrompt)
	var buf strings.Builder
	raw := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(raw)
		if n > 0 {
			ch := raw[0]
			if ch == '\n' || ch == '\r' {
				break
			}
			buf.WriteByte(ch)
		}
		if err != nil {
			line := strings.TrimRight(buf.String(), "\r\n")
			if line != "" {
				i.addHistory(line)
				return line, nil
			}
			return "", err
		}
	}
	line := buf.String()
	i.addHistory(line)
	return line, nil
}

// History returns a copy of the history list (oldest first).
func (i *Input) History() []string {
	out := make([]string, len(i.history))
	copy(out, i.history)
	return out
}

// LoadHistoryFile reads newline-separated history entries from path.
// Missing file is silently ignored.
func (i *Input) LoadHistoryFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			i.addHistory(line)
		}
	}
	return nil
}

// SaveHistoryFile writes the current history to path (newline-separated).
func (i *Input) SaveHistoryFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var sb strings.Builder
	for _, line := range i.history {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(sb.String()), 0o600)
}

func (i *Input) addHistory(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	for j, h := range i.history {
		if h == line {
			i.history = append(i.history[:j], i.history[j+1:]...)
			break
		}
	}
	i.history = append(i.history, line)
	if len(i.history) > i.maxHist {
		i.history = i.history[len(i.history)-i.maxHist:]
	}
}

// ── raw mode readline ─────────────────────────────────────────────────────────

// lineState holds the mutable state of the current input line.
type lineState struct {
	buf    []rune // full line buffer
	cursor int    // cursor position (rune index into buf)
}

func (ls *lineState) insert(r rune) {
	ls.buf = append(ls.buf, 0)
	copy(ls.buf[ls.cursor+1:], ls.buf[ls.cursor:])
	ls.buf[ls.cursor] = r
	ls.cursor++
}

func (ls *lineState) deleteBack() {
	if ls.cursor == 0 {
		return
	}
	ls.buf = append(ls.buf[:ls.cursor-1], ls.buf[ls.cursor:]...)
	ls.cursor--
}

func (ls *lineState) deleteForward() {
	if ls.cursor >= len(ls.buf) {
		return
	}
	ls.buf = append(ls.buf[:ls.cursor], ls.buf[ls.cursor+1:]...)
}

// rawReadLine runs a full readline loop with the terminal in raw mode.
// Works in two sub-modes:
//   - Screen mode (i.screen != nil): delegates drawing to Screen methods.
//   - Inline mode (i.screen == nil): draws directly with ANSI escape codes.
func (i *Input) rawReadLine(prompt string) (string, error) {
	i.lastPrompt = prompt

	fd := int(i.in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Can't enter raw mode – fall back to cooked read.
		i.initLine(prompt)
		var sb strings.Builder
		buf := make([]byte, 1)
		for {
			n, readErr := i.in.Read(buf)
			if n > 0 && buf[0] != '\n' && buf[0] != '\r' {
				sb.WriteByte(buf[0])
			}
			if (n > 0 && (buf[0] == '\n' || buf[0] == '\r')) || readErr != nil {
				line := sb.String()
				i.doneLine()
				i.addHistory(line)
				return line, readErr
			}
		}
	}
	defer term.Restore(fd, oldState)

	// Switch stdin to non-blocking so the main select can also watch
	// background channels such as ApprovalRequests (tool approval from Telegram).
	syscall.SetNonblock(fd, true)

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	byteCh := make(chan byte, 256)

	go func() {
		defer close(readerDone)
		buf := []byte{0}
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := syscall.Read(fd, buf)
			if n > 0 {
				select {
				case byteCh <- buf[0]:
				case <-stop:
					return
				}
			}
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if err != nil {
				return
			}
		}
	}()

	defer func() {
		close(stop)
		<-readerDone
		syscall.SetNonblock(fd, false)
	}()

	i.initLine(prompt)

	// Pre-fill with any characters typed during the previous streaming phase.
	ls := &lineState{}
	if len(i.typeAhead) > 0 {
		ls.buf = i.typeAhead
		ls.cursor = len(ls.buf)
		i.typeAhead = nil
		i.redrawContent(prompt, ls)
	}
	histIdx := len(i.history) // points past end = current editing buffer
	savedBuf := []rune{}      // snapshot of buf before history navigation

	redraw := func() { i.redrawContent(prompt, ls) }

	// readByte reads the next byte from the async byte channel.
	// Used for escape sequences where we need multiple bytes sequentially.
	readByte := func() (byte, error) {
		select {
		case b := <-byteCh:
			return b, nil
		case <-stop:
			return 0, io.EOF
		}
	}

	// redrawBox redraws the input box with the current buffer and repositions
	// the cursor.  Used after approval prompts and terminal resizes.
	redrawBox := func() {
		i.initLine(prompt)
		if len(ls.buf) > 0 {
			fmt.Fprint(i.out, string(ls.buf))
			tailCols := visibleLen(string(ls.buf[ls.cursor:]))
			if tailCols > 0 {
				fmt.Fprintf(i.out, "\033[%dD", tailCols)
			}
		}
	}

	for {
		// Check for background approval requests (e.g. from a Telegram prompt)
		// before blocking on keyboard input.
		var approvalCh <-chan ApprovalRequest
		if i.ApprovalRequests != nil {
			approvalCh = i.ApprovalRequests
		}

		var b byte
		select {
		case req := <-approvalCh:
			// A tool-approval request arrived while the user was idle at the
			// prompt. Clear the box, show the approval UI, then re-draw the
			// box with whatever the user had typed so far.
			if i.screen == nil {
				i.clearBox()
				fmt.Fprint(i.out, "\r\n")
			}
			i.printApproval(req, byteCh, stop)
			redrawBox()
			continue

		case <-i.ResizeCh:
			// Terminal was resized.  Redraw the input box at the new width.
			// We cannot rely on clearBox() here because relative cursor moves
			// break after a resize (the terminal may have reflowed lines).
			// Instead, erase from the start of the current line to the end of
			// the screen (\r\033[J), then redraw the whole box fresh.
			if i.screen != nil {
				i.screen.RedrawInputContent(prompt, string(ls.buf), ls.cursor)
			} else {
				fmt.Fprint(i.out, "\r\033[J") // CR + erase to end of screen
				i.boxDrawn = false
				redrawBox()
			}
			continue

		case nextByte := <-byteCh:
			b = nextByte
		}

		switch b {
		case '\r', '\n': // Enter
			line := string(ls.buf)
			i.scrollToBottom()
			i.doneLine()
			i.addHistory(line)
			return line, nil

		case 3: // Ctrl+C
			i.doneLine()
			return "", ErrInterrupt

		case 4: // Ctrl+D – EOF if line is empty, else delete forward
			if len(ls.buf) == 0 {
				i.doneLine()
				return "", io.EOF
			}
			ls.deleteForward()
			redraw()

		case 127, 8: // Backspace / DEL
			ls.deleteBack()
			redraw()

		case 1: // Ctrl+A – beginning of line
			ls.cursor = 0
			redraw()

		case 5: // Ctrl+E – end of line
			ls.cursor = len(ls.buf)
			redraw()

		case 11: // Ctrl+K – kill to end of line
			ls.buf = ls.buf[:ls.cursor]
			redraw()

		case 18: // Ctrl+R – expand last tool call
			if i.OnCtrlR != nil {
				if i.screen == nil {
					// Inline mode: clear the box, run the handler, then
					// redraw the box with the current buffer.
					i.clearBox()
					fmt.Fprint(i.out, "\r\n")
					i.OnCtrlR()
					i.initLine(prompt)
					if len(ls.buf) > 0 {
						fmt.Fprint(i.out, string(ls.buf))
						tailCols := visibleLen(string(ls.buf[ls.cursor:]))
						if tailCols > 0 {
							fmt.Fprintf(i.out, "\033[%dD", tailCols)
						}
					}
				} else {
					i.OnCtrlR()
				}
			}

		case 21: // Ctrl+U – kill to start of line
			ls.buf = ls.buf[ls.cursor:]
			ls.cursor = 0
			redraw()

		case 27: // ESC – start of escape sequence
			seq1, err := readByte()
			if err != nil {
				continue
			}
			if seq1 != '[' && seq1 != 'O' {
				continue // unknown sequence, ignore
			}
			seq2, err := readByte()
			if err != nil {
				continue
			}
			switch seq2 {
			case '<': // SGR mouse event: only handled in Screen mode
				if i.screen != nil {
					i.handleSGRMouse(readByte)
				} else {
					// Consume until M or m.
					for {
						c, err := readByte()
						if err != nil || c == 'M' || c == 'm' {
							break
						}
					}
				}
				continue
			case 'A': // Up – history previous
				if histIdx > 0 {
					if histIdx == len(i.history) {
						savedBuf = make([]rune, len(ls.buf))
						copy(savedBuf, ls.buf)
					}
					histIdx--
					ls.buf = []rune(i.history[histIdx])
					ls.cursor = len(ls.buf)
					redraw()
				}
			case 'B': // Down – history next
				if histIdx < len(i.history) {
					histIdx++
					if histIdx == len(i.history) {
						ls.buf = make([]rune, len(savedBuf))
						copy(ls.buf, savedBuf)
					} else {
						ls.buf = []rune(i.history[histIdx])
					}
					ls.cursor = len(ls.buf)
					redraw()
				}
			case 'C': // Right
				if ls.cursor < len(ls.buf) {
					ls.cursor++
					redraw()
				}
			case 'D': // Left
				if ls.cursor > 0 {
					ls.cursor--
					redraw()
				}
			case 'H', '1': // Home
				if seq2 == '1' {
					readByte() // ~
				}
				ls.cursor = 0
				redraw()
			case 'F', '4': // End
				if seq2 == '4' {
					readByte() // ~
				}
				ls.cursor = len(ls.buf)
				redraw()
			case '3': // Delete (ESC[3~)
				readByte() // ~
				ls.deleteForward()
				redraw()
			case '5': // Page Up (ESC[5~)
				readByte() // ~
				if i.screen != nil {
					i.screen.ScrollUp(i.screen.ContentBottom() / 2)
				}
			case '6': // Page Down (ESC[6~)
				readByte() // ~
				if i.screen != nil {
					i.screen.ScrollDown(i.screen.ContentBottom() / 2)
				}
			default:
				if seq2 >= '0' && seq2 <= '9' {
					readByte() // consume trailing ~
				}
			}

		default:
			// Printable character – may be multi-byte UTF-8.
			if b < 0x80 {
				if b >= 32 {
					ls.insert(rune(b))
					redraw()
				}
			} else {
				n := utf8ByteLen(b)
				if n < 1 {
					continue
				}
				seq := make([]byte, n)
				seq[0] = b
				for k := 1; k < n; k++ {
					seq[k], err = readByte()
					if err != nil {
						break
					}
				}
				r, _ := utf8.DecodeRune(seq)
				if r != utf8.RuneError {
					ls.insert(r)
					redraw()
				}
			}
		}
	}
}

// ── drawing helpers ───────────────────────────────────────────────────────────

// initLine sets up the input line before editing begins.
// In inline mode it draws a 3-line Claude Code-style box:
//
//	❯ [input]
//	──────────────────────
//	  /help · ctrl+c
//
// and then moves the cursor back to the prompt line.
func (i *Input) initLine(prompt string) {
	if i.screen != nil {
		i.screen.InitInputLine(prompt)
		return
	}
	w := termWidth()
	// Reserve one column so the separator never occupies exactly the full
	// terminal width. An exactly-full-width line wraps on some terminals,
	// which would shift the hint row down and break the \033[2A cursor-up.
	sepW := w - 1
	if sepW < 1 {
		sepW = 1
	}
	sep := styled(i.noColor, ansiBrightBlack, strings.Repeat("─", sepW))

	// Truncate the hint to fit in w columns so it never wrap either.
	hintText := "  /help commands · /dashboard runtime · ctrl+c interrupt"
	if visibleLen(hintText) >= w {
		// Keep as many runes as fit, leaving room for at least 1 column margin.
		runes := []rune(hintText)
		for visibleLen(string(runes)) >= w && len(runes) > 0 {
			runes = runes[:len(runes)-1]
		}
		hintText = string(runes)
	}
	hint := styled(i.noColor, ansiDim, hintText)
	promptLine := i.decoratePrompt(prompt)
	cursorCol := visibleLen("│ ") + visibleLen(prompt)

	// Print prompt line, separator, hint; then jump back up to the prompt row.
	// Both sep and hint are guaranteed to fit in one terminal row (no wrapping),
	// so \033[2A reliably moves back exactly 2 physical rows.
	fmt.Fprintf(i.out, "%s\r\n%s\r\n%s\033[2A\r\033[%dC",
		promptLine, sep, hint, cursorCol)
	i.boxDrawn = true
}

// clearBox erases the separator and hint lines below the prompt line, leaving
// the cursor on the prompt line.  No-op if the box is not currently drawn.
func (i *Input) clearBox() {
	if !i.boxDrawn {
		return
	}
	// Move down to separator line, clear it; move down to hint line, clear it;
	// then return up two lines to the prompt line.
	fmt.Fprint(i.out, "\033[1B\r\033[2K\033[1B\r\033[2K\033[2A")
	i.boxDrawn = false
}

// doneLine is called when a line is committed (Enter / Ctrl+C / EOF).
func (i *Input) doneLine() {
	if i.screen != nil {
		i.screen.AfterReadLine()
		return
	}
	// In raw mode \n is a bare line-feed (no carriage return), so the
	// cursor would stay in the middle of the line.  Use \r\n to go to
	// the beginning of the next line.  Clear the box first so the
	// separator and hint lines are removed.
	i.clearBox()
	fmt.Fprint(i.out, "\r\n")
}

// scrollToBottom is called before committing a line.
func (i *Input) scrollToBottom() {
	if i.screen != nil {
		i.screen.ScrollToBottom()
	}
}

// redrawContent redraws the current editing buffer.
func (i *Input) redrawContent(prompt string, ls *lineState) {
	if i.screen != nil {
		i.screen.RedrawInputContent(prompt, string(ls.buf), ls.cursor)
		return
	}
	// Inline mode: \r to column 1, erase line, reprint prompt + buffer.
	fmt.Fprintf(i.out, "\r\033[K%s%s", i.decoratePrompt(prompt), string(ls.buf))
	// Move cursor left to the correct position within the buffer.
	tailCols := visibleLen(string(ls.buf[ls.cursor:]))
	if tailCols > 0 {
		fmt.Fprintf(i.out, "\033[%dD", tailCols)
	}
}

// utf8ByteLen returns the total byte length of a UTF-8 sequence from its
// leading byte, or -1 if invalid.
func utf8ByteLen(b byte) int {
	switch {
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	}
	return -1
}

// ── scroll loop ───────────────────────────────────────────────────────────────

// RunScrollLoop waits while the AI is streaming and handles abort (Ctrl+C).
//
// In Screen mode it also handles scroll events (mouse wheel, Page Up/Down)
// and typeahead input.  In inline mode it simply blocks until done is closed;
// Ctrl+C is handled by the SIGINT handler in the caller.
func (i *Input) RunScrollLoop(done <-chan struct{}, abortFn func()) {
	if i.screen == nil {
		i.runInlineScrollLoop(done, abortFn)
		return
	}

	// ── Screen mode scroll loop (unchanged) ──────────────────────────────────
	fd := int(i.in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		<-done
		return
	}

	syscall.SetNonblock(fd, true)

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	byteCh := make(chan byte, 256)

	go func() {
		defer close(readerDone)
		buf := []byte{0}
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := syscall.Read(fd, buf)
			if n > 0 {
				select {
				case byteCh <- buf[0]:
				case <-stop:
					return
				}
			}
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if err != nil {
				return
			}
		}
	}()

	defer func() {
		close(stop)
		<-readerDone
		syscall.SetNonblock(fd, false)
		term.Restore(fd, oldState)
	}()

	readByteTimeout := func(ms int) (byte, bool) {
		select {
		case b := <-byteCh:
			return b, true
		case <-time.After(time.Duration(ms) * time.Millisecond):
			return 0, false
		case <-done:
			return 0, false
		}
	}

	readByteErr := func() (byte, error) {
		b, ok := readByteTimeout(200)
		if !ok {
			return 0, io.EOF
		}
		return b, nil
	}

	var ta []rune
	showTypeAhead := func() {
		if i.lastPrompt != "" {
			i.screen.RedrawInputContent(i.lastPrompt, string(ta), len(ta))
		}
	}

	for {
		select {
		case <-done:
			if len(ta) > 0 {
				i.typeAhead = ta
			}
			return
		case b := <-byteCh:
			switch b {
			case 3: // Ctrl+C
				if abortFn != nil {
					abortFn()
				}
				return
			case 18: // Ctrl+R
				if i.OnCtrlR != nil {
					i.OnCtrlR()
				}
			case 127, 8: // Backspace
				if len(ta) > 0 {
					ta = ta[:len(ta)-1]
					showTypeAhead()
				}
			case 27: // ESC
				seq1, ok := readByteTimeout(100)
				if !ok || seq1 != '[' {
					continue
				}
				seq2, ok := readByteTimeout(100)
				if !ok {
					continue
				}
				switch seq2 {
				case '<': // SGR mouse event
					i.handleSGRMouse(readByteErr)
				case '5': // Page Up
					readByteTimeout(100)
					i.screen.ScrollUp(i.screen.ContentBottom() / 2)
				case '6': // Page Down
					readByteTimeout(100)
					i.screen.ScrollDown(i.screen.ContentBottom() / 2)
				default:
					if seq2 >= '0' && seq2 <= '9' {
						readByteTimeout(100)
					}
				}
			default:
				if b >= 32 && b < 127 {
					ta = append(ta, rune(b))
					showTypeAhead()
				} else if b >= 0x80 {
					n := utf8ByteLen(b)
					if n > 1 {
						seq := make([]byte, n)
						seq[0] = b
						ok := true
						for k := 1; k < n; k++ {
							cb, hasMore := readByteTimeout(50)
							if !hasMore {
								ok = false
								break
							}
							seq[k] = cb
						}
						if ok {
							r, _ := utf8.DecodeRune(seq)
							if r != utf8.RuneError {
								ta = append(ta, r)
								showTypeAhead()
							}
						}
					}
				}
			}
		}
	}
}

// runInlineScrollLoop is the inline-mode implementation of RunScrollLoop.
// It puts stdin in raw mode, shows the prompt at the bottom (via OnPromptChange),
// buffers keystrokes as typeahead, and handles Ctrl+C abort.
func (i *Input) runInlineScrollLoop(done <-chan struct{}, abortFn func()) {
	if i.in == nil {
		<-done
		return
	}

	fd := int(i.in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		<-done
		return
	}

	syscall.SetNonblock(fd, true)

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	byteCh := make(chan byte, 256)

	go func() {
		defer close(readerDone)
		buf := []byte{0}
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := syscall.Read(fd, buf)
			if n > 0 {
				select {
				case byteCh <- buf[0]:
				case <-stop:
					return
				}
			}
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if err != nil {
				return
			}
		}
	}()

	defer func() {
		close(stop)
		<-readerDone
		syscall.SetNonblock(fd, false)
		term.Restore(fd, oldState)
	}()

	readByteTimeout := func(ms int) (byte, bool) {
		select {
		case b := <-byteCh:
			return b, true
		case <-time.After(time.Duration(ms) * time.Millisecond):
			return 0, false
		case <-done:
			return 0, false
		}
	}

	// Show prompt at the bottom (we're at line start after the previous doneLine).
	var ta []rune
	setPrompt := func() {
		if i.OnPromptChange != nil {
			i.OnPromptChange(i.lastPrompt + string(ta))
		}
	}
	clearPrompt := func() {
		if i.OnPromptChange != nil {
			i.OnPromptChange("")
		}
	}
	setPrompt()

	for {
		var approvalCh <-chan ApprovalRequest
		if i.ApprovalRequests != nil {
			approvalCh = i.ApprovalRequests
		}
		select {
		case <-done:
			clearPrompt()
			if len(ta) > 0 {
				i.typeAhead = ta
			}
			return

		case req := <-approvalCh:
			clearPrompt()
			i.printApproval(req, byteCh, done)
			setPrompt()

		case <-i.ResizeCh:
			// Terminal resized: repaint the prompt on the current line.
			setPrompt()

		case b := <-byteCh:
			switch b {
			case 3: // Ctrl+C – abort streaming.
				clearPrompt()
				if abortFn != nil {
					abortFn()
				}
				return

			case 18: // Ctrl+R – expand last tool call.
				if i.OnCtrlR != nil {
					clearPrompt()
					i.OnCtrlR()
					setPrompt()
				}

			case 127, 8: // Backspace – delete last typeahead character.
				if len(ta) > 0 {
					ta = ta[:len(ta)-1]
					setPrompt()
				}

			case 27: // ESC – consume escape sequences silently.
				seq1, ok := readByteTimeout(100)
				if !ok || seq1 != '[' {
					continue
				}
				seq2, ok := readByteTimeout(100)
				if !ok {
					continue
				}
				// Consume trailing ~ for numeric sequences (e.g. Page Up/Down).
				if seq2 >= '0' && seq2 <= '9' {
					readByteTimeout(100)
				}

			default:
				// Printable ASCII.
				if b >= 32 && b < 127 {
					ta = append(ta, rune(b))
					setPrompt()
				} else if b >= 0x80 {
					// Multi-byte UTF-8 (e.g. CJK input).
					n := utf8ByteLen(b)
					if n > 1 {
						seq := make([]byte, n)
						seq[0] = b
						ok := true
						for k := 1; k < n; k++ {
							cb, hasMore := readByteTimeout(50)
							if !hasMore {
								ok = false
								break
							}
							seq[k] = cb
						}
						if ok {
							r, _ := utf8.DecodeRune(seq)
							if r != utf8.RuneError {
								ta = append(ta, r)
								setPrompt()
							}
						}
					}
				}
			}
		}
	}
}

// handleSGRMouse parses and handles an SGR mouse event (Screen mode only).
func (i *Input) handleSGRMouse(readByte func() (byte, error)) {
	var buf strings.Builder
	var terminator byte
	for {
		c, err := readByte()
		if err != nil {
			return
		}
		if c == 'M' || c == 'm' {
			terminator = c
			break
		}
		buf.WriteByte(c)
	}
	_ = terminator

	parts := strings.SplitN(buf.String(), ";", 3)
	if len(parts) < 1 {
		return
	}
	button, err := strconv.Atoi(parts[0])
	if err != nil {
		return
	}

	const scrollLines = 3
	switch button {
	case 64:
		i.screen.ScrollUp(scrollLines)
	case 65:
		i.screen.ScrollDown(scrollLines)
	}
}

// printApproval shows an approval prompt and waits for the user to press a key.
// It reads from byteCh (already in raw mode) and sends the decision to req.Response.
func (i *Input) printApproval(req ApprovalRequest, byteCh <-chan byte, done <-chan struct{}) {
	noColor := i.noColor
	bold := func(s string) string { return styled(noColor, ansiBold, s) }
	yellow := func(s string) string { return styled(noColor, ansiYellow, s) }
	green := func(s string) string { return styled(noColor, ansiGreen, s) }
	red := func(s string) string { return styled(noColor, ansiRed, s) }
	dim := func(s string) string { return styled(noColor, ansiDim, s) }

	fmt.Fprintf(i.out, "\r\n%s %s\r\n",
		yellow("▶ Tool:"), bold(req.ToolName))
	fmt.Fprintf(i.out, "%s %s\r\n",
		dim("  Approve?"),
		dim("[y] yes  [a] always  [n] no  [d] deny-always"))

	var cancelCh <-chan struct{}
	if req.Cancel != nil {
		cancelCh = req.Cancel
	}

	decision := "deny"
	for {
		var b byte
		select {
		case b = <-byteCh:
		case <-done:
			req.Response <- decision
			return
		case <-cancelCh:
			// Decision was made remotely (e.g. via Telegram); dismiss the prompt.
			fmt.Fprintf(i.out, "\r%s\r\n", dim("  → decided remotely"))
			return
		}
		switch b {
		case 'y', 'Y':
			decision = "allow"
			fmt.Fprintf(i.out, "\r%s\r\n", green("✓ Allowed"))
			req.Response <- decision
			return
		case 'a', 'A':
			decision = "allow_always"
			fmt.Fprintf(i.out, "\r%s\r\n", green("✓ Always allowed"))
			req.Response <- decision
			return
		case 'n', 'N', 27: // n or ESC
			decision = "deny"
			fmt.Fprintf(i.out, "\r%s\r\n", red("✗ Denied"))
			req.Response <- decision
			return
		case 'd', 'D':
			decision = "deny_always"
			fmt.Fprintf(i.out, "\r%s\r\n", red("✗ Always denied"))
			req.Response <- decision
			return
		case 3: // Ctrl+C → deny
			decision = "deny"
			fmt.Fprintf(i.out, "\r%s\r\n", red("✗ Denied"))
			req.Response <- decision
			return
		}
	}
}
