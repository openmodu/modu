package tui

import (
	"fmt"
	"strings"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
)

func planPanelContent(session *coding_agent.CodingSession) string {
	status := session.PlanStatus()
	lines := []string{
		"status",
		"  active: " + yesNoText(status.Active),
		"  latest: " + yesNoText(status.PlanExists),
		fmt.Sprintf("  revisions: %d", status.RevisionCount),
		fmt.Sprintf("  todos: total=%d pending=%d in_progress=%d completed=%d", status.TodoTotal, status.TodoPending, status.TodoInProgress, status.TodoCompleted),
	}
	if status.PlanFile != "" {
		lines = append(lines, "  file: "+status.PlanFile)
	}
	revisions := session.ListPlanRevisions()
	if len(revisions) > 0 {
		lines = append(lines, "", "recent revisions")
		for i, revision := range revisions {
			if i >= 5 {
				break
			}
			lines = append(lines, "  "+revision.ModTime.Format("2006-01-02 15:04:05")+"  "+revision.Name)
		}
	}
	todos := session.GetTodos()
	if len(todos) > 0 {
		lines = append(lines, "", "todo progress")
		for i, todo := range todos {
			if i >= 6 {
				lines = append(lines, fmt.Sprintf("  ... +%d more", len(todos)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("  [%s] %s", todo.Status, todo.Content))
		}
	}
	return strings.Join(lines, "\n")
}
func worktreePanelContent(session *coding_agent.CodingSession) string {
	status := session.WorktreeStatus()
	lines := []string{
		"status",
		"  active: " + yesNoText(status.Active),
		"  exists: " + yesNoText(status.Exists),
	}
	if status.Cwd != "" {
		lines = append(lines, "  cwd: "+status.Cwd)
	}
	if status.Path != "" {
		lines = append(lines, "  path: "+status.Path)
	}
	if status.OriginalCwd != "" {
		lines = append(lines, "  original cwd: "+status.OriginalCwd)
	}
	worktrees := session.ListManagedWorktrees()
	lines = append(lines, "", fmt.Sprintf("managed worktrees: %d", len(worktrees)))
	for i, wt := range worktrees {
		if i >= 5 {
			lines = append(lines, fmt.Sprintf("  ... +%d more", len(worktrees)-i))
			break
		}
		state := "idle"
		if wt.Active {
			state = "active"
		}
		lines = append(lines, fmt.Sprintf("  %s exists=%s %s", state, yesNoText(wt.Exists), wt.Path))
	}
	if status.Active {
		diff, err := session.ActiveWorktreeDiff()
		if err == nil {
			lines = append(lines, "", "diff")
			if strings.TrimSpace(diff.Stat) == "" && strings.TrimSpace(diff.NameStatus) == "" {
				lines = append(lines, "  no changes")
			} else {
				if strings.TrimSpace(diff.Stat) != "" {
					lines = append(lines, indentBlock("  ", diff.Stat)...)
				}
				if strings.TrimSpace(diff.NameStatus) != "" {
					lines = append(lines, "  files:")
					lines = append(lines, indentBlock("    ", diff.NameStatus)...)
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

func indentBlock(prefix, text string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, prefix+line)
	}
	return lines
}

func yesNoText(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
