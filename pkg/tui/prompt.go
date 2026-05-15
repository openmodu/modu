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
		r.runSlash(line)
		return
	}
	r.runPrompt(line)
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
		if !handled && r.isSkillSlash(line) {
			// Not a built-in slash command, but a known skill — delegate to the
			// session, which has its own /skill-name → executeSkill dispatch.
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

// isSkillSlash reports whether line is `/<name>[ args]` where <name> matches
// a discovered skill. Skill names are case-sensitive (mirrors what the session
// does — built-in slash commands lowercase, but skill names from frontmatter
// are taken as-is).
func (r *goTUIRoot) isSkillSlash(line string) bool {
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
	return false
}

// skillSlashCommands snapshots the current skills into slash-suggestion entries
// so /<skill-name> auto-completes alongside the built-ins.
func (r *goTUIRoot) skillSlashCommands() []slashCommandDef {
	if r.session == nil {
		return nil
	}
	list := r.session.GetSkills()
	if len(list) == 0 {
		return nil
	}
	out := make([]slashCommandDef, 0, len(list))
	for _, s := range list {
		out = append(out, slashCommandDef{
			Name:        "/" + s.Name,
			Description: s.Description,
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
				r.model.errMsg = err.Error()
			}
			r.model.queryActive = false
			if r.model.statusMsg != "interrupted" {
				r.model.statusMsg = ""
			}
			r.model.state = uiStateInput
			r.bump()
		})
	}()
}
