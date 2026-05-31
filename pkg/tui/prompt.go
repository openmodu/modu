package tui

import (
	"context"
	"errors"
	"strings"
)

type submitMode int

const (
	submitModeNormal submitMode = iota
	submitModeSteer
)

func queueCommandArg(line string, names ...string) (string, bool) {
	for _, name := range names {
		if line == name {
			return "", true
		}
		if strings.HasPrefix(line, name+" ") {
			return strings.TrimSpace(strings.TrimPrefix(line, name)), true
		}
	}
	return "", false
}
func formatShellResult(out []byte, err error) string {
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			text += "\n"
		}
		text += err.Error()
	}
	if text == "" {
		return "(no output)"
	}
	return text
}

func formatShellPrompt(shellCmd, output string) string {
	return "$ " + shellCmd + "\n" + output
}
func queueStateForPromptError(err error, steeringCancel bool) string {
	if err == nil || steeringCancel {
		return "done"
	}
	if errors.Is(err, context.Canceled) {
		return "interrupted"
	}
	return "failed"
}
