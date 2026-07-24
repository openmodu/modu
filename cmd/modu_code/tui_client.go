package main

import (
	tea "charm.land/bubbletea/v2"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

// newModuTUIClient is the only adapter from modu_code's Bubble Tea runtime to
// the business-facing TUI client.
func newModuTUIClient(send func(tea.Msg)) modutui.Client {
	if send == nil {
		return modutui.Client{}
	}
	return modutui.NewClient(func(message any) {
		send(message)
	})
}
