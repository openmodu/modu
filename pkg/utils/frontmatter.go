package utils

import "strings"

// ParseFrontmatter extracts simple key-value frontmatter from markdown content.
// It supports "key: value" lines and returns the trimmed body after the closing marker.
func ParseFrontmatter(content string) (map[string]string, string, bool) {
	front, body, ok := SplitFrontmatter(content)
	if !ok {
		return nil, body, false
	}
	return ParseKeyValueLines(front), body, true
}

// SplitFrontmatter extracts raw frontmatter and the trimmed body from markdown content.
func SplitFrontmatter(content string) (string, string, bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", strings.TrimSpace(content), false
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return "", strings.TrimSpace(content), false
	}

	front := normalized[4 : 4+end]
	body := strings.TrimSpace(normalized[4+end+4:])
	return front, body, true
}

// ParseKeyValueLines parses simple "key: value" lines into a map.
func ParseKeyValueLines(content string) map[string]string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	values := make(map[string]string)
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "\"'")
	}
	return values
}
