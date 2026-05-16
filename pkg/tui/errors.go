package tui

import (
	"fmt"
	"strings"
)

const maxInlineErrorChars = 180

func (m *uiModel) setPromptError(err error) {
	if err == nil {
		m.clearPromptError()
		return
	}
	text := compactInlineError(err.Error())
	if text == m.lastErrText {
		m.errRepeat++
	} else {
		m.lastErrText = text
		m.errRepeat = 1
	}
	msg := text
	if m.errRepeat > 1 {
		msg = fmt.Sprintf("%s (repeated %dx)", text, m.errRepeat)
	}
	m.errMsg = msg + " | try: /retry, /model, /doctor, ctrl+c"
}

func (m *uiModel) clearPromptError() {
	m.errMsg = ""
	m.lastErrText = ""
	m.errRepeat = 0
}

func compactInlineError(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= maxInlineErrorChars {
		return text
	}
	if maxInlineErrorChars <= 3 {
		return text[:maxInlineErrorChars]
	}
	return text[:maxInlineErrorChars-3] + "..."
}
