package tui

import (
	"fmt"
	"io"
	"os"
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

	readByte := func() (byte, error) {
		b := make([]byte, 1)
		for {
			n, err := i.in.Read(b)
			if n > 0 {
				return b[0], nil
			}
			if err != nil {
				return 0, err
			}
		}
	}

	for {
		b, err := readByte()
		if err != nil {
			i.doneLine()
			return "", io.EOF
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
					// Inline mode: move past the current prompt line, run the
					// handler, then reprint the prompt and current buffer.
					// Use \r\n (not \n) because raw mode disables ONLCR.
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
func (i *Input) initLine(prompt string) {
	if i.screen != nil {
		i.screen.InitInputLine(prompt)
	} else {
		fmt.Fprint(i.out, prompt)
	}
}

// doneLine is called when a line is committed (Enter / Ctrl+C / EOF).
func (i *Input) doneLine() {
	if i.screen != nil {
		i.screen.AfterReadLine()
	} else {
		// In raw mode \n is a bare line-feed (no carriage return), so the
		// cursor would stay in the middle of the line.  Use \r\n to go to
		// the beginning of the next line.
		fmt.Fprint(i.out, "\r\n")
	}
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
	fmt.Fprintf(i.out, "\r\033[K%s%s", prompt, string(ls.buf))
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
		select {
		case <-done:
			clearPrompt()
			if len(ta) > 0 {
				i.typeAhead = ta
			}
			return

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
