// Package tui provides terminal UI components for building CLI AI applications.
// It renders agent events using ANSI escape codes with no external dependencies.
package tui

import (
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// ANSI escape code constants.
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiItalic = "\033[3m"

	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiBlue    = "\033[34m"
	ansiMagenta = "\033[35m"
	ansiCyan    = "\033[36m"

	ansiBrightBlack  = "\033[90m"
	ansiBrightGreen  = "\033[92m"
	ansiBrightYellow = "\033[93m"
	ansiBrightBlue   = "\033[94m"
	ansiBrightCyan   = "\033[96m"
	ansiBrightWhite  = "\033[97m"

	// Cursor and screen control.
	ansiSaveCursor    = "\033[s"
	ansiRestoreCursor = "\033[u"
	ansiHideCursor    = "\033[?25l"
	ansiShowCursor    = "\033[?25h"
	ansiAltScreenOn   = "\033[?1049h"
	ansiAltScreenOff  = "\033[?1049l"
	ansiEraseLine     = "\033[2K"
	ansiEraseDown     = "\033[J"
)

// styled wraps text with ANSI codes. Returns plain text if noColor is true.
func styled(noColor bool, codes string, text string) string {
	if noColor || text == "" {
		return text
	}
	return codes + text + ansiReset
}

// shouldDisableColor returns true when colors should be suppressed.
func shouldDisableColor(w io.Writer) bool {
	// Respect the NO_COLOR standard (https://no-color.org/).
	if os.Getenv("NO_COLOR") != "" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return true
	}
	// Disable color when writing to a non-terminal.
	f, ok := w.(*os.File)
	if !ok {
		return true
	}
	return !isTerminalFd(uintptr(f.Fd()))
}

// isTerminalFd returns true if fd is a terminal.
func isTerminalFd(fd uintptr) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}

// winsize holds the terminal dimensions from TIOCGWINSZ.
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// termSize returns (width, height) of the terminal.
func termSize() (int, int) {
	var ws winsize
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		os.Stdout.Fd(), syscall.TIOCGWINSZ,
		uintptr(unsafe.Pointer(&ws))); errno == 0 {
		w, h := int(ws.Col), int(ws.Row)
		if w <= 0 {
			w = 80
		}
		if h <= 0 {
			h = 24
		}
		return w, h
	}
	w, h := 80, 24
	if col := os.Getenv("COLUMNS"); col != "" {
		if n, err := strconv.Atoi(col); err == nil && n > 0 {
			w = n
		}
	}
	if lines := os.Getenv("LINES"); lines != "" {
		if n, err := strconv.Atoi(lines); err == nil && n > 0 {
			h = n
		}
	}
	return w, h
}

// termWidth returns the current terminal width.
func termWidth() int {
	w, _ := termSize()
	return w
}

// termHeight returns the current terminal height.
func termHeight() int {
	_, h := termSize()
	return h
}

// separator returns a horizontal line of dashes.
func separator(noColor bool, width int) string {
	line := strings.Repeat("─", width)
	return styled(noColor, ansiBrightBlack, line)
}
