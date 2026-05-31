package tui

import (
	"fmt"
	"strings"
)

// planMarkdown turns the exit_plan_mode args into a markdown document so the
// plan renders with headings/lists in the transcript.
func planMarkdown(args map[string]any) string {
	plan, _ := args["plan"].(string)
	var b strings.Builder
	b.WriteString("## 📋 Proposed plan\n\n")
	b.WriteString(strings.TrimSpace(plan))
	if raw, ok := args["steps"].([]any); ok && len(raw) > 0 {
		b.WriteString("\n\n### Steps\n")
		for i, s := range raw {
			if str, ok := s.(string); ok && str != "" {
				fmt.Fprintf(&b, "\n%d. %s", i+1, str)
			}
		}
	}
	return strings.TrimSpace(b.String())
}
func planApprovalStepCount(args map[string]any) int {
	raw, ok := args["steps"].([]any)
	if !ok {
		return 0
	}
	count := 0
	for _, step := range raw {
		if text, ok := step.(string); ok && strings.TrimSpace(text) != "" {
			count++
		}
	}
	return count
}
