package tui

import (
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
)

// handleAgentEvent forwards the event to the model layer for state updates,
// then mirrors specific transitions onto the TUI: assistant blocks and
// completed tool blocks are pushed to scrollback once each.
func (r *goTUIRoot) handleAgentEvent(ev agent.AgentEvent) {
	r.model.handleAgentEvent(ev)

	switch ev.Type {
	case agent.EventTypeAgentEnd:
		// Model already cleared queryActive; reset UI state so the working
		// indicator stops before session.Prompt() returns.
		r.model.state = uiStateInput

	case agent.EventTypeMessageEnd:
		// Push the completed assistant block to scrollback (at most once per block).
		for i := len(r.model.blocks) - 1; i >= 0; i-- {
			if r.model.blocks[i].Kind == "assistant" {
				if !r.model.blocks[i].pushed {
					r.model.blocks[i].pushed = true
					r.pushBlockAbove(r.model.blocks[i])
				}
				break
			}
		}
	case agent.EventTypeToolExecutionEnd:
		// Push the specific completed tool by ToolCallID only — name-based
		// matching would re-print already-flushed tools sharing the same name.
		for i := len(r.model.blocks) - 1; i >= 0; i-- {
			if r.model.blocks[i].Kind != "tool" {
				continue
			}
			for _, tool := range r.model.blocks[i].Tools {
				var matched bool
				if ev.ToolCallID != "" {
					matched = tool.ID == ev.ToolCallID
				} else {
					matched = tool.Name == ev.ToolName && (tool.Status == "done" || tool.Status == "error")
				}
				if matched {
					s := strings.TrimRight(renderUITool(tool, r.model.transcriptMode, r.model.viewportContentWidth()), "\n")
					if strings.TrimSpace(stripANSIForGoTUI(s)) != "" {
						r.printAbove(s)
					}
					break
				}
			}
			break
		}
	}
	r.bump()
}

func (r *goTUIRoot) handleSessionEvent(ev coding_agent.SessionEvent) {
	switch ev.Type {
	case coding_agent.SessionEventModelChange:
		if r.session != nil {
			r.model.model = r.session.GetModel()
			r.modelInfo = r.model.model
		}
		r.model.statusMsg = "model changed; context cleared"
	case coding_agent.SessionEventCwdChanged:
		r.model.statusMsg = "cwd changed"
	case coding_agent.SessionEventCompactionStart:
		r.model.statusMsg = "compacting"
	case coding_agent.SessionEventCompactionDone:
		r.model.statusMsg = "compacted"
	case coding_agent.SessionEventWorktreeCreate:
		r.model.statusMsg = "worktree"
	case coding_agent.SessionEventWorktreeRemove:
		r.model.statusMsg = "worktree closed"
	case coding_agent.SessionEventSubagentStart:
		r.model.statusMsg = "subagent started"
	case coding_agent.SessionEventSubagentStop:
		r.model.statusMsg = "subagent stopped"
	}
	r.bump()
}

// externalInfo / externalUser are entry points used by channel bridges
// (e.g. the Telegram bot) to inject content into the conversation.

func (r *goTUIRoot) externalInfo(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	r.queue(func() {
		b := uiBlock{Kind: "section", Title: "modu_code", Content: text, Timestamp: time.Now()}
		r.model.appendBlock(b)
		r.pushBlockAbove(b)
		r.bump()
	})
}

func (r *goTUIRoot) externalUser(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	r.queue(func() {
		b := uiBlock{Kind: "user", Content: text, Timestamp: time.Now()}
		r.model.appendBlock(b)
		r.pushBlockAbove(b)
		r.bump()
	})
}

// pushBlockAbove renders a block and prints it to the inline scrollback.
// Must be called from the main event loop.
func (r *goTUIRoot) pushBlockAbove(block uiBlock) {
	if r.app == nil {
		return
	}
	s := r.model.renderSingleBlock(block)
	if strings.TrimSpace(stripANSIForGoTUI(s)) == "" {
		return
	}
	r.app.PrintAboveStyledln("%s", s)
	r.app.PrintAboveln("") // blank line between blocks
}

// printAbove prints a pre-rendered ANSI string to the inline scrollback.
func (r *goTUIRoot) printAbove(s string) {
	if r.app == nil {
		return
	}
	r.app.PrintAboveStyledln("%s", s)
	r.app.PrintAboveln("") // blank line after tool output
}
