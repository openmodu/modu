package tui

import (
	"fmt"
	"strings"
)

// goTUIBridgePrinter adapts the goTUIRoot into the channel-bridge Printer
// interface used by the Telegram bot and similar adapters. It funnels output
// back into the TUI through the externalInfo/externalUser entry points so
// content shows up in the conversation alongside locally typed input.
type goTUIBridgePrinter struct {
	root *goTUIRoot
}

func (p *goTUIBridgePrinter) PrintInfo(msg string) {
	if p.root != nil {
		p.root.externalInfo(msg)
	}
}

func (p *goTUIBridgePrinter) PrintError(err error) {
	if err != nil && p.root != nil {
		p.root.externalInfo("error: " + err.Error())
	}
}

func (p *goTUIBridgePrinter) PrintUser(msg string) {
	if p.root != nil {
		p.root.externalUser(msg)
	}
}

func (p *goTUIBridgePrinter) ClearLine() {}

func (p *goTUIBridgePrinter) PrintSection(title string, lines []string) {
	if p.root != nil {
		p.root.externalInfo(fmt.Sprintf("%s\n%s", title, strings.Join(lines, "\n")))
	}
}

func (p *goTUIBridgePrinter) ClearScreen() {
	if p.root != nil {
		p.root.queue(func() {
			p.root.model.blocks = nil
			p.root.bump()
		})
	}
}
