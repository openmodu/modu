package contextmgr

import (
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
	"github.com/openmodu/modu/pkg/types"
)

// Byte budgets for dynamically injected path-specific context.
const (
	nestedContextMaxFileBytes  = 4 * 1024
	nestedContextMaxTotalBytes = 12 * 1024
)

// OnToolExecutionEnd injects path-specific context that became relevant after a
// file-touching tool ran. The injected message is transient and is removed by
// PruneTransient when the agent turn ends.
func (m *Manager) OnToolExecutionEnd(event types.Event) {
	if m.deps.Resources == nil {
		return
	}
	switch event.ToolName {
	case "read", "edit", "write", "grep", "find", "ls":
	default:
		return
	}

	paths := extractToolPaths(event)
	if len(paths) == 0 {
		return
	}

	newContexts := m.collectNewContextFiles(paths)
	if len(newContexts) == 0 {
		return
	}

	text := formatNestedContext(paths, newContexts)
	if text == "" {
		return
	}
	m.deps.Agent.Steer(m.deps.Host.NestedContextMessage(text))
}

// PruneTransient removes host-injected transient context messages (nested
// context, explicit skill prompts, hidden extension messages) once the agent
// turn has ended, so they do not persist or accumulate.
func (m *Manager) PruneTransient() {
	state := m.deps.Agent.GetState()
	if len(state.Messages) == 0 {
		return
	}

	// Scan first to avoid allocation when there is nothing to prune.
	hasTransient := false
	for _, msg := range state.Messages {
		if m.deps.Host.IsTransient(msg) {
			hasTransient = true
			break
		}
	}
	if !hasTransient {
		return
	}

	filtered := make([]types.AgentMessage, 0, len(state.Messages))
	for _, msg := range state.Messages {
		if !m.deps.Host.IsTransient(msg) {
			filtered = append(filtered, msg)
		}
	}
	m.deps.Agent.ReplaceMessages(filtered)
}

func (m *Manager) collectNewContextFiles(paths []string) []resource.ContextFile {
	if len(paths) == 0 {
		return nil
	}

	// Load context files outside the lock: this involves filesystem I/O and
	// a git subprocess, both of which are expensive to hold a mutex across.
	var candidates []resource.ContextFile
	for _, path := range paths {
		candidates = append(candidates, m.deps.Resources.LoadContextFilesForPath(path)...)
	}

	m.contextMu.Lock()
	defer m.contextMu.Unlock()

	var out []resource.ContextFile
	for _, file := range candidates {
		if _, seen := m.loadedContexts[file.Path]; seen {
			continue
		}
		m.loadedContexts[file.Path] = struct{}{}
		out = append(out, file)
	}
	return out
}

func extractToolPaths(event types.Event) []string {
	var paths []string
	seen := make(map[string]struct{})
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	if result, ok := event.Result.(types.ToolResult); ok {
		if details, ok := result.Details.(map[string]any); ok {
			collectToolPathsFromDetails(details, add)
		}
	}
	if args, ok := event.Args.(map[string]any); ok {
		collectToolPathsFromDetails(args, add)
	}
	return paths
}

func collectToolPathsFromDetails(details map[string]any, add func(string)) {
	if path, _ := details["path"].(string); path != "" {
		add(path)
	}
	switch matched := details["matched_paths"].(type) {
	case []string:
		for _, path := range matched {
			add(path)
		}
	case []any:
		for _, raw := range matched {
			if path, ok := raw.(string); ok {
				add(path)
			}
		}
	}
}

func formatNestedContext(targetPaths []string, files []resource.ContextFile) string {
	if len(files) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, "Additional path-specific instructions became relevant after accessing:")
	for _, path := range targetPaths {
		parts = append(parts, "- "+path)
	}

	remaining := nestedContextMaxTotalBytes
	for _, file := range files {
		if remaining <= 0 {
			break
		}
		content := strings.TrimSpace(file.Content)
		if content == "" {
			continue
		}
		limit := min(nestedContextMaxFileBytes, remaining)
		if len(content) > limit {
			content = truncateWithNotice(content, limit, file.Name)
		}
		if len(content) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("# Path Context: %s\n%s", file.Name, content))
		remaining -= len(content)
	}

	if len(parts) == 1 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func truncateWithNotice(content string, maxBytes int, label string) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	const ellipsis = "\n\n...[truncated for context budget]"
	keep := maxBytes - len(ellipsis)
	if keep < 0 {
		keep = 0
	}
	content = strings.TrimSpace(content[:keep])
	if label != "" {
		return content + ellipsis + " (" + label + ")"
	}
	return content + ellipsis
}
