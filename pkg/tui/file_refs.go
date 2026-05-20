package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	gotui "github.com/grindlemire/go-tui"
)

const (
	fileReferenceVisibleRows = 10
	maxFileReferenceMatches  = 80
	maxReferencedFileBytes   = 48 * 1024
	maxReferencedTotalBytes  = 128 * 1024
)

type fileSuggestion struct {
	Path        string
	Description string
	IsDir       bool
}

type inputToken struct {
	Start int
	End   int
	Text  string
}

func (r *goTUIRoot) updateInputSuggestions() {
	r.updateSlashMatches()
	if len(r.slashMatches) > 0 {
		r.fileMatches = nil
		r.fileMatchIdx = 0
		return
	}
	r.updateFileMatches()
}

func (r *goTUIRoot) updateFileMatches() {
	token, ok := r.currentFileReferenceToken()
	if !ok {
		r.fileMatches = nil
		r.fileMatchIdx = 0
		r.fileScrollOffset = 0
		return
	}
	query := strings.TrimPrefix(token.Text, "@")
	matches := r.findFileReferenceMatches(query)
	r.fileMatches = matches
	if r.fileMatchIdx >= len(r.fileMatches) {
		r.fileMatchIdx = 0
	}
	r.adjustFileScroll()
}

func (r *goTUIRoot) currentFileReferenceToken() (inputToken, bool) {
	token, ok := currentInputToken(r.draft.Get(), r.cursor)
	if !ok || !strings.HasPrefix(token.Text, "@") {
		return inputToken{}, false
	}
	if strings.ContainsAny(token.Text, "\n\r\t ") {
		return inputToken{}, false
	}
	return token, true
}

func currentInputToken(text string, cursor int) (inputToken, bool) {
	rs := []rune(text)
	cursor = clampInt(cursor, 0, len(rs))
	start := cursor
	for start > 0 && !unicode.IsSpace(rs[start-1]) {
		start--
	}
	end := cursor
	for end < len(rs) && !unicode.IsSpace(rs[end]) {
		end++
	}
	if start == end {
		return inputToken{}, false
	}
	return inputToken{Start: start, End: end, Text: string(rs[start:end])}, true
}

func (r *goTUIRoot) findFileReferenceMatches(query string) []fileSuggestion {
	cwd := r.currentWorkingDir()
	if cwd == "" {
		return nil
	}
	query = strings.TrimSpace(strings.TrimPrefix(query, "/"))
	queryLower := strings.ToLower(query)
	var matches []fileSuggestion
	_ = filepath.WalkDir(cwd, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != cwd && entry.IsDir() && shouldSkipFileReferenceDir(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(cwd, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if queryLower != "" && !fileReferenceMatch(rel, queryLower) {
			return nil
		}
		info, _ := entry.Info()
		desc := "file"
		if info != nil {
			desc = formatFileReferenceSize(info.Size())
		}
		matches = append(matches, fileSuggestion{Path: rel, Description: desc})
		if len(matches) >= maxFileReferenceMatches {
			return filepath.SkipAll
		}
		return nil
	})
	sort.SliceStable(matches, func(i, j int) bool {
		return fileReferenceScore(matches[i].Path, queryLower) < fileReferenceScore(matches[j].Path, queryLower)
	})
	return matches
}

func shouldSkipFileReferenceDir(name string) bool {
	switch name {
	case ".git", ".coding_agent", "node_modules", "vendor", "dist", "build", ".next", ".cache":
		return true
	default:
		return false
	}
}

func fileReferenceMatch(path, query string) bool {
	path = strings.ToLower(path)
	if strings.Contains(path, query) {
		return true
	}
	parts := strings.Fields(strings.NewReplacer("/", " ", "-", " ", "_", " ", ".", " ").Replace(query))
	for _, part := range parts {
		if part != "" && !strings.Contains(path, part) {
			return false
		}
	}
	return len(parts) > 0
}

func fileReferenceScore(path, query string) int {
	path = strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	if query == "" {
		return strings.Count(path, "/")
	}
	switch {
	case base == query:
		return 0
	case strings.HasPrefix(base, query):
		return 1
	case strings.Contains(base, query):
		return 2
	case strings.HasPrefix(path, query):
		return 3
	default:
		return 10 + strings.Count(path, "/")
	}
}

func formatFileReferenceSize(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		return fmt.Sprintf("%d KB", size/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
}

func (r *goTUIRoot) completeFileMatch() bool {
	if len(r.fileMatches) == 0 {
		return false
	}
	token, ok := r.currentFileReferenceToken()
	if !ok {
		return false
	}
	chosen := "@" + r.fileMatches[r.fileMatchIdx].Path
	rs := []rune(r.draft.Get())
	replacement := []rune(chosen)
	next := append([]rune{}, rs[:token.Start]...)
	next = append(next, replacement...)
	next = append(next, rs[token.End:]...)
	r.draft.Set(string(next))
	r.cursor = token.Start + len(replacement)
	r.updateInputSuggestions()
	r.bump()
	return true
}

func (r *goTUIRoot) completePathToken() bool {
	token, ok := currentInputToken(r.draft.Get(), r.cursor)
	if !ok || strings.HasPrefix(token.Text, "@") || !looksLikePathToken(token.Text) {
		return false
	}
	cwd := r.currentWorkingDir()
	if cwd == "" {
		return false
	}
	prefix := token.Text
	dirPart, filePart := filepath.Split(prefix)
	searchDir := dirPart
	if searchDir == "" {
		searchDir = "."
	}
	if strings.HasPrefix(searchDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		searchDir = filepath.Join(home, strings.TrimPrefix(searchDir, "~"))
	} else if !filepath.IsAbs(searchDir) {
		searchDir = filepath.Join(cwd, searchDir)
	}
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return false
	}
	var candidates []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), filePart) {
			suffix := ""
			if entry.IsDir() {
				suffix = string(filepath.Separator)
			}
			candidates = append(candidates, dirPart+entry.Name()+suffix)
		}
	}
	if len(candidates) == 0 {
		return false
	}
	sort.Strings(candidates)
	replacement := candidates[0]
	if len(candidates) > 1 {
		replacement = longestCommonPrefix(candidates)
		if replacement == token.Text {
			return false
		}
	}
	rs := []rune(r.draft.Get())
	rep := []rune(replacement)
	next := append([]rune{}, rs[:token.Start]...)
	next = append(next, rep...)
	next = append(next, rs[token.End:]...)
	r.draft.Set(string(next))
	r.cursor = token.Start + len(rep)
	r.updateInputSuggestions()
	r.bump()
	return true
}

func looksLikePathToken(token string) bool {
	return strings.HasPrefix(token, "./") ||
		strings.HasPrefix(token, "../") ||
		strings.HasPrefix(token, "~/") ||
		strings.HasPrefix(token, "/") ||
		strings.Contains(token, "/")
}

func longestCommonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func (r *goTUIRoot) adjustFileScroll() {
	if len(r.fileMatches) <= fileReferenceVisibleRows {
		r.fileScrollOffset = 0
		return
	}
	if r.fileMatchIdx < r.fileScrollOffset {
		r.fileScrollOffset = r.fileMatchIdx
	} else if r.fileMatchIdx >= r.fileScrollOffset+fileReferenceVisibleRows {
		r.fileScrollOffset = r.fileMatchIdx - fileReferenceVisibleRows + 1
	}
}

func (r *goTUIRoot) moveFileMatch(delta int) bool {
	if len(r.fileMatches) == 0 {
		return false
	}
	r.fileMatchIdx = (r.fileMatchIdx + delta + len(r.fileMatches)) % len(r.fileMatches)
	r.adjustFileScroll()
	r.bump()
	return true
}

func (r *goTUIRoot) renderFileSuggestions() []*gotui.Element {
	if len(r.fileMatches) == 0 {
		return nil
	}
	end := r.fileScrollOffset + fileReferenceVisibleRows
	if end > len(r.fileMatches) {
		end = len(r.fileMatches)
	}
	rows := make([]*gotui.Element, 0, end-r.fileScrollOffset+1)
	for i := r.fileScrollOffset; i < end; i++ {
		match := r.fileMatches[i]
		selected := i == r.fileMatchIdx
		prefix := "  "
		if selected {
			prefix = "❯ "
		}
		text := prefix + "@" + match.Path
		if match.Description != "" {
			text += "  " + match.Description
		}
		if i == r.fileScrollOffset && r.fileScrollOffset > 0 {
			text += "  ↑"
		} else if i == end-1 && end < len(r.fileMatches) {
			text += "  ↓"
		}
		style := gotui.NewStyle().Dim()
		if selected {
			style = gotui.NewStyle().Foreground(gotui.Cyan).Bold()
		}
		rows = append(rows, gotui.New(
			gotui.WithText(text),
			gotui.WithTextStyle(style),
			gotui.WithFlexShrink(0),
		))
	}
	rows = append(rows, gotui.New(
		gotui.WithText("  Tab/Enter complete file reference"),
		gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow).Italic()),
		gotui.WithFlexShrink(0),
	))
	return rows
}

func (r *goTUIRoot) currentWorkingDir() string {
	if r.session == nil {
		return ""
	}
	return r.session.GetContextInfo().Cwd
}

func (r *goTUIRoot) expandFileReferencesForPrompt(text string) string {
	cwd := r.currentWorkingDir()
	if cwd == "" || !strings.Contains(text, "@") {
		return text
	}
	refs := collectPromptFileReferences(text)
	if len(refs) == 0 {
		return text
	}
	seen := make(map[string]struct{})
	var parts []string
	remaining := maxReferencedTotalBytes
	for _, ref := range refs {
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		abs, ok := safePromptReferencePath(cwd, ref)
		if !ok {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() == 0 {
			continue
		}
		limit := min(maxReferencedFileBytes, remaining)
		if limit <= 0 {
			break
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if len(data) > limit {
			data = data[:limit]
		}
		if !looksTextLike(data) {
			continue
		}
		remaining -= len(data)
		content := strings.TrimRight(string(data), "\n")
		if info.Size() > int64(len(data)) {
			content += "\n... (truncated)"
		}
		parts = append(parts, fmt.Sprintf("# @%s\n```text\n%s\n```", ref, content))
	}
	if len(parts) == 0 {
		return text
	}
	return strings.TrimSpace(text) + "\n\nReferenced files:\n\n" + strings.Join(parts, "\n\n")
}

func collectPromptFileReferences(text string) []string {
	var refs []string
	for _, raw := range strings.Fields(text) {
		if !strings.HasPrefix(raw, "@") || len(raw) == 1 {
			continue
		}
		ref := strings.Trim(strings.TrimPrefix(raw, "@"), ".,;:!?)]}\"'")
		ref = strings.TrimPrefix(ref, "./")
		if ref != "" {
			refs = append(refs, filepath.ToSlash(ref))
		}
	}
	return refs
}

func safePromptReferencePath(cwd, ref string) (string, bool) {
	if filepath.IsAbs(ref) || strings.HasPrefix(ref, "..") {
		return "", false
	}
	abs := filepath.Clean(filepath.Join(cwd, filepath.FromSlash(ref)))
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return abs, true
}

func looksTextLike(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}
