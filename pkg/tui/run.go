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
	"github.com/openmodu/modu/pkg/mailboxrt"
	"github.com/openmodu/modu/pkg/tgbot"
	"github.com/openmodu/modu/pkg/types"
)

// Run starts the interactive TUI session.
func Run(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, rt *mailboxrt.Runtime, noApprove bool) error {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
		_ = n
	}

	histFile := session.InputHistoryFile()
	var approvalCh chan approval.Request
	if !noApprove {
		approvalCh = make(chan approval.Request)
	}
	var promptMu sync.Mutex

	root := newGoTUIRoot(ctx, session, model, rt, histFile, approvalCh, &promptMu)
	if history, err := loadHistoryFile(histFile); err == nil {
		root.history = history
		root.historyIndex = len(history)
	}

	app, err := gotui.NewApp(
		gotui.WithRootComponent(root),
		gotui.WithInlineHeight(5),
		gotui.WithCursor(),
	)
	if err != nil {
		return err
	}
	defer app.Close()

	if approvalCh != nil {
		session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
			respCh := make(chan string, 1)
			approvalCh <- approval.Request{
				ToolName:   toolName,
				ToolCallID: toolCallID,
				Args:       args,
				Response:   respCh,
			}
			return agent.ToolApprovalDecision(<-respCh), nil
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
			// Clear the visible screen (not scrollback) to remove ghost widget
			// frames that terminal emulators can leave behind on resize.
			_, _ = app.Terminal().WriteDirect([]byte("\033[H\033[2J"))
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
