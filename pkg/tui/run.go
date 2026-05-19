package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/approval"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/tgbot"
	"github.com/openmodu/modu/pkg/types"
)

// Run starts the interactive TUI session.
func Run(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool) error {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
		_ = n
	}

	histFile := session.InputHistoryFile()
	var approvalCh chan approval.Request
	if !noApprove {
		approvalCh = make(chan approval.Request)
	}
	var promptMu sync.Mutex

	root := newGoTUIRoot(ctx, session, model, histFile, approvalCh, &promptMu)
	if history, err := loadHistoryFile(histFile); err == nil {
		root.history = history
		root.historyIndex = len(history)
	}

	app, err := gotui.NewApp(
		gotui.WithRootComponent(root),
		gotui.WithInlineHeight(5),
		gotui.WithCursor(),
		// Soft-reset the visible viewport on launch: push whatever was in the
		// terminal (e.g. a prior modu_code session's exit stats) up into
		// scrollback via newline flow instead of rendering the inline widget
		// glued directly beneath it. Non-destructive — no screen/scrollback
		// erase — so the previous conversation stays scrollable above.
		gotui.WithInlineStartupMode(gotui.InlineStartupSoftReset),
	)
	if err != nil {
		return err
	}
	defer app.Close()

	if approvalCh != nil {
		session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
			respCh := make(chan string, 1)
			req := approval.Request{
				ToolName:   toolName,
				ToolCallID: toolCallID,
				Args:       args,
				Response:   respCh,
			}
			select {
			case approvalCh <- req:
			case <-ctx.Done():
				return agent.ToolApprovalDeny, ctx.Err()
			case <-app.StopCh():
				return agent.ToolApprovalDeny, context.Canceled
			}
			select {
			case decision := <-respCh:
				return agent.ToolApprovalDecision(decision), nil
			case <-ctx.Done():
				return agent.ToolApprovalDeny, ctx.Err()
			case <-app.StopCh():
				return agent.ToolApprovalDeny, context.Canceled
			}
		})

		// Plan approval is a richer prompt (renders the plan, three-way
		// decision, free-form rejection feedback) but rides the same
		// approval channel. Decision strings: "approve", "approve_auto",
		// "reject", "reject:<feedback>".
		session.SetPlanDecisionCallback(func(plan string, steps []string) string {
			respCh := make(chan string, 1)
			anySteps := make([]any, len(steps))
			for i, s := range steps {
				anySteps[i] = s
			}
			req := approval.Request{
				ToolName:   "exit_plan_mode",
				ToolCallID: "plan",
				Args:       map[string]any{"plan": plan, "steps": anySteps},
				Response:   respCh,
			}
			select {
			case approvalCh <- req:
			case <-ctx.Done():
				return "reject"
			case <-app.StopCh():
				return "reject"
			}
			select {
			case decision := <-respCh:
				return decision
			case <-ctx.Done():
				return "reject"
			case <-app.StopCh():
				return "reject"
			}
		})
	}

	unsub := session.Subscribe(func(ev agent.AgentEvent) {
		app.QueueUpdate(func() {
			root.handleAgentEvent(ev)
		})
	})
	defer unsub()

	unsubSession := session.SubscribeSession(func(ev coding_agent.SessionEvent) {
		app.QueueUpdate(func() {
			root.handleSessionEvent(ev)
		})
	})
	defer unsubSession()

	printer := &goTUIBridgePrinter{root: root}
	token := os.Getenv("MOMS_TG_TOKEN")
	if tgCfg, err := tgbot.LoadConfig(); err == nil && tgCfg.Token != "" {
		token = tgCfg.Token
	}
	if token != "" {
		attachDir := os.TempDir() + "/modu_code_tg"
		if username, err := tgbot.Start(ctx, token, attachDir, session, printer, &promptMu, approvalCh); err == nil {
			root.tgUsername = username
		}
	}

	err = runLoop(app, root)
	// In inline mode the conversation stays in the terminal scrollback; just print session stats.
	if meta := strings.TrimSpace(root.model.renderExitSessionMeta()); meta != "" {
		fmt.Println(meta)
	}
	return err
}

// runLoop drives the go-tui event loop and positions the terminal cursor at
// the text-input location after each frame so the OS knows where to anchor
// the IME candidate window. Mirrors app.Run() but adds the positionCursor call.
func runLoop(app *gotui.App, root *goTUIRoot) error {
	if err := app.Open(); err != nil {
		return err
	}
	const frameDuration = 16 * time.Millisecond
	resized := false
	for {
		frameStart := time.Now()
		deadline := frameStart.Add(frameDuration / 2)
	drain:
		for time.Now().Before(deadline) {
			select {
			case ev := <-app.Events():
				if _, ok := ev.(gotui.ResizeEvent); ok {
					resized = true
				}
				app.Dispatch(ev)
			case <-app.StopCh():
				return nil
			default:
				break drain
			}
		}
		if resized {
			// On resize go-tui's inline full-redraw only clears from the new
			// inlineStartRow down; the old frame, pushed above that row by the
			// reflowed scrollback, is orphaned. Wipe the whole screen *and*
			// the terminal scrollback buffer (\033[3J), then reflow the saved
			// history to the new width before go-tui's built-in resize path
			// (it already set needsFullRedraw when it dispatched the
			// ResizeEvent) repaints the widget once at the bottom. We do NOT
			// call resetInlineHeight here: SetInlineHeight drives its own
			// inline-session scroll/redraw that double-draws against the
			// manual clear.
			_, _ = app.Terminal().WriteDirect([]byte("\033[3J\033[2J\033[H"))
			if w, _ := app.Size(); w > 0 {
				root.model.width = max(20, w-2)
			}
			root.repaintAbove()
			resized = false
		}
		app.Render()
		root.positionCursor(app)
		elapsed := time.Since(frameStart)
		if remaining := frameDuration - elapsed; remaining > 0 {
			select {
			case <-time.After(remaining):
			case <-app.StopCh():
				return nil
			}
		}
	}
}
