package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

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

	uiM := newUIModel(ctx, session, model, rt, histFile, approvalCh, &promptMu, "")
	if history, err := loadHistoryFile(histFile); err == nil {
		uiM.input.SetHistory(history)
	}

	program := tea.NewProgram(uiM, tea.WithAltScreen(), tea.WithMouseCellMotion())

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
		program.Send(uiAgentEventMsg{event: ev})
	})
	defer unsub()

	unsubSession := session.SubscribeSession(func(ev coding_agent.SessionEvent) {
		program.Send(uiSessionEventMsg{event: ev})
	})
	defer unsubSession()

	printer := &uiBridgePrinter{program: program}

	token := os.Getenv("MOMS_TG_TOKEN")
	if tgCfg, err := tgbot.LoadConfig(); err == nil && tgCfg.Token != "" {
		token = tgCfg.Token
	}
	if token != "" {
		attachDir := os.TempDir() + "/modu_code_tg"
		if username, err := tgbot.Start(ctx, token, attachDir, session, printer, &promptMu, approvalCh); err == nil {
			uiM.tgUsername = username
		}
	}

	finalModel, err := program.Run()
	if uiFinal, ok := finalModel.(*uiModel); ok && uiFinal != nil {
		if transcript := strings.TrimSpace(uiFinal.renderExitTranscript()); transcript != "" {
			fmt.Printf("\n%s\n", transcript)
		}
	}
	return err
}
