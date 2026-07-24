package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/openmodu/modu/pkg/types"
)

func firstStringValue(v any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := mapStringValue(v, key); ok {
			return value, true
		}
	}
	return "", false
}

func toolResultStringDetail(result any, key string) string {
	details := toolResultDetails(result)
	if details == nil {
		return ""
	}
	if value, ok := details[key].(string); ok {
		return value
	}
	return ""
}

func toolResultDetails(result any) map[string]any {
	switch r := result.(type) {
	case types.ToolResult:
		if m, ok := r.Details.(map[string]any); ok {
			return m
		}
	case *types.ToolResult:
		if r != nil {
			if m, ok := r.Details.(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

type toolArtifactInfo struct {
	ID        string
	Path      string
	Truncated bool
}

func toolArtifactInfoFromResult(result any) toolArtifactInfo {
	return toolArtifactInfoFromDetails(toolResultDetails(result))
}

func toolArtifactInfoFromDetails(details any) toolArtifactInfo {
	m, ok := details.(map[string]any)
	if !ok {
		return toolArtifactInfo{}
	}
	output, ok := m["output"].(map[string]any)
	if !ok {
		return toolArtifactInfo{}
	}
	var info toolArtifactInfo
	if value, ok := output["artifactId"].(string); ok {
		info.ID = value
	}
	if value, ok := output["artifactPath"].(string); ok {
		info.Path = value
	}
	if value, ok := output["truncated"].(bool); ok {
		info.Truncated = value
	}
	return info
}

func languageFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".jsx":
		return "jsx"
	case ".json":
		return "json"
	case ".md", ".markdown":
		return "markdown"
	case ".py":
		return "python"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".yaml", ".yml":
		return "yaml"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".sql":
		return "sql"
	default:
		return ""
	}
}

func toolInputFromArgs(toolName string, args any) string {
	if strings.EqualFold(toolName, "bash") {
		if command, ok := mapStringValue(args, "command"); ok {
			return command
		}
	}
	if strings.EqualFold(toolName, "read") {
		return readInputFromArgs(args)
	}
	if isWriteLikeTool(toolName) {
		if path, ok := firstStringValue(args, "path", "file_path"); ok {
			return path
		}
	}
	return formatJSON(args)
}

func mapStringValue(v any, key string) (string, bool) {
	switch m := v.(type) {
	case map[string]any:
		value, ok := m[key].(string)
		return value, ok
	case map[string]string:
		value, ok := m[key]
		return value, ok
	default:
		return "", false
	}
}

func mapStringSliceCount(v any, key string) int {
	var raw any
	switch m := v.(type) {
	case map[string]any:
		raw = m[key]
	case map[string][]string:
		raw = m[key]
	case map[string]string:
		return 0
	default:
		return 0
	}
	switch values := raw.(type) {
	case []string:
		count := 0
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				count++
			}
		}
		return count
	case []any:
		count := 0
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				count++
			}
		}
		return count
	default:
		return 0
	}
}

func toolOutputFromResult(toolName string, isError bool, result any) string {
	switch r := result.(type) {
	case types.ToolResult:
		if text := toolOutputFromContent(toolName, isError || r.IsError, r.Content); text != "" {
			return text
		}
		return formatJSON(r.Details)
	case *types.ToolResult:
		if r == nil {
			return ""
		}
		if text := toolOutputFromContent(toolName, isError || r.IsError, r.Content); text != "" {
			return text
		}
		return formatJSON(r.Details)
	default:
		return formatJSON(result)
	}
}

func toolOutputFromContent(toolName string, isError bool, content []types.ContentBlock) string {
	text := contentBlocksText(content)
	if strings.EqualFold(toolName, "read") && !isError {
		return readOutputSummary(text)
	}
	return text
}

func readInputFromArgs(args any) string {
	path, _ := mapStringValue(args, "path")
	if path == "" {
		return formatJSON(args)
	}
	start := intArgValue(args, "offset", 1)
	limit := intArgValue(args, "limit", 0)
	if limit > 0 {
		return fmt.Sprintf("%s · lines %d-%d", path, start, start+limit-1)
	}
	if start > 1 {
		return fmt.Sprintf("%s · lines %d-", path, start)
	}
	return path
}

func readOutputSummary(text string) string {
	count := 0
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if readResultLine(line) {
			count++
		}
	}
	if count == 1 {
		return "Read 1 line"
	}
	return fmt.Sprintf("Read %d lines", count)
}

func readResultLine(line string) bool {
	tab := strings.IndexByte(line, '\t')
	if tab <= 0 {
		return false
	}
	for _, r := range line[:tab] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func intArgValue(v any, key string, fallback int) int {
	switch m := v.(type) {
	case map[string]any:
		return intValue(m[key], fallback)
	case map[string]string:
		return intValue(m[key], fallback)
	default:
		return fallback
	}
}

func intValue(v any, fallback int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		var parsed int
		if _, err := fmt.Sscanf(n, "%d", &parsed); err == nil {
			return parsed
		}
	}
	return fallback
}
