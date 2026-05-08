package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_code/internal/mailboxrt"
	"github.com/openmodu/modu/cmd/modu_code/internal/tgbot"
)

// Run starts the interactive TUI session.
func Run(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, rt *mailboxrt.Runtime, noApprove bool) error {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
		_ = n
	}

	histFile := session.InputHistoryFile()
	var approvalCh chan tui.ApprovalRequest
	if !noApprove {
		approvalCh = make(chan tui.ApprovalRequest)
	}
	var promptMu sync.Mutex

	root := newGoTUIRoot(ctx, session, model, rt, histFile, approvalCh, &promptMu)
	if history, err := loadHistoryFile(histFile); err == nil {
		root.history = history
	}

	app, err := gotui.NewApp(
		gotui.WithRootComponent(root),
		gotui.WithInlineHeight(5),
	)
	if err != nil {
		return err
	}
	defer app.Close()

	if approvalCh != nil {
		session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
			respCh := make(chan string, 1)
			approvalCh <- tui.ApprovalRequest{
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

	err = app.Run()
	// In inline mode the conversation stays in the terminal scrollback; just print session stats.
	if meta := strings.TrimSpace(root.model.renderExitSessionMeta()); meta != "" {
		fmt.Println(meta)
	}
	return err
}
