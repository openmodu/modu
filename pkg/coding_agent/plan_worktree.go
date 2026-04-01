package coding_agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/subagent"
	"github.com/openmodu/modu/pkg/coding_agent/tools"
)

type planModeAdapter struct {
	session *CodingSession
}

func (a planModeAdapter) EnterPlanMode() {
	if a.session == nil {
		return
	}
	a.session.planMu.Lock()
	defer a.session.planMu.Unlock()
	a.session.planMode = true
}

func (a planModeAdapter) ExitPlanMode(plan string) {
	if a.session == nil {
		return
	}
	a.session.planMu.Lock()
	defer a.session.planMu.Unlock()
	a.session.planMode = false
	_ = plan
}

func (a planModeAdapter) IsPlanMode() bool {
	if a.session == nil {
		return false
	}
	a.session.planMu.RLock()
	defer a.session.planMu.RUnlock()
	return a.session.planMode
}

func (s *CodingSession) replacePlanTools() {
	enter := tools.NewEnterPlanModeTool(planModeAdapter{session: s})
	exit := tools.NewExitPlanModeTool(planModeAdapter{session: s})
	s.activeTools = replaceAgentTool(s.activeTools, enter)
	s.activeTools = replaceAgentTool(s.activeTools, exit)
	stateTools := replaceAgentTool(s.agent.GetState().Tools, enter)
	stateTools = replaceAgentTool(stateTools, exit)
	s.agent.SetTools(stateTools)
}

type worktreeAdapter struct {
	session *CodingSession
}

func (a worktreeAdapter) EnterWorktree() (string, error) {
	if a.session == nil {
		return "", fmt.Errorf("worktree session is not configured")
	}
	return a.session.EnterWorktree()
}

func (a worktreeAdapter) ExitWorktree() error {
	if a.session == nil {
		return fmt.Errorf("worktree session is not configured")
	}
	return a.session.ExitWorktree()
}

func (a worktreeAdapter) ActiveWorktree() string {
	if a.session == nil {
		return ""
	}
	return a.session.worktreePath
}

func (s *CodingSession) replaceWorktreeTools() {
	enter := tools.NewEnterWorktreeTool(worktreeAdapter{session: s})
	exit := tools.NewExitWorktreeTool(worktreeAdapter{session: s})
	s.activeTools = replaceAgentTool(s.activeTools, enter)
	s.activeTools = replaceAgentTool(s.activeTools, exit)
	stateTools := replaceAgentTool(s.agent.GetState().Tools, enter)
	stateTools = replaceAgentTool(stateTools, exit)
	s.agent.SetTools(stateTools)
}

func (s *CodingSession) EnterWorktree() (string, error) {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()

	if s.worktreePath != "" {
		return s.worktreePath, nil
	}

	root, err := gitOutput(s.cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("enter_worktree: not a git repository: %w", err)
	}

	baseDir := filepath.Join(s.agentDir, "worktrees")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(baseDir, fmt.Sprintf("wt-%d", time.Now().UnixMilli()))
	if _, err := runGit(root, "worktree", "add", "--detach", path, "HEAD"); err != nil {
		return "", fmt.Errorf("enter_worktree: %w", err)
	}

	s.originalCwd = s.cwd
	s.worktreePath = path
	s.cwd = path
	s.refreshToolsForCwd(path)
	return path, nil
}

func (s *CodingSession) ExitWorktree() error {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()

	if s.worktreePath == "" {
		return nil
	}
	path := s.worktreePath
	restore := s.originalCwd
	root, err := gitOutput(path, "rev-parse", "--show-toplevel")
	if err == nil {
		_, _ = runGit(root, "worktree", "remove", "--force", path)
	}
	s.worktreePath = ""
	if restore != "" {
		s.cwd = restore
		s.originalCwd = ""
		s.refreshToolsForCwd(restore)
	}
	return nil
}

func (s *CodingSession) refreshToolsForCwd(cwd string) {
	var updated []agent.AgentTool
	for _, tool := range s.activeTools {
		switch tool.Name() {
		case "read":
			updated = append(updated, tools.NewReadTool(cwd))
		case "write":
			updated = append(updated, tools.NewWriteTool(cwd))
		case "edit":
			updated = append(updated, tools.NewEditTool(cwd))
		case "bash":
			updated = append(updated, tools.NewBashTool(cwd))
		case "grep":
			updated = append(updated, tools.NewGrepTool(cwd))
		case "find":
			updated = append(updated, tools.NewFindTool(cwd))
		case "ls":
			updated = append(updated, tools.NewLsTool(cwd))
		case "spawn_subagent":
			updated = append(updated, tools.NewSpawnSubagentTool(cwd, s.agentDir, s.subagentLoader, updated, s.model, s.getAPIKey, s.streamFn, func(def *subagent.SubagentDefinition) *subagent.SubagentDefinition {
				return prepareSubagentDefinition(def, s.skillManager, s.memoryStore)
			}, taskStoreAdapter{manager: s.taskManager}))
		default:
			updated = append(updated, tool)
		}
	}
	s.activeTools = updated
	s.agent.SetTools(updated)
}

func gitOutput(dir string, args ...string) (string, error) {
	out, err := runGit(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
