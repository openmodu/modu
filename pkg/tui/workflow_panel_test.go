package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestWorkflowPanelContentShowsRunsAndCommands(t *testing.T) {
	content := workflowPanelContentFromStates(map[string]any{
		"workflow": map[string]any{
			"runningCount":   1,
			"stoppedCount":   0,
			"completedCount": 2,
			"failedCount":    1,
			"indicator":      "workflow review 1/3 running: Review",
			"runs": []map[string]any{
				{
					"id":           "run-newest",
					"name":         "review",
					"status":       "running",
					"agentCount":   3,
					"doneCount":    1,
					"errorCount":   0,
					"currentPhase": "Review",
					"updatedAt":    200,
					"phases": []map[string]any{
						{
							"title":           "Inventory",
							"agentCount":      1,
							"doneCount":       1,
							"runningCount":    0,
							"errorCount":      0,
							"estimatedTokens": 42,
							"cost":            0.0012,
							"durationMs":      300,
						},
						{
							"title":           "Review",
							"agentCount":      2,
							"doneCount":       0,
							"runningCount":    2,
							"errorCount":      0,
							"estimatedTokens": 120,
						},
					},
					"agents": []map[string]any{
						{
							"id":              2,
							"label":           "risk",
							"phase":           "Review",
							"promptPreview":   "Review risks in pkg/coding_agent",
							"status":          "running",
							"estimatedTokens": 120,
							"cost":            0.0045,
							"failedToolCalls": 1,
							"recentToolCalls": 2,
							"error":           "permission denied",
							"recentToolCallPreviews": []map[string]any{
								{
									"toolName":      "read",
									"argsPreview":   `{"path":"go.mod"}`,
									"resultPreview": "module github.com/openmodu/modu",
								},
								{
									"toolName":    "bash",
									"isError":     true,
									"argsPreview": `{"command":"exit 1"}`,
								},
							},
						},
						{
							"id":            1,
							"label":         "inventory",
							"phase":         "Review",
							"promptPreview": "Inspect repository inventory",
							"status":        "done",
							"turnTokens":    42,
							"cost":          0.0012,
							"resultPreview": "inventory ok",
						},
					},
				},
				{
					"id":         "run-oldest",
					"name":       "research",
					"status":     "failed",
					"agentCount": 2,
					"doneCount":  1,
					"errorCount": 1,
					"updatedAt":  100,
				},
			},
		},
	})
	for _, want := range []string{
		"running: 1",
		"completed: 2",
		"failed: 1",
		"workflow review 1/3 running: Review",
		"run-newe [running] 1/3 review phase=Review",
		"run-olde [failed] 1/2 research errors=1",
		"latest run phases",
		"Inventory: 1 agent(s), 1 done, 0 running, 0 errors estimated=42 cost=0.001200 durationMs=300",
		"Review: 2 agent(s), 0 done, 2 running, 0 errors estimated=120",
		"latest run agents",
		"#1 [done] inventory phase=Review tokens=42 cost=0.001200",
		"prompt: Inspect repository inventory",
		"result: inventory ok",
		"#2 [running] risk phase=Review estimated=120 cost=0.004500 failedTools=1 recentTools=2",
		"prompt: Review risks in pkg/coding_agent",
		"error: permission denied",
		`tools: read[ok] args={"path":"go.mod"} result=module github.com/openmodu/modu; bash[error] args={"command":"exit 1"}`,
		"/workflows show <run-id|latest>",
		"/workflows agent-stop|agent-restart <run-id|latest> <agent-id>",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("panel missing %q:\n%s", want, content)
		}
	}
	if strings.Index(content, "run-newe") > strings.Index(content, "run-olde") {
		t.Fatalf("runs not sorted newest first:\n%s", content)
	}
}

func TestWorkflowPanelContentHandlesMissingWorkflowState(t *testing.T) {
	if got := workflowPanelContentFromStates(nil); got != "" {
		t.Fatalf("nil state content = %q", got)
	}
	if got := workflowPanelContentFromStates(map[string]any{"workflow": "bad"}); got != "" {
		t.Fatalf("bad state content = %q", got)
	}
}

func TestWorkflowPanelContentShowsEmptyRuns(t *testing.T) {
	content := workflowPanelContentFromStates(map[string]any{
		"workflow": map[string]any{
			"runningCount": 0,
			"runs":         []any{},
		},
	})
	if !strings.Contains(content, "no live workflow runs in this session") {
		t.Fatalf("empty runs content = %q", content)
	}
}

func TestWorkflowPanelSelectionWrapsAndBuildsShowCommand(t *testing.T) {
	runs := []workflowPanelRun{
		{ID: "run-one"},
		{ID: "run-two"},
		{ID: "run-three"},
	}
	if got := workflowPanelMoveSelection(0, len(runs), -1); got != 2 {
		t.Fatalf("move up from first = %d, want 2", got)
	}
	if got := workflowPanelMoveSelection(2, len(runs), 1); got != 0 {
		t.Fatalf("move down from last = %d, want 0", got)
	}
	if got := workflowPanelMoveSelection(-10, len(runs), 0); got != 2 {
		t.Fatalf("negative selection = %d, want 2", got)
	}
	if got := workflowPanelSelectedRunCommand(runs, 99); got != "/workflows show run-three" {
		t.Fatalf("selected command = %q", got)
	}
	if got := workflowPanelSelectedRunCommand(nil, 0); got != "" {
		t.Fatalf("empty selected command = %q", got)
	}
}

func TestWorkflowPanelAgentsParseFullPrompt(t *testing.T) {
	agents := workflowPanelAgents([]map[string]any{
		{
			"id":            1,
			"label":         "risk",
			"status":        "running",
			"prompt":        "line one\nline two",
			"promptPreview": "line one",
		},
	})
	if len(agents) != 1 || agents[0].Prompt != "line one\nline two" || agents[0].PromptPreview != "line one" {
		t.Fatalf("agents = %+v", agents)
	}
}

func TestWorkflowPanelSelectableContentShowsSelectedRunsAndHint(t *testing.T) {
	content := stripANSIForGoTUI(workflowPanelSelectableContent(map[string]any{
		"runningCount": 1,
		"failedCount":  1,
		"indicator":    "workflow review 1/2 running: Review",
	}, []workflowPanelRun{
		{
			ID:           "run-newest",
			Name:         "review",
			Status:       "running",
			AgentCount:   2,
			DoneCount:    1,
			CurrentPhase: "Review",
		},
		{
			ID:         "run-failed",
			Name:       "audit",
			Status:     "failed",
			AgentCount: 1,
			ErrorCount: 1,
		},
	}, 1, 0))
	for _, want := range []string{
		"Workflows",
		"running=1 failed=1",
		"workflow review 1/2 running: Review",
		"run-newe [running] 1/2 review phase=Review",
		"> run-fail [failed] 0/1 audit errors=1",
		"enter/right phases",
		"esc/q close",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("selectable panel missing %q:\n%s", want, content)
		}
	}
}

func TestWorkflowPanelEnterOpensPhaseViewAndEscReturnsToRuns(t *testing.T) {
	root := &bubbleTUI{
		model: &uiModel{state: uiStateWorkflowPanel},
		workflowRuns: []workflowPanelRun{
			{
				ID:           "run-phase",
				Name:         "review",
				Status:       "running",
				AgentCount:   2,
				DoneCount:    1,
				CurrentPhase: "Review",
				Phases: []workflowPanelPhase{
					{Title: "Inventory", AgentCount: 1, DoneCount: 1},
					{Title: "Review", AgentCount: 1, RunningCount: 1},
				},
			},
		},
	}
	_, cmd := root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("enter with phases returned command")
	}
	if root.workflowPanelLevel != workflowPanelLevelPhases {
		t.Fatalf("level = %v, want phases", root.workflowPanelLevel)
	}
	if root.workflowPhaseIdx != 0 {
		t.Fatalf("phase idx = %d, want 0", root.workflowPhaseIdx)
	}

	root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if root.workflowPhaseIdx != 1 {
		t.Fatalf("phase idx after j = %d, want 1", root.workflowPhaseIdx)
	}

	root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if root.workflowPanelLevel != workflowPanelLevelRuns {
		t.Fatalf("level after esc = %v, want runs", root.workflowPanelLevel)
	}
	if root.model.state != uiStateWorkflowPanel {
		t.Fatalf("state after phase esc = %v, want workflow panel", root.model.state)
	}
}

func TestWorkflowPanelPhaseOpensAgentsAndAgentDetail(t *testing.T) {
	root := &bubbleTUI{
		model: &uiModel{state: uiStateWorkflowPanel},
		workflowRuns: []workflowPanelRun{
			{
				ID:     "run-agent",
				Name:   "review",
				Status: "running",
				Phases: []workflowPanelPhase{
					{Title: "Review", AgentCount: 2, RunningCount: 1},
				},
				Agents: []workflowPanelAgent{
					{ID: 1, Label: "inventory", Phase: "Inventory", Status: "done"},
					{
						ID:              2,
						Label:           "risk",
						Phase:           "Review",
						Status:          "running",
						PromptPreview:   "Review pkg/coding_agent risks",
						ResultPreview:   "risk notes",
						RecentToolCalls: 1,
						ToolCalls: []workflowPanelToolCall{
							{ToolName: "read", ArgsPreview: `{"path":"go.mod"}`, ResultPreview: "module github.com/openmodu/modu"},
						},
					},
				},
			},
		},
	}
	_, cmd := root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || root.workflowPanelLevel != workflowPanelLevelPhases {
		t.Fatalf("enter run level=%v cmd=%v", root.workflowPanelLevel, cmd)
	}
	_, cmd = root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || root.workflowPanelLevel != workflowPanelLevelAgents {
		t.Fatalf("enter phase level=%v cmd=%v", root.workflowPanelLevel, cmd)
	}
	if got := len(root.currentWorkflowAgents()); got != 1 {
		t.Fatalf("filtered agents = %d, want 1", got)
	}
	_, cmd = root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || root.workflowPanelLevel != workflowPanelLevelAgentDetail {
		t.Fatalf("enter agent level=%v cmd=%v", root.workflowPanelLevel, cmd)
	}

	root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if root.workflowPanelLevel != workflowPanelLevelAgents {
		t.Fatalf("detail esc level=%v, want agents", root.workflowPanelLevel)
	}
	root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if root.workflowPanelLevel != workflowPanelLevelPhases {
		t.Fatalf("agents esc level=%v, want phases", root.workflowPanelLevel)
	}
}

func TestWorkflowPanelPhaseContentShowsProgressAndBackHint(t *testing.T) {
	content := stripANSIForGoTUI(workflowPanelPhaseContent(workflowPanelRun{
		ID:           "run-phase",
		Name:         "review",
		Status:       "running",
		AgentCount:   2,
		DoneCount:    1,
		CurrentPhase: "Review",
		Phases: []workflowPanelPhase{
			{Title: "Inventory", AgentCount: 1, DoneCount: 1, EstimatedTokens: 42, Cost: 0.0012, DurationMs: 300},
			{Title: "Review", AgentCount: 1, RunningCount: 1, ErrorCount: 1},
		},
	}, 1, 0))
	for _, want := range []string{
		"Workflow phases",
		"run-phas running",
		"review [running] 1/2 phase=Review",
		"Inventory: 1 agent(s), 1 done, 0 running, 0 errors estimated=42 cost=0.001200 durationMs=300",
		"> Review: 1 agent(s), 0 done, 1 running, 1 errors",
		"esc/left runs",
		"q close",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("phase panel missing %q:\n%s", want, content)
		}
	}
}

func TestWorkflowPanelAgentsAndDetailContent(t *testing.T) {
	run := workflowPanelRun{ID: "run-agent", Status: "running"}
	phase := workflowPanelPhase{Title: "Review", AgentCount: 1, RunningCount: 1}
	agents := []workflowPanelAgent{
		{
			ID:              2,
			Label:           "risk",
			Phase:           "Review",
			Status:          "running",
			EstimatedTokens: 120,
			Cost:            0.0045,
			FailedToolCalls: 1,
			RecentToolCalls: 1,
			Prompt:          "Review pkg/coding_agent risks\nCheck workflow runtime",
			PromptPreview:   "Review pkg/coding_agent risks",
			ResultPreview:   "risk notes",
			ToolCalls: []workflowPanelToolCall{
				{ToolName: "read", ArgsPreview: `{"path":"go.mod"}`, ResultPreview: "module github.com/openmodu/modu"},
			},
		},
	}
	list := stripANSIForGoTUI(workflowPanelAgentsContent(run, phase, agents, 0, 0))
	for _, want := range []string{
		"Workflow agents",
		"Review: 1 agent(s), 0 done, 1 running, 0 errors",
		"> #2 [running] risk estimated=120 cost=0.004500 failedTools=1 recentTools=1",
		"enter/right detail",
		"esc/left phases",
	} {
		if !strings.Contains(list, want) {
			t.Fatalf("agent list missing %q:\n%s", want, list)
		}
	}

	detail := stripANSIForGoTUI(workflowPanelAgentDetailContent(run, phase, agents[0], 0))
	for _, want := range []string{
		"Workflow agent",
		"#2 [running] risk",
		"phase: Review",
		"estimated tokens: 120",
		"cost: 0.004500",
		"tool calls: 1 recent, 1 failed",
		"prompt:",
		"Review pkg/coding_agent risks",
		"Check workflow runtime",
		"result: risk notes",
		"esc/left agents",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("agent detail missing %q:\n%s", want, detail)
		}
	}
	detail = stripANSIForGoTUI(workflowPanelAgentDetailContent(run, phase, agents[0], 1))
	if want := `read[ok] args={"path":"go.mod"} result=module github.com/openmodu/modu`; !strings.Contains(detail, want) {
		t.Fatalf("scrolled agent detail missing %q:\n%s", want, detail)
	}
}

func TestWorkflowPanelControlCommands(t *testing.T) {
	root := &bubbleTUI{
		model: &uiModel{state: uiStateWorkflowPanel},
		workflowRuns: []workflowPanelRun{
			{
				ID:     "run-control",
				Status: "running",
				Phases: []workflowPanelPhase{
					{Title: "Review", AgentCount: 1, RunningCount: 1},
				},
				Agents: []workflowPanelAgent{
					{ID: 7, Label: "risk", Phase: "Review", Status: "running"},
				},
			},
		},
	}
	if cmd, status := root.workflowPanelControlCommand("p"); cmd != "/workflows pause run-control" || status != "pausing workflow" {
		t.Fatalf("pause command = %q %q", cmd, status)
	}
	if cmd, status := root.workflowPanelControlCommand("x"); cmd != "/workflows stop run-control" || status != "stopping workflow" {
		t.Fatalf("stop run command = %q %q", cmd, status)
	}
	if cmd, status := root.workflowPanelControlCommand("r"); cmd != "" || status != "select an agent to restart" {
		t.Fatalf("restart without agent = %q %q", cmd, status)
	}

	root.workflowPanelLevel = workflowPanelLevelAgents
	root.workflowPhaseIdx = 0
	if cmd, status := root.workflowPanelControlCommand("x"); cmd != "/workflows agent-stop run-control 7" || status != "stopping workflow agent" {
		t.Fatalf("agent stop command = %q %q", cmd, status)
	}
	if cmd, status := root.workflowPanelControlCommand("r"); cmd != "/workflows agent-restart run-control 7" || status != "restarting workflow agent" {
		t.Fatalf("agent restart command = %q %q", cmd, status)
	}

	root.workflowRuns[0].Status = "stopped"
	if cmd, status := root.workflowPanelControlCommand("p"); cmd != "/workflows resume run-control" || status != "resuming workflow" {
		t.Fatalf("resume command = %q %q", cmd, status)
	}
}

func TestWorkflowPanelSaveInput(t *testing.T) {
	root := &bubbleTUI{
		model: &uiModel{state: uiStateWorkflowPanel},
		workflowRuns: []workflowPanelRun{
			{ID: "run-save", Name: "review", Status: "completed"},
		},
		workflowPanelLevel: workflowPanelLevelRuns,
	}
	root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if root.workflowPanelLevel != workflowPanelLevelSave {
		t.Fatalf("level after s = %v, want save", root.workflowPanelLevel)
	}
	for _, r := range "reuse_1" {
		root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if root.workflowSaveName != "reuse_1" {
		t.Fatalf("save name = %q", root.workflowSaveName)
	}
	root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if root.workflowSaveScope != "user" {
		t.Fatalf("save scope = %q, want user", root.workflowSaveScope)
	}
	if cmd, status := workflowPanelSaveCommand(root.workflowRuns[0], root.workflowSaveName, root.workflowSaveScope); cmd != "/workflows save run-save reuse_1 user" || status != "saving workflow" {
		t.Fatalf("save command = %q %q", cmd, status)
	}

	root.updateWorkflowPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if root.workflowPanelLevel != workflowPanelLevelRuns {
		t.Fatalf("level after save esc = %v, want runs", root.workflowPanelLevel)
	}
}

func TestWorkflowPanelSaveContentAndValidation(t *testing.T) {
	run := workflowPanelRun{ID: "run-save", Name: "review", Status: "completed"}
	content := stripANSIForGoTUI(workflowPanelSaveContent(run, "reuse", "user"))
	for _, want := range []string{
		"Save workflow",
		"review [completed]",
		"name: reuse",
		"scope: user",
		"tab scope",
		"enter save",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("save content missing %q:\n%s", want, content)
		}
	}
	if cmd, status := workflowPanelSaveCommand(run, "-bad", "project"); cmd != "" || !strings.Contains(status, "workflow name") {
		t.Fatalf("invalid save command = %q %q", cmd, status)
	}
	if !workflowSaveNameValid("valid.name_1") {
		t.Fatal("expected valid save name")
	}
}
