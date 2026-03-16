package tui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Input provides line-based terminal input with an in-memory history ring.
type Input struct {
	reader  *bufio.Reader
	out     io.Writer
	screen  *Screen // optional; if set, prompt is drawn on the screen's input line
	history []string
	maxHist int
	noColor bool
}

// NewInput creates an Input reading from in and writing the prompt to out.
func NewInput(in io.Reader, out io.Writer) *Input {
	return &Input{
		reader:  bufio.NewReader(in),
		out:     out,
		maxHist: 200,
		noColor: shouldDisableColor(out),
	}
}

// NewInputWithScreen creates an Input that uses the Screen's input line for the prompt.
func NewInputWithScreen(in io.Reader, s *Screen) *Input {
	return &Input{
		reader:  bufio.NewReader(in),
		out:     s.out,
		screen:  s,
		maxHist: 200,
		noColor: s.noColor,
	}
}

// ReadLine displays prompt and returns the next line of input.
// Returns ("", io.EOF) on end-of-input (Ctrl+D).
func (i *Input) ReadLine(prompt string) (string, error) {
	styledPrompt := styled(i.noColor, ansiBold+ansiGreen, prompt)

	if i.screen != nil {
		i.screen.InitInputLine(styledPrompt)
	} else {
		fmt.Fprint(i.out, styledPrompt)
	}

	line, err := i.reader.ReadString('\n')

	if i.screen != nil {
		i.screen.AfterReadLine()
	}

	if err != nil {
		if err == io.EOF && line != "" {
			line = strings.TrimRight(line, "\r\n")
			i.addHistory(line)
			return line, nil
		}
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
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
