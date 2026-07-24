package main

import (
	"context"
	"sync"
	"time"

	codetui "github.com/openmodu/modu/cmd/modu_code/internal/tui"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

type moduTUIWorkflowController struct {
	mu          sync.Mutex
	session     *coding_agent.CodingSession
	client      modutui.Client
	ref         moduTUIWorkflowPanelRef
	active      bool
	fingerprint string
}

func newModuTUIWorkflowController(session *coding_agent.CodingSession) *moduTUIWorkflowController {
	return &moduTUIWorkflowController{session: session}
}

func (c *moduTUIWorkflowController) SetClient(client modutui.Client) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.client = client
	c.mu.Unlock()
}

func (c *moduTUIWorkflowController) ObserveUpdate(update modutui.Update) {
	if c == nil {
		return
	}
	switch update := update.(type) {
	case modutui.ShowPanelUpdate:
		c.remember(update.Panel)
	case modutui.RefreshPanelUpdate:
		c.remember(update.Panel)
	case modutui.ClosePanelUpdate:
		c.Forget(update.ID)
	}
}

func (c *moduTUIWorkflowController) remember(panel modutui.Panel) {
	ref, ok := moduTUIWorkflowPanelRefFromPanel(panel)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !ok {
		c.active = false
		c.fingerprint = ""
		return
	}
	c.ref = ref
	c.active = true
	c.fingerprint = moduTUIWorkflowRuntimeFingerprint(c.session)
}

func (c *moduTUIWorkflowController) Forget(panelID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active && (panelID == "" || panelID == c.ref.PanelID) {
		c.active = false
		c.fingerprint = ""
	}
}

func (c *moduTUIWorkflowController) Refresh() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if !c.active {
		c.mu.Unlock()
		return
	}
	ref := c.ref
	lastFingerprint := c.fingerprint
	client := c.client
	c.mu.Unlock()

	fingerprint := moduTUIWorkflowRuntimeFingerprint(c.session)
	if fingerprint == lastFingerprint {
		return
	}
	panel, ok := ref.Panel(c.session)
	if !ok {
		return
	}
	c.mu.Lock()
	shouldSend := c.active && c.ref == ref
	if shouldSend {
		c.fingerprint = fingerprint
	}
	c.mu.Unlock()
	if shouldSend {
		client.RefreshPanel(panel)
	}
}

func (c *moduTUIWorkflowController) RefreshRun(runID string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	if !c.active {
		c.mu.Unlock()
		return false
	}
	ref := c.ref
	client := c.client
	c.mu.Unlock()
	if !ref.MatchesRun(runID) {
		return false
	}
	panel, ok := ref.Panel(c.session)
	if !ok {
		return false
	}
	fingerprint := moduTUIWorkflowRuntimeFingerprint(c.session)
	c.mu.Lock()
	shouldSend := c.active && c.ref == ref && ref.MatchesRun(runID)
	if shouldSend {
		c.fingerprint = fingerprint
	}
	c.mu.Unlock()
	if shouldSend {
		client.RefreshPanel(panel)
	}
	return shouldSend
}

func (c *moduTUIWorkflowController) RunRefresh(ctx context.Context, done <-chan struct{}) {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			c.Refresh()
		}
	}
}

func (c *moduTUIWorkflowController) HandleAction(
	ctx context.Context,
	runtime *codetui.Runtime,
	executor *moduTUICommandExecutor,
	action modutui.PanelAction,
) {
	if command, runID, agentID, status, ok := moduTUIWorkflowAgentControlAction(action); ok {
		runtime.Go("workflow agent control action", func() {
			c.client.SetStatus(status, moduTUITerminalStatusTTL)
			executor.Execute(ctx, command)
			c.client.OpenPanel(moduTUIWorkflowAgentPanel(c.session, runID, agentID))
		})
		return
	}
	if command, runID, status, ok := moduTUIWorkflowControlAction(action); ok {
		runtime.Go("workflow control action", func() {
			c.client.SetStatus(status, moduTUITerminalStatusTTL)
			executor.Execute(ctx, command)
			c.client.OpenPanel(moduTUIWorkflowRunDetailPanel(c.session, runID))
		})
		return
	}
	if panel, ok := moduTUIWorkflowPanelAction(c.session, action); ok {
		runtime.Go("workflow panel action", func() {
			c.client.OpenPanel(panel)
		})
		return
	}
	command := moduTUIWorkflowActionCommand(action)
	if command == "" {
		c.client.ClosePanel(action.PanelID)
		return
	}
	runtime.Go("panel action", func() {
		executor.Execute(ctx, command)
		c.client.ClosePanel(action.PanelID)
	})
}

func (c *moduTUIWorkflowController) HandleToolEvent(ev types.Event) {
	panel, status, ok := moduTUIWorkflowPanelFromToolEvent(c.session, ev)
	if !ok {
		return
	}
	if status != "" {
		c.client.SetStatus(status, moduTUITerminalStatusTTL)
	}
	if panel.ID == "" {
		return
	}
	runID := moduTUIWorkflowRunIDFromToolResult(ev.Result)
	if runID == "" {
		runID = moduTUIWorkflowRunIDFromPanel(panel)
	}
	if runID == "" || !c.RefreshRun(runID) {
		c.client.OpenPanel(panel)
	}
}

func (c *moduTUIWorkflowController) HandleSessionEvent(ev coding_agent.SessionEvent) bool {
	panel, status, ok := moduTUIWorkflowPanelFromNotify(c.session, ev)
	if !ok {
		return false
	}
	if status != "" {
		c.client.SetStatus(status, moduTUITerminalStatusTTL)
	}
	if panel.ID != "" {
		runID := moduTUIWorkflowRunIDFromNotify(ev.Message)
		if runID == "" {
			runID = moduTUIWorkflowRunIDFromPanel(panel)
		}
		if runID == "" || !c.RefreshRun(runID) {
			c.client.OpenPanel(panel)
		}
	}
	return true
}
