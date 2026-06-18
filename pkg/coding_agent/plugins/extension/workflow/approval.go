package workflow

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	workflowApprovalRunOnce = "Run once"
	workflowApprovalAlways  = "Always allow this workflow in this project"
	workflowApprovalViewRaw = "View raw script"
	workflowApprovalCancel  = "Cancel"
	maxWorkflowRawViews     = 3
)

var (
	workflowNameRE        = regexp.MustCompile(`(?m)\bname\s*=\s*["']([^"']+)["']`)
	workflowDescriptionRE = regexp.MustCompile(`(?m)\bdescription\s*=\s*["']([^"']+)["']`)
	workflowPhaseRE       = regexp.MustCompile(`\bphase\s*\(\s*["']([^"']+)["']\s*\)`)
	workflowPhaseTitleRE  = regexp.MustCompile(`(?m)\btitle\s*=\s*["']([^"']+)["']`)
)

type workflowApprovalSummary struct {
	Name        string
	Description string
	Phases      []string
}

func (e *Extension) approveWorkflowRun(exec workflowExecution, source string) bool {
	if e == nil || e.api == nil {
		return true
	}
	summary := summarizeWorkflowScript(exec.Script)
	key, canRemember := e.workflowApprovalKey(summary, exec, source)
	mode := normalizeWorkflowPermissionMode(e.api.PermissionMode())
	if mode == "bypasspermissions" {
		return true
	}
	if canRemember && e.workflowApprovalAllowed(key) {
		return true
	}
	title := "Allow workflow run?"
	body := formatWorkflowApprovalBody(summary, exec, source)
	rawViews := 0
	for {
		choice := e.api.Select(title+"\n\n"+body, []string{
			workflowApprovalRunOnce,
			workflowApprovalAlways,
			workflowApprovalViewRaw,
			workflowApprovalCancel,
		})
		switch choice {
		case workflowApprovalRunOnce:
			if mode == "auto" && canRemember {
				if err := e.rememberWorkflowApproval(key); err != nil {
					e.tell(fmt.Sprintf("Workflow approval could not be saved: %v", err))
				}
			}
			return true
		case workflowApprovalAlways:
			if canRemember {
				if err := e.rememberWorkflowApproval(key); err != nil {
					e.tell(fmt.Sprintf("Workflow approval could not be saved: %v", err))
				}
			}
			return true
		case workflowApprovalViewRaw:
			rawViews++
			e.tell(formatWorkflowRawScript(summary, exec))
			if rawViews >= maxWorkflowRawViews {
				e.tell("Workflow approval cancelled after repeated raw script views.")
				return false
			}
			continue
		default:
			return false
		}
	}
}

func normalizeWorkflowPermissionMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	mode = strings.ReplaceAll(mode, "_", "")
	mode = strings.ReplaceAll(mode, "-", "")
	mode = strings.ReplaceAll(mode, " ", "")
	return mode
}

func summarizeWorkflowScript(script string) workflowApprovalSummary {
	return workflowApprovalSummary{
		Name:        firstRegexpGroup(workflowNameRE, script),
		Description: firstRegexpGroup(workflowDescriptionRE, script),
		Phases:      workflowPhaseNames(script),
	}
}

func firstRegexpGroup(re *regexp.Regexp, text string) string {
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func workflowPhaseNames(script string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, match := range workflowPhaseTitleRE.FindAllStringSubmatch(script, -1) {
		if len(match) >= 2 {
			add(match[1])
		}
	}
	for _, match := range workflowPhaseRE.FindAllStringSubmatch(script, -1) {
		if len(match) >= 2 {
			add(match[1])
		}
	}
	return out
}

func formatWorkflowApprovalBody(summary workflowApprovalSummary, exec workflowExecution, source string) string {
	var b strings.Builder
	name := strings.TrimSpace(summary.Name)
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(&b, "Workflow: %s\n", name)
	if strings.TrimSpace(summary.Description) != "" {
		fmt.Fprintf(&b, "Description: %s\n", summary.Description)
	}
	if strings.TrimSpace(source) != "" {
		fmt.Fprintf(&b, "Source: %s\n", source)
	}
	if exec.ScriptPath != "" {
		fmt.Fprintf(&b, "Script: %s\n", exec.ScriptPath)
	}
	if len(summary.Phases) > 0 {
		b.WriteString("Planned phases:\n")
		for _, phase := range summary.Phases {
			fmt.Fprintf(&b, "- %s\n", phase)
		}
	}
	fmt.Fprintf(&b, "Concurrency: %d\n", exec.Concurrency)
	if exec.MaxAgents > 0 {
		fmt.Fprintf(&b, "Max agents: %d\n", exec.MaxAgents)
	}
	if exec.BudgetTotal > 0 {
		fmt.Fprintf(&b, "Budget: %d\n", exec.BudgetTotal)
	}
	b.WriteString("\nWorkflow agents may read, edit, and run tools according to their prompts and your active allowlist.\n\n")
	b.WriteString("Script preview:\n```lua\n")
	b.WriteString(truncateWorkflowScript(exec.Script, 3000))
	b.WriteString("\n```")
	return b.String()
}

func truncateWorkflowScript(script string, max int) string {
	script = strings.TrimSpace(script)
	if max <= 0 || len(script) <= max {
		return script
	}
	if max <= 4 {
		return script[:max]
	}
	return strings.TrimSpace(script[:max-4]) + "\n..."
}

func formatWorkflowRawScript(summary workflowApprovalSummary, exec workflowExecution) string {
	name := strings.TrimSpace(summary.Name)
	if name == "" {
		name = "(unnamed)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Workflow raw script: %s\n", name)
	if exec.ScriptPath != "" {
		fmt.Fprintf(&b, "Script: %s\n", exec.ScriptPath)
	}
	b.WriteString("\n```lua\n")
	b.WriteString(strings.TrimSpace(exec.Script))
	b.WriteString("\n```")
	return b.String()
}
