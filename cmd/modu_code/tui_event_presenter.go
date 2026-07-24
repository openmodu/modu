package main

import (
	codetui "github.com/openmodu/modu/cmd/modu_code/internal/tui"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func newModuTUIEventPresenter() codetui.EventPresenter {
	return codetui.NewEventPresenter(moduTUIToolPresenter{}, moduTUIContextCompactDivider)
}

func moduTUITranscriptEntries(session *coding_agent.CodingSession, presenter codetui.EventPresenter) []modutui.Entry {
	if session == nil {
		return nil
	}
	messages := session.GetMessages()
	nodes := session.GetSessionTreeNodes()
	if len(nodes) == 0 {
		return presenter.AgentMessages(messages, session.Cwd())
	}
	out := make([]modutui.Entry, 0, len(messages))
	messageIndex := 0
	for _, node := range nodes {
		if !node.InCurrentPath {
			continue
		}
		switch node.Type {
		case "message":
			if messageIndex >= len(messages) {
				continue
			}
			out = append(out, presenter.AgentMessage(messages[messageIndex], session.Cwd())...)
			messageIndex++
		case "compaction":
			out = append(out, presenter.ContextCompactEntry())
		}
	}
	for messageIndex < len(messages) {
		out = append(out, presenter.AgentMessage(messages[messageIndex], session.Cwd())...)
		messageIndex++
	}
	return out
}
