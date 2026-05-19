package tui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/slash"
)

// submit dispatches a submitted draft. Three forms:
//   - "! cmd"  → run shell, print output to scrollback
//   - "/cmd"   → built-in slash command via pkg/slash
//   - anything else → send to the agent as a prompt
func (r *goTUIRoot) submit(text string) {
	line := strings.TrimSpace(text)
	if line == "" {
		return
	}
	r.draft.Set("")
	r.cursor = 0
	r.appendHistory(line)

	if rest, ok := strings.CutPrefix(line, "! "); ok {
		r.runShell(rest)
		return
	}
	if strings.HasPrefix(line, "/") {
		if line == "/retry" {
			r.retryLastFailedPrompt()
			return
		}
		if line == "/model" {
			r.openModelSelect()
			return
		}
		if line == "/sessions" {
			r.openSessionSelect(false)
			return
		}
		if line == "/sessions all" {
			r.openSessionSelect(true)
			return
		}
		r.runSlash(line)
		return
	}
	r.runPrompt(line)
}

// togglePlanMode flips plan mode on/off. Bound to Shift+Tab so the user can
// switch into planning before describing a task (mirrors Claude Code). Only
// acts while the input box is active — toggling mid-query or mid-approval is
// ignored to avoid racing the running turn.
func (r *goTUIRoot) togglePlanMode() {
	if r.session == nil || r.model.state != uiStateInput {
		return
	}
	if r.session.IsPlanMode() {
		r.session.ExitPlanMode("manually exited via shift+tab", nil)
		r.model.statusMsg = "plan mode off"
	} else {
		r.session.EnterPlanMode()
		r.model.statusMsg = "plan mode on — describe the task; I'll plan first"
	}
	r.bump()
}

func (r *goTUIRoot) runShell(shellCmd string) {
	block := uiBlock{Kind: "system", Content: "$ " + shellCmd, Timestamp: time.Now()}
	r.model.appendBlock(block)
	r.pushBlockAbove(block)
	go func() {
		out, err := exec.Command("bash", "-c", shellCmd).CombinedOutput()
		r.queue(func() {
			text := strings.TrimSpace(string(out))
			if err != nil {
				if text != "" {
					text += "\n"
				}
				text += err.Error()
			}
			b := uiBlock{Kind: "system", Content: text, Timestamp: time.Now()}
			r.model.appendBlock(b)
			r.pushBlockAbove(b)
			r.bump()
		})
	}()
}

func (r *goTUIRoot) runSlash(line string) {
	go func() {
		printer := &uiSlashPrinter{}
		handled, exit := slash.Handle(r.ctx, line, r.session, printer, r.modelInfo)
		if !handled && r.isDynamicAgentSlash(line) {
			// Not a built-in slash command, but a known skill or prompt
			// template — delegate to the session for expansion/execution.
			r.queue(func() {
				r.runPrompt(line)
			})
			return
		}
		r.queue(func() {
			switch {
			case !handled:
				b := uiBlock{Kind: "system", Content: "unknown command: " + line, Timestamp: time.Now()}
				r.model.appendBlock(b)
				r.pushBlockAbove(b)
			case printer.clear:
				r.model.blocks = nil
			case strings.TrimSpace(strings.Join(printer.lines, "\n")) != "":
				b := uiBlock{Kind: "section", Title: "modu_code", Content: strings.Join(printer.lines, "\n"), Timestamp: time.Now()}
				r.model.appendBlock(b)
				r.pushBlockAbove(b)
			}
			r.bump()
			if exit && r.app != nil {
				r.app.Stop()
			}
		})
	}()
}

// isDynamicAgentSlash reports whether line is `/<name>[ args]` where <name>
// matches a discovered skill or prompt template.
func (r *goTUIRoot) isDynamicAgentSlash(line string) bool {
	cmd := strings.TrimPrefix(strings.TrimSpace(line), "/")
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		cmd = cmd[:i]
	}
	if cmd == "" || r.session == nil {
		return false
	}
	for _, s := range r.session.GetSkills() {
		if s.Name == cmd {
			return true
		}
	}
	for _, p := range r.session.GetPromptTemplates() {
		if p.Name == cmd {
			return true
		}
	}
	return false
}

// skillSlashCommands snapshots the current dynamic resources into
// slash-suggestion entries so /<name> auto-completes alongside the built-ins.
func (r *goTUIRoot) skillSlashCommands() []slashCommandDef {
	if r.session == nil {
		return nil
	}
	list := r.session.GetSkills()
	prompts := r.session.GetPromptTemplates()
	if len(list) == 0 && len(prompts) == 0 {
		return nil
	}
	out := make([]slashCommandDef, 0, len(list)+len(prompts))
	for _, s := range list {
		out = append(out, slashCommandDef{
			Name:        "/" + s.Name,
			Description: s.Description,
		})
	}
	for _, p := range prompts {
		out = append(out, slashCommandDef{
			Name:        "/" + p.Name,
			Description: p.Description,
		})
	}
	return out
}

func (r *goTUIRoot) runPrompt(line string) {
	block := uiBlock{Kind: "user", Content: line, Timestamp: time.Now()}
	r.model.appendBlock(block)
	r.pushBlockAbove(block)
	r.model.queryActive = true
	r.model.state = uiStateQuerying
	r.model.statusMsg = "thinking"
	r.model.lastActivity = ""
	r.model.queryStartTime = time.Now()
	r.model.thinkingStart = time.Time{}
	queryCtx, queryCancel := context.WithCancel(r.ctx)
	r.model.queryCancel = queryCancel
	r.bump()

	go func() {
		defer queryCancel()
		r.promptMu.Lock()
		defer r.promptMu.Unlock()
		// Bail if the context was cancelled while waiting for the lock.
		select {
		case <-queryCtx.Done():
			return
		default:
		}
		err := r.session.Prompt(queryCtx, line)
		r.queue(func() {
			if err != nil && err != context.Canceled {
				r.lastFailedPrompt = line
				r.model.setPromptError(err)
			} else if err == nil {
				r.lastFailedPrompt = ""
				r.model.clearPromptError()
			}
			r.model.finishActivity(err)
			r.model.queryActive = false
			if r.model.statusMsg != "interrupted" {
				r.model.statusMsg = ""
			}
			r.model.state = uiStateInput
			r.bump()
		})
	}()
}

func (r *goTUIRoot) retryLastFailedPrompt() {
	prompt := strings.TrimSpace(r.lastFailedPrompt)
	if prompt == "" {
		r.model.statusMsg = "no failed prompt to retry"
		r.bump()
		return
	}
	r.model.statusMsg = "retrying last prompt"
	r.runPrompt(prompt)
}
