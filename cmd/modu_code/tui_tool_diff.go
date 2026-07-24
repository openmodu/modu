package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func toolPreviewOutputFromArgsWithCwd(toolName string, args any, cwd string) string {
	if strings.EqualFold(toolName, "edit") {
		oldText, _ := firstStringValue(args, "old_text", "old_string")
		newText, _ := firstStringValue(args, "new_text", "new_string")
		removed := countContentLines(oldText)
		added := countContentLines(newText)
		return fmt.Sprintf("Added %d lines, removed %d lines", added, removed)
	}
	if strings.EqualFold(toolName, "write") {
		content, ok := mapStringValue(args, "content")
		if !ok {
			return ""
		}
		if oldContent, ok := previewFileContentFromArgs(args, cwd); ok {
			added, removed := changedLineCounts(oldContent, content)
			if added == 0 && removed == 0 {
				return "No changes"
			}
			return fmt.Sprintf("Added %d lines, removed %d lines", added, removed)
		}
		lines := countContentLines(content)
		bytes := len([]byte(content))
		if lines == 1 {
			return fmt.Sprintf("Wrote 1 line, %d bytes", bytes)
		}
		return fmt.Sprintf("Wrote %d lines, %d bytes", lines, bytes)
	}
	return ""
}

func toolCodeFromArgsWithCwd(toolName string, args any, cwd string) string {
	if strings.EqualFold(toolName, "write") {
		content, _ := mapStringValue(args, "content")
		if diff := contextualWriteDiffFromArgs(args, content, cwd); diff != "" {
			return diff
		}
		return numberedContent(content)
	}
	if strings.EqualFold(toolName, "edit") {
		oldText, _ := firstStringValue(args, "old_text", "old_string")
		newText, _ := firstStringValue(args, "new_text", "new_string")
		if diff := contextualEditDiffFromArgs(args, oldText, newText, cwd); diff != "" {
			return diff
		}
		// Without the target file we cannot show truthful file line numbers.
		// Successful edit results carry the tool-generated numbered diff and
		// will populate the block when execution completes.
		return ""
	}
	return ""
}

func toolCodeFromResult(toolName string, output string) string {
	if strings.EqualFold(toolName, "edit") {
		return editDiffFromOutput(output)
	}
	return ""
}

func toolLanguageFromResult(toolName string) string {
	if strings.EqualFold(toolName, "edit") {
		return "diff"
	}
	return ""
}

func toolLanguageFromArgsWithCwd(toolName string, args any, cwd string) string {
	if strings.EqualFold(toolName, "edit") {
		return "diff"
	}
	if strings.EqualFold(toolName, "write") {
		if writeArgsExistingFileInCwd(args, cwd) {
			return "diff"
		}
		path, _ := firstStringValue(args, "path", "file_path")
		return languageFromPath(path)
	}
	return ""
}

func editDiffFromOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	if idx := strings.Index(output, "\n\n--- "); idx >= 0 {
		return strings.TrimSpace(output[idx+2:])
	}
	if strings.HasPrefix(output, "--- ") {
		return output
	}
	return ""
}

func contextualEditDiffFromArgs(args any, oldText, newText, cwd string) string {
	if strings.TrimSpace(oldText) == "" {
		return ""
	}
	path, _ := firstStringValue(args, "path", "file_path")
	fileContent, ok := previewFileContentInCwd(path, cwd)
	if !ok {
		return ""
	}
	return replacementPreviewDiff(path, fileContent, oldText, newText)
}

func contextualWriteDiffFromArgs(args any, newContent string, cwd string) string {
	path, _ := firstStringValue(args, "path", "file_path")
	oldContent, ok := previewFileContentInCwd(path, cwd)
	if !ok || oldContent == newContent {
		return ""
	}
	return contentPreviewDiff(path, oldContent, newContent)
}

func previewFileContentFromArgs(args any, cwd string) (string, bool) {
	path, _ := firstStringValue(args, "path", "file_path")
	return previewFileContentInCwd(path, cwd)
}

func previewFileContentInCwd(path, cwd string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		if strings.TrimSpace(cwd) != "" {
			path = filepath.Join(cwd, path)
		} else {
			path = filepath.Clean(path)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func writeArgsExistingFileInCwd(args any, cwd string) bool {
	path, _ := firstStringValue(args, "path", "file_path")
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		if strings.TrimSpace(cwd) != "" {
			path = filepath.Join(cwd, path)
		} else {
			path = filepath.Clean(path)
		}
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func replacementPreviewDiff(path, fileContent, oldText, newText string) string {
	idx := strings.Index(fileContent, oldText)
	if idx < 0 {
		return ""
	}
	startLine := strings.Count(fileContent[:idx], "\n") + 1
	oldLines := splitContentLines(oldText)
	newLines := splitContentLines(newText)
	fileLines := splitContentLines(fileContent)
	return localizedPreviewDiff(path, fileLines, startLine, oldLines, newLines)
}

func contentPreviewDiff(path, oldContent, newContent string) string {
	oldLines := splitContentLines(oldContent)
	newLines := splitContentLines(newContent)
	prefix, suffix := commonLineWindow(oldLines, newLines)
	removed := oldLines[prefix : len(oldLines)-suffix]
	added := newLines[prefix : len(newLines)-suffix]
	return localizedPreviewDiff(path, oldLines, prefix+1, removed, added)
}

func localizedPreviewDiff(path string, fileLines []string, startLine int, removed, added []string) string {
	const contextLines = 3
	if len(removed) == 0 && len(added) == 0 {
		return ""
	}
	if startLine < 1 {
		startLine = 1
	}
	contextStart := startLine - 1 - contextLines
	if contextStart < 0 {
		contextStart = 0
	}
	afterStart := startLine - 1 + len(removed)
	contextEnd := afterStart + contextLines
	if contextEnd > len(fileLines) {
		contextEnd = len(fileLines)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", path)
	fmt.Fprintf(&sb, "+++ %s\n", path)
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", startLine, len(removed), startLine, len(added))
	for i := contextStart; i < startLine-1 && i < len(fileLines); i++ {
		fmt.Fprintf(&sb, "  %d  %s\n", i+1, fileLines[i])
	}
	for i, line := range removed {
		fmt.Fprintf(&sb, "- %d  %s\n", startLine+i, line)
	}
	for i, line := range added {
		fmt.Fprintf(&sb, "+ %d  %s\n", startLine+i, line)
	}
	for i := afterStart; i < contextEnd && i < len(fileLines); i++ {
		fmt.Fprintf(&sb, "  %d  %s\n", i+1, fileLines[i])
	}
	return strings.TrimRight(sb.String(), "\n")
}

func commonLineWindow(oldLines, newLines []string) (prefix, suffix int) {
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	return prefix, suffix
}

func changedLineCounts(oldContent, newContent string) (added, removed int) {
	oldLines := splitContentLines(oldContent)
	newLines := splitContentLines(newContent)
	prefix, suffix := commonLineWindow(oldLines, newLines)
	return len(newLines) - prefix - suffix, len(oldLines) - prefix - suffix
}

func numberedContent(content string) string {
	lines := splitContentLines(content)
	if len(lines) == 0 {
		return ""
	}
	width := len(fmt.Sprintf("%d", len(lines)))
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		out = append(out, fmt.Sprintf("%*d  %s", width, i+1, line))
	}
	return strings.Join(out, "\n")
}

func splitContentLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(text, "\n"), "\n")
}

func countContentLines(text string) int {
	return len(splitContentLines(text))
}
