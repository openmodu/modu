package tui

import (
	"strings"
	"time"
)

func (r *goTUIRoot) appendSystemSection(title, content string) {
	block := uiBlock{Kind: "section", Title: title, Content: content, Timestamp: time.Now()}
	r.model.appendBlock(block)
	r.pushBlockAbove(block)
	r.bump()
}

func (r *goTUIRoot) runNewSessionCommand() {
	if r.session == nil {
		return
	}
	if err := r.session.ClearConversation(); err != nil {
		r.model.setPromptError(err)
	} else {
		r.model.blocks = nil
		r.model.setTransientStatus("new session")
	}
	r.bump()
}

func (r *goTUIRoot) runCloneCommand() {
	if r.session == nil {
		return
	}
	leafID := r.session.GetSessionLeafID()
	if leafID == "" {
		r.model.setTransientStatus("nothing to clone")
		r.bump()
		return
	}
	if _, err := r.session.CreateBranchedSession(leafID); err != nil {
		r.model.setPromptError(err)
	} else {
		r.model.setTransientStatus("cloned session")
	}
	r.bump()
}

func (r *goTUIRoot) runReloadCommand() {
	if r.session == nil {
		return
	}
	r.session.ReloadResources()
	r.model.setTransientStatus("reloaded resources")
	r.bump()
}

func (r *goTUIRoot) runNameCommand(name string) {
	if r.session == nil {
		return
	}
	if strings.TrimSpace(name) == "" {
		current := r.session.GetSessionName()
		if current == "" {
			current = "(unnamed)"
		}
		r.appendSystemSection("modu_code", "session name: "+current)
		return
	}
	r.session.SetSessionName(name)
	r.model.setTransientStatus("session name: " + name)
	r.bump()
}

func (r *goTUIRoot) showHotkeys() {
	r.appendSystemSection("Hotkeys", hotkeyHelpText())
}

func hotkeyHelpText() string {
	lines := []string{
		"Navigation",
		"  Up/Down: move cursor, history, or selector",
		"  Home/End: line start/end or selector start/end",
		"  PageUp/PageDown: scroll transcript or selector page",
		"  Esc: close selector",
		"",
		"Editing",
		"  Enter: submit",
		"  Ctrl+J: newline",
		"  Tab: autocomplete or selector scope",
		"  @file: fuzzy file reference; Tab/Enter completes",
		"  !cmd: run shell and send output; !!cmd display only",
		"",
		"App",
		"  Ctrl+C: interrupt or exit",
		"  Ctrl+D: exit when input is empty",
		"  Ctrl+L: clear screen",
		"  Ctrl+O: expand/collapse tool output",
		"  Ctrl+P/Ctrl+N: cycle models",
		"  Shift+Tab: toggle plan mode",
		"  Tree: Ctrl+F branch-session, Ctrl+S summary",
		"",
		"Commands",
		"  /settings, /config, /model, /scoped-models, /sessions",
		"  /tree, /fork, /clone, /skills, /prompts",
		"  /export, /copy, /changelog",
	}
	return strings.Join(lines, "\n")
}
