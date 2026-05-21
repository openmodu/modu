package tui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/slash"
)

// submit dispatches a submitted draft. Three forms:
//   - "!cmd"   → run shell, print output, then send command/output to the model
//   - "!!cmd"  → run shell and print output without sending it to the model
//   - "/cmd"   → built-in slash command via pkg/slash
//   - anything else → send to the agent as a prompt
func (r *goTUIRoot) submit(text string) {
	r.submitWithMode(text, submitModeNormal)
}

func (r *goTUIRoot) submitSteer(text string) {
	r.submitWithMode(text, submitModeSteer)
}

type submitMode int

const (
	submitModeNormal submitMode = iota
	submitModeSteer
)

func (r *goTUIRoot) submitWithMode(text string, mode submitMode) {
	line := strings.TrimSpace(text)
	if line == "" {
		return
	}
	r.draft.Set("")
	r.cursor = 0
	r.slashMatches = nil
	r.fileMatches = nil
	r.appendHistory(line)

	if line == "/steer" || strings.HasPrefix(line, "/steer ") {
		r.submitQueueCommand("steer", strings.TrimSpace(strings.TrimPrefix(line, "/steer")))
		return
	}
	if line == "/followup" || strings.HasPrefix(line, "/followup ") {
		r.submitQueueCommand("followup", strings.TrimSpace(strings.TrimPrefix(line, "/followup")))
		return
	}
	if rest, ok := strings.CutPrefix(line, "!!"); ok {
		r.runShell(strings.TrimSpace(rest), false)
		return
	}
	if rest, ok := strings.CutPrefix(line, "!"); ok {
		r.runShell(strings.TrimSpace(rest), true)
		return
	}
	if strings.HasPrefix(line, "/") {
		if line == "/retry" {
			r.retryLastFailedPrompt()
			return
		}
		if line == "/config" || strings.HasPrefix(line, "/config ") {
			r.runConfigHook(strings.TrimSpace(strings.TrimPrefix(line, "/config")))
			return
		}
		if line == "/settings" {
			r.openSettingsSelect()
			return
		}
		if line == "/model" || strings.HasPrefix(line, "/model ") {
			r.openModelSelect(strings.TrimSpace(strings.TrimPrefix(line, "/model")))
			return
		}
		if line == "/scoped-models" {
			r.openScopedModelsSelect()
			return
		}
		if line == "/sessions" || line == "/resume" {
			r.openSessionSelect(false)
			return
		}
		if line == "/sessions all" || line == "/resume all" {
			r.openSessionSelect(true)
			return
		}
		if line == "/tree" || line == "/fork" {
			r.openTreeSelect()
			return
		}
		if line == "/skills" {
			r.openResourceSelect("skills")
			return
		}
		if line == "/prompts" {
			r.openResourceSelect("prompts")
			return
		}
		if line == "/new" {
			r.runNewSessionCommand()
			return
		}
		if line == "/clone" {
			r.runCloneCommand()
			return
		}
		if line == "/hotkeys" {
			r.showHotkeys()
			return
		}
		if line == "/plan" {
			r.showPlanPanel()
			return
		}
		if line == "/worktree" {
			r.showWorktreePanel()
			return
		}
		if line == "/reload" {
			r.runReloadCommand()
			return
		}
		if line == "/name" || strings.HasPrefix(line, "/name ") {
			r.runNameCommand(strings.TrimSpace(strings.TrimPrefix(line, "/name")))
			return
		}
		r.runSlash(line)
		return
	}
	if r.model.queryActive {
		if mode == submitModeSteer {
			r.queueSteer(line)
			return
		}
		r.queueFollowUp(line)
		return
	}
	r.runPrompt(r.expandFileReferencesForPrompt(line))
}

func (r *goTUIRoot) submitQueueCommand(kind, text string) {
	if text == "" {
		r.model.setTransientStatus("/" + kind + " requires a message")
		r.bump()
		return
	}
	if !r.model.queryActive {
		r.model.setTransientStatus("no active task to " + kind)
		r.bump()
		return
	}
	if kind == "steer" {
		r.queueSteer(text)
		return
	}
	r.queueFollowUp(text)
}

func (r *goTUIRoot) queueFollowUp(line string) {
	if r.session == nil {
		r.model.setTransientStatus("session is not available")
		r.bump()
		return
	}
	expanded := r.expandFileReferencesForPrompt(line)
	r.session.FollowUp(expanded)
	r.appendQueuedUserBlock("followup", expanded)
	r.model.setStatus("queued follow-up")
	r.bump()
}

func (r *goTUIRoot) queueSteer(line string) {
	if r.session == nil {
		r.model.setTransientStatus("session is not available")
		r.bump()
		return
	}
	expanded := r.expandFileReferencesForPrompt(line)
	r.session.Steer(expanded)
	r.appendQueuedUserBlock("steer", expanded)
	r.continueQueuedAfterCancel = true
	if r.model.queryCancel != nil {
		r.model.queryCancel()
	}
	r.session.Abort()
	r.session.AbortBash()
	r.model.setStatus("steering")
	r.bump()
}

func (r *goTUIRoot) appendQueuedUserBlock(source, content string) {
	block := uiBlock{Kind: "user", Content: content, Source: source, Timestamp: time.Now()}
	r.model.appendBlock(block)
	r.pushBlockAbove(block)
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
		r.model.setTransientStatus("plan mode off")
	} else {
		r.session.EnterPlanMode()
		r.model.setTransientStatus("plan mode on — describe the task; I'll plan first")
	}
	r.bump()
}

func (r *goTUIRoot) runShell(shellCmd string, sendToModel bool) {
	if shellCmd == "" {
		r.model.setTransientStatus("shell command is empty")
		r.bump()
		return
	}
	block := uiBlock{Kind: "system", Content: "$ " + shellCmd, Timestamp: time.Now()}
	r.model.appendBlock(block)
	r.pushBlockAbove(block)
	go func() {
		cmd := exec.Command("bash", "-c", shellCmd)
		if cwd := r.currentWorkingDir(); cwd != "" {
			cmd.Dir = cwd
		}
		out, err := cmd.CombinedOutput()
		r.queue(func() {
			text := formatShellResult(out, err)
			b := uiBlock{Kind: "system", Content: text, Timestamp: time.Now()}
			r.model.appendBlock(b)
			r.pushBlockAbove(b)
			r.bump()
			if sendToModel {
				r.runPrompt(formatShellPrompt(shellCmd, text))
			}
		})
	}()
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

func (r *goTUIRoot) runConfigHook(args string) {
	if r.commandHooks.Config == nil {
		r.model.setTransientStatus("config command is not available")
		r.bump()
		return
	}
	go func() {
		out, err := r.commandHooks.Config(args)
		r.queue(func() {
			content := strings.TrimSpace(out)
			if err != nil {
				if content != "" {
					content += "\n"
				}
				content += "error: " + err.Error()
			}
			if content == "" {
				content = "config command completed"
			}
			block := uiBlock{Kind: "section", Title: "Config", Content: content, Timestamp: time.Now()}
			r.model.appendBlock(block)
			r.pushBlockAbove(block)
			r.bump()
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
	block := uiBlock{Kind: "user", Content: line, Source: "local", Timestamp: time.Now()}
	r.model.appendBlock(block)
	r.pushBlockAbove(block)
	r.runPromptOperation(line, func(ctx context.Context) error {
		return r.session.Prompt(ctx, line)
	})
}

func (r *goTUIRoot) runPromptOperation(failedPrompt string, run func(context.Context) error) {
	r.model.queryActive = true
	r.model.state = uiStateQuerying
	r.model.setStatus("thinking")
	r.model.clearActivity()
	r.model.queryStartTime = time.Now()
	r.model.thinkingStart = time.Time{}
	queryCtx, queryCancel := context.WithCancel(r.ctx)
	r.model.queryCancel = queryCancel
	r.bump()

	go func() {
		defer queryCancel()
		if r.promptMu != nil {
			r.promptMu.Lock()
			defer r.promptMu.Unlock()
		}
		// Bail if the context was cancelled while waiting for the lock.
		select {
		case <-queryCtx.Done():
			r.queue(func() {
				r.finishPromptOperation(queryCtx.Err(), failedPrompt)
			})
			return
		default:
		}
		err := run(queryCtx)
		r.queue(func() {
			r.finishPromptOperation(err, failedPrompt)
		})
	}()
}

func (r *goTUIRoot) continueQueuedPrompt() bool {
	if r.session == nil {
		return false
	}
	ag := r.session.GetAgent()
	if ag == nil || !ag.HasQueuedMessages() {
		return false
	}
	r.runPromptOperation("", func(ctx context.Context) error {
		return ag.Continue(ctx)
	})
	return true
}

func (r *goTUIRoot) finishPromptOperation(err error, failedPrompt string) {
	steeringCancel := err == context.Canceled && r.continueQueuedAfterCancel
	shouldContinue := r.session != nil &&
		r.session.GetAgent() != nil &&
		r.session.GetAgent().HasQueuedMessages() &&
		(err == nil || steeringCancel)
	r.continueQueuedAfterCancel = false
	if shouldContinue && r.continueQueuedPrompt() {
		return
	}
	if err != nil && err != context.Canceled {
		if failedPrompt != "" {
			r.lastFailedPrompt = failedPrompt
		}
		r.model.setPromptError(err)
	} else if err == nil || steeringCancel {
		r.lastFailedPrompt = ""
		r.model.clearPromptError()
	}
	finishErr := err
	if steeringCancel {
		finishErr = nil
	}
	r.model.finishActivity(finishErr)
	r.model.queryActive = false
	if r.model.statusMsg != "interrupted" {
		r.model.setStatus("")
	}
	r.model.state = uiStateInput
	r.bump()
}

func (r *goTUIRoot) retryLastFailedPrompt() {
	prompt := strings.TrimSpace(r.lastFailedPrompt)
	if prompt == "" {
		r.model.setTransientStatus("no failed prompt to retry")
		r.bump()
		return
	}
	r.model.setTransientStatus("retrying last prompt")
	r.runPrompt(prompt)
}
