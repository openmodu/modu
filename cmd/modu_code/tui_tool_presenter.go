package main

import (
	"fmt"
	"strconv"
	"strings"

	codetui "github.com/openmodu/modu/cmd/modu_code/internal/tui"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

// moduTUIToolPresenter is the single modu_code mapping from agent tool data to
// the standard ToolNode contract. Rendering remains owned by pkg/modu-tui.
type moduTUIToolPresenter struct{}

var _ codetui.ToolNodePresenter = moduTUIToolPresenter{}

func (moduTUIToolPresenter) EventNode(event types.Event, cwd string) (modutui.ToolNode, bool) {
	switch event.Type {
	case types.EventTypeToolExecutionStart:
		input := toolInputFromArgs(event.ToolName, event.Args)
		return modutui.ToolNode{
			Call: modutui.ToolCall{
				ID:         event.ToolCallID,
				Name:       toolRenderNameFromArgsWithCwd(event.ToolName, event.Args, cwd),
				Summary:    toolRunningSummaryFromArgs(event.ToolName, event.Args),
				Detail:     input,
				Input:      input,
				Output:     toolPreviewOutputFromArgsWithCwd(event.ToolName, event.Args, cwd),
				BatchSize:  event.BatchSize,
				BatchID:    event.BatchID,
				Code:       toolCodeFromArgsWithCwd(event.ToolName, event.Args, cwd),
				Language:   toolLanguageFromArgsWithCwd(event.ToolName, event.Args, cwd),
				NoCollapse: isWriteLikeTool(event.ToolName),
			},
			Expanded: isWriteLikeTool(event.ToolName),
		}, true
	case types.EventTypeToolExecutionEnd:
		output := toolOutputFromResult(event.ToolName, event.IsError, event.Result)
		artifact := toolArtifactInfoFromResult(event.Result)
		return modutui.ToolNode{
			Call: modutui.ToolCall{
				ID:           event.ToolCallID,
				Name:         toolRenderName(event.ToolName, event.Result),
				Summary:      toolDoneSummary(event.ToolName, event.IsError, output),
				Output:       toolDisplayOutput(event.ToolName, event.IsError, output),
				ArtifactID:   artifact.ID,
				ArtifactPath: artifact.Path,
				Truncated:    artifact.Truncated,
				BatchSize:    event.BatchSize,
				BatchID:      event.BatchID,
				Code:         toolCodeFromResult(event.ToolName, output),
				Language:     toolLanguageFromResult(event.ToolName),
				Error:        event.IsError,
				Done:         true,
				NoCollapse:   isWriteLikeTool(event.ToolName),
			},
			Expanded: event.IsError || isWriteLikeTool(event.ToolName),
		}, true
	default:
		return modutui.ToolNode{}, false
	}
}

func (moduTUIToolPresenter) CallNode(call *types.ToolCallContent, cwd string) modutui.ToolNode {
	if call == nil {
		return modutui.ToolNode{}
	}
	input := toolInputFromArgs(call.Name, call.Arguments)
	return modutui.ToolNode{
		Call: modutui.ToolCall{
			ID:         call.ID,
			Name:       toolRenderNameFromArgsWithCwd(call.Name, call.Arguments, cwd),
			Summary:    toolRunningSummaryFromArgs(call.Name, call.Arguments),
			Detail:     input,
			Input:      input,
			Output:     toolPreviewOutputFromArgsWithCwd(call.Name, call.Arguments, cwd),
			Code:       toolCodeFromArgsWithCwd(call.Name, call.Arguments, cwd),
			Language:   toolLanguageFromArgsWithCwd(call.Name, call.Arguments, cwd),
			NoCollapse: isWriteLikeTool(call.Name),
		},
		Expanded: isWriteLikeTool(call.Name),
	}
}

func (moduTUIToolPresenter) ResultNode(result types.ToolResultMessage, _ string) modutui.ToolNode {
	output := toolOutputFromContent(result.ToolName, result.IsError, result.Content)
	artifact := toolArtifactInfoFromDetails(result.Details)
	return modutui.ToolNode{
		Call: modutui.ToolCall{
			ID:           result.ToolCallID,
			Name:         toolRenderName(result.ToolName, nil),
			Summary:      toolDoneSummary(result.ToolName, result.IsError, output),
			Output:       toolDisplayOutput(result.ToolName, result.IsError, output),
			ArtifactID:   artifact.ID,
			ArtifactPath: artifact.Path,
			Truncated:    artifact.Truncated,
			Code:         toolCodeFromResult(result.ToolName, output),
			Language:     toolLanguageFromResult(result.ToolName),
			Error:        result.IsError,
			Done:         true,
			NoCollapse:   isWriteLikeTool(result.ToolName),
		},
		Expanded: result.IsError || isWriteLikeTool(result.ToolName),
	}
}

func toolRunningSummaryFromArgs(toolName string, args any) string {
	if strings.EqualFold(toolName, "bash") {
		return "Running shell command"
	}
	if strings.EqualFold(toolName, "read") {
		n := readFileCountFromArgs(args)
		if n == 1 {
			return "Read 1 file"
		}
		return fmt.Sprintf("Read %d files", n)
	}
	if strings.EqualFold(toolName, "grep") {
		return "Search files"
	}
	if strings.EqualFold(toolName, "find") {
		return "Find files"
	}
	if strings.EqualFold(toolName, "ls") {
		return "List directory"
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	return "Running " + name
}

func readFileCountFromArgs(args any) int {
	count := 0
	for _, key := range []string{"path", "file_path"} {
		if value, ok := mapStringValue(args, key); ok && strings.TrimSpace(value) != "" {
			count++
		}
	}
	for _, key := range []string{"paths", "file_paths"} {
		count += mapStringSliceCount(args, key)
	}
	if count == 0 {
		return 1
	}
	return count
}

func grepDoneSummary(output string) string {
	output = strings.TrimSpace(output)
	if output == "" || strings.EqualFold(output, "No matches found.") || strings.EqualFold(output, "No files found") {
		return "Found 0 matches"
	}
	if n, ok := firstIntAfterPrefix(output, "Found ", " file(s)"); ok {
		return fmt.Sprintf("Found %d files", n)
	}
	if n, ok := firstIntAfterPrefixAfterLastNewline(output, "Found ", " total occurrence(s)"); ok {
		return fmt.Sprintf("Found %d matches", n)
	}
	return fmt.Sprintf("Found %d matches", countResultLines(output))
}

func findDoneSummary(output string) string {
	output = strings.TrimSpace(output)
	if output == "" || strings.EqualFold(output, "No files found") {
		return "Found 0 files"
	}
	return fmt.Sprintf("Found %d files", countResultLines(output))
}

func lsDoneSummary(output string) string {
	output = strings.TrimSpace(output)
	if output == "" || strings.EqualFold(output, "(empty directory)") {
		return "Listed 0 entries"
	}
	return fmt.Sprintf("Listed %d entries", countResultLines(output))
}

func countResultLines(output string) int {
	count := 0
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "... (") || strings.HasPrefix(line, "(Results are truncated") {
			continue
		}
		count++
	}
	return count
}

func firstIntAfterPrefix(output, prefix, suffix string) (int, bool) {
	line, _, _ := strings.Cut(output, "\n")
	return parseIntBetween(line, prefix, suffix)
}

func firstIntAfterPrefixAfterLastNewline(output, prefix, suffix string) (int, bool) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if n, ok := parseIntBetween(strings.TrimSpace(lines[i]), prefix, suffix); ok {
			return n, true
		}
	}
	return 0, false
}

func parseIntBetween(text, prefix, suffix string) (int, bool) {
	if !strings.HasPrefix(text, prefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(text, prefix)
	if suffix != "" {
		idx := strings.Index(rest, suffix)
		if idx < 0 {
			return 0, false
		}
		rest = rest[:idx]
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	return n, err == nil
}

func toolDoneSummary(toolName string, isError bool, output string) string {
	if strings.EqualFold(toolName, "workflow") {
		if isError {
			return "Workflow failed"
		}
		if runID := moduTUIWorkflowRunIDFromNotify(output); runID != "" {
			return "Workflow started: " + runID
		}
		if first, _, ok := strings.Cut(strings.TrimSpace(output), "\n"); ok && strings.Contains(first, " completed with ") {
			return first
		}
		return "Workflow completed"
	}
	if strings.EqualFold(toolName, "bash") {
		if isError {
			return "Shell command failed"
		}
		return "Ran 1 shell command"
	}
	if strings.EqualFold(toolName, "read") && !isError {
		if strings.HasPrefix(output, "Read ") {
			return output
		}
		return "Read file"
	}
	if strings.EqualFold(toolName, "grep") {
		if isError {
			return "Search failed"
		}
		return grepDoneSummary(output)
	}
	if strings.EqualFold(toolName, "find") {
		if isError {
			return "Find failed"
		}
		return findDoneSummary(output)
	}
	if strings.EqualFold(toolName, "ls") {
		if isError {
			return "List directory failed"
		}
		return lsDoneSummary(output)
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	if isError {
		return name + " failed"
	}
	return "Ran " + name
}

func isWriteLikeTool(toolName string) bool {
	return strings.EqualFold(toolName, "write") || strings.EqualFold(toolName, "edit")
}

func toolRenderName(toolName string, result any) string {
	if strings.EqualFold(toolName, "edit") {
		return "update"
	}
	if strings.EqualFold(toolName, "write") {
		if strings.EqualFold(toolResultStringDetail(result, "type"), "update") {
			return "update"
		}
	}
	return toolName
}

func toolRenderNameFromArgsWithCwd(toolName string, args any, cwd string) string {
	if strings.EqualFold(toolName, "edit") {
		return "update"
	}
	if strings.EqualFold(toolName, "write") && writeArgsExistingFileInCwd(args, cwd) {
		return "update"
	}
	return toolName
}

func toolDisplayOutput(toolName string, isError bool, output string) string {
	if isWriteLikeTool(toolName) && !isError {
		return ""
	}
	if strings.EqualFold(toolName, "workflow") && !isError {
		if runID := moduTUIWorkflowRunIDFromNotify(output); runID != "" {
			return "Opened workflow run panel: " + runID
		}
		if first, _, ok := strings.Cut(strings.TrimSpace(output), "\n"); ok && strings.Contains(first, " completed with ") {
			return "Opened workflow run panel: latest"
		}
	}
	return output
}
