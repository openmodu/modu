package moms

import (
	"encoding/json"
	"fmt"
	"strings"
)

// toolIcon returns an emoji icon for a tool.
func toolIcon(name string) string {
	switch name {
	case "bash":
		return "🖥"
	case "read":
		return "📄"
	case "write":
		return "✏️"
	case "edit":
		return "🔧"
	case "attach":
		return "📎"
	case "find_skills", "install_skill":
		return "🔌"
	case "web_search":
		return "🔍"
	case "web_fetch":
		return "🌐"
	default:
		return "⚡"
	}
}

// toolArgsSummary extracts a one-line human-readable summary of the key argument(s).
func toolArgsSummary(name string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch name {
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			return truncateStr(cmd, 120)
		}
	case "read":
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "write":
		if path, ok := args["path"].(string); ok {
			content, _ := args["content"].(string)
			return fmt.Sprintf("%s (%d bytes)", path, len(content))
		}
	case "edit":
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "attach":
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "find_skills":
		if q, ok := args["query"].(string); ok {
			return fmt.Sprintf("query: %q", truncateStr(q, 60))
		}
	case "install_skill":
		if n, ok := args["name"].(string); ok {
			return fmt.Sprintf("name: %q", n)
		}
	case "web_search":
		if q, ok := args["query"].(string); ok {
			return fmt.Sprintf("query: %q", truncateStr(q, 80))
		}
	case "web_fetch":
		if u, ok := args["url"].(string); ok {
			return truncateStr(u, 100)
		}
	}
	// Fallback: compact JSON of all args.
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return truncateStr(string(b), 120)
}

// renderToolCardHeader renders just the "running" header for a tool (sent at start).
func renderToolCardHeader(icon, name string) string {
	return fmt.Sprintf("%s *%s* ⏳", icon, escapeMarkdownV2(name))
}

// renderToolCard renders the complete finished card (sent via edit at end).
func renderToolCard(icon, name, argsSummary, result string, durationMs int64, isError bool) string {
	statusIcon := "✅"
	if isError {
		statusIcon = "❌"
	}

	var sb strings.Builder

	// Header line: icon name status elapsed
	sb.WriteString(fmt.Sprintf("%s *%s* %s  _%s_\n",
		icon,
		escapeMarkdownV2(name),
		statusIcon,
		escapeMarkdownV2(formatDuration(durationMs)),
	))

	// Arg summary line
	if argsSummary != "" {
		sb.WriteString(fmt.Sprintf("└─ `%s`\n", escapeMarkdownV2Mono(argsSummary)))
	}

	// Separator + result
	if result != "" {
		trimmed := strings.TrimSpace(result)
		if len(trimmed) > 0 {
			sb.WriteString("```\n")
			// Escape backticks inside code block
			safe := strings.ReplaceAll(truncateStr(trimmed, 1800), "```", "'''")
			sb.WriteString(safe)
			sb.WriteString("\n```")
		}
	}

	return sb.String()
}

// formatDuration formats milliseconds as "0.3s" or "12s" or "1m5s".
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := ms / 1000
	ms = ms % 1000
	if s < 60 {
		return fmt.Sprintf("%d.%ds", s, ms/100)
	}
	m := s / 60
	s = s % 60
	return fmt.Sprintf("%dm%ds", m, s)
}

// escapeMarkdownV2 escapes all MarkdownV2 special chars outside code spans.
func escapeMarkdownV2(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]",
		"(", "\\(", ")", "\\)", "~", "\\~", "`", "\\`",
		">", "\\>", "#", "\\#", "+", "\\+", "-", "\\-",
		"=", "\\=", "|", "\\|", "{", "\\{", "}", "\\}",
		".", "\\.", "!", "\\!",
	)
	return replacer.Replace(s)
}

// escapeMarkdownV2Mono escapes chars that need escaping inside inline code backticks context.
// Inside ` ... ` only backtick itself needs escaping.
func escapeMarkdownV2Mono(s string) string {
	return strings.ReplaceAll(s, "`", "'")
}
