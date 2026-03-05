package tools

import (
	"fmt"
	"strings"
)

const (
	DefaultMaxLines   = 2000
	DefaultMaxBytes   = 50 * 1024 // 50KB
	DefaultMaxLineLen = 500
	GrepMaxLineLen    = 500
	BashMaxLines      = 2000
	ReadMaxLines      = 2000
)

// TruncateOptions configures truncation behavior.
type TruncateOptions struct {
	MaxLines int
	MaxBytes int
}

// TruncationResult holds the result of a truncation operation.
type TruncationResult struct {
	Content      string
	WasTruncated bool
	OrigLines    int
	KeptLines    int
	Message      string
}

// TruncateHead keeps the first N lines, truncating from the bottom.
// Used primarily for read operations.
func TruncateHead(content string, opts TruncateOptions) TruncationResult {
	if opts.MaxLines <= 0 {
		opts.MaxLines = DefaultMaxLines
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}

	lines := strings.Split(content, "\n")
	origLines := len(lines)

	// Check byte limit
	if len(content) > opts.MaxBytes {
		byteContent := content[:opts.MaxBytes]
		// Find last complete line
		lastNL := strings.LastIndex(byteContent, "\n")
		if lastNL > 0 {
			byteContent = byteContent[:lastNL]
		}
		lines = strings.Split(byteContent, "\n")
	}

	if len(lines) > opts.MaxLines {
		lines = lines[:opts.MaxLines]
		kept := len(lines)
		result := strings.Join(lines, "\n")
		return TruncationResult{
			Content:      result,
			WasTruncated: true,
			OrigLines:    origLines,
			KeptLines:    kept,
			Message:      fmt.Sprintf("... (truncated %d lines, showing first %d)", origLines-kept, kept),
		}
	}

	if len(lines) < origLines {
		kept := len(lines)
		return TruncationResult{
			Content:      strings.Join(lines, "\n"),
			WasTruncated: true,
			OrigLines:    origLines,
			KeptLines:    kept,
			Message:      fmt.Sprintf("... (truncated due to size limit, showing first %d lines)", kept),
		}
	}

	return TruncationResult{
		Content:      content,
		WasTruncated: false,
		OrigLines:    origLines,
		KeptLines:    origLines,
	}
}

// TruncateTail keeps the last N lines, truncating from the top.
// Used primarily for bash output.
func TruncateTail(content string, opts TruncateOptions) TruncationResult {
	if opts.MaxLines <= 0 {
		opts.MaxLines = DefaultMaxLines
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}

	lines := strings.Split(content, "\n")
	origLines := len(lines)

	// Check byte limit first
	if len(content) > opts.MaxBytes {
		byteContent := content[len(content)-opts.MaxBytes:]
		// Find first complete line
		firstNL := strings.Index(byteContent, "\n")
		if firstNL >= 0 {
			byteContent = byteContent[firstNL+1:]
		}
		lines = strings.Split(byteContent, "\n")
	}

	if len(lines) > opts.MaxLines {
		start := len(lines) - opts.MaxLines
		lines = lines[start:]
		kept := len(lines)
		result := strings.Join(lines, "\n")
		return TruncationResult{
			Content:      result,
			WasTruncated: true,
			OrigLines:    origLines,
			KeptLines:    kept,
			Message:      fmt.Sprintf("(truncated %d lines, showing last %d) ...\n", origLines-kept, kept),
		}
	}

	if len(lines) < origLines {
		kept := len(lines)
		return TruncationResult{
			Content:      strings.Join(lines, "\n"),
			WasTruncated: true,
			OrigLines:    origLines,
			KeptLines:    kept,
			Message:      fmt.Sprintf("(truncated due to size limit, showing last %d lines) ...\n", kept),
		}
	}

	return TruncationResult{
		Content:      content,
		WasTruncated: false,
		OrigLines:    origLines,
		KeptLines:    origLines,
	}
}

// TruncateLine truncates a single line to maxChars characters.
func TruncateLine(line string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = DefaultMaxLineLen
	}
	runes := []rune(line)
	if len(runes) <= maxChars {
		return line
	}
	return string(runes[:maxChars]) + "..."
}

// FormatSize converts bytes to human-readable format.
func FormatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
