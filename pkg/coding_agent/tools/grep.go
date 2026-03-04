package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/providers"
)

const defaultGrepLimit = 100

// GrepTool implements the content search tool.
type GrepTool struct {
	cwd string
}

func NewGrepTool(cwd string) *GrepTool {
	return &GrepTool{cwd: cwd}
}

func (t *GrepTool) Name() string  { return "grep" }
func (t *GrepTool) Label() string { return "Search Content" }
func (t *GrepTool) Description() string {
	return `Search file contents using regex patterns. Uses ripgrep (rg) if available, falls back to built-in implementation. Respects .gitignore. Returns matching lines with file paths and line numbers.`
}

func (t *GrepTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory or file to search in (default: cwd)",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "Glob pattern to filter files (e.g., '*.go', '*.ts')",
			},
			"ignore_case": map[string]any{
				"type":        "boolean",
				"description": "Case insensitive search",
			},
			"literal": map[string]any{
				"type":        "boolean",
				"description": "Treat pattern as literal string, not regex",
			},
			"context": map[string]any{
				"type":        "integer",
				"description": "Number of context lines before and after each match",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results (default 100)",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return errorResult("pattern is required"), nil
	}

	searchPath := t.cwd
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = ResolveToCwd(p, t.cwd)
	}

	glob, _ := args["glob"].(string)
	ignoreCase, _ := args["ignore_case"].(bool)
	literal, _ := args["literal"].(bool)
	contextLines := 0
	if v, ok := args["context"]; ok {
		contextLines = toInt(v)
	}
	limit := defaultGrepLimit
	if v, ok := args["limit"]; ok {
		limit = toInt(v)
		if limit <= 0 {
			limit = defaultGrepLimit
		}
	}

	// Try ripgrep first
	if rgPath, err := exec.LookPath("rg"); err == nil {
		return t.executeRipgrep(ctx, rgPath, pattern, searchPath, glob, ignoreCase, literal, contextLines, limit)
	}

	// Fallback to built-in
	return t.executeBuiltin(ctx, pattern, searchPath, glob, ignoreCase, literal, contextLines, limit)
}

func (t *GrepTool) executeRipgrep(ctx context.Context, rgPath, pattern, searchPath, glob string, ignoreCase, literal bool, contextLines, limit int) (agent.AgentToolResult, error) {
	args := []string{
		"--line-number",
		"--no-heading",
		"--color", "never",
		"--max-count", fmt.Sprintf("%d", limit),
	}

	if ignoreCase {
		args = append(args, "--ignore-case")
	}
	if literal {
		args = append(args, "--fixed-strings")
	}
	if contextLines > 0 {
		args = append(args, fmt.Sprintf("--context=%d", contextLines))
	}
	if glob != "" {
		args = append(args, "--glob", glob)
	}

	args = append(args, pattern, searchPath)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = t.cwd

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				// No matches
				return agent.AgentToolResult{
					Content: []providers.ContentBlock{
						providers.TextContent{Type: "text", Text: "No matches found."},
					},
				}, nil
			}
		}
		return errorResult(fmt.Sprintf("ripgrep error: %v", err)), nil
	}

	result := string(output)

	// Truncate long lines
	lines := strings.Split(result, "\n")
	for i, line := range lines {
		lines[i] = TruncateLine(line, GrepMaxLineLen)
	}
	result = strings.Join(lines, "\n")

	// Make paths relative
	result = strings.ReplaceAll(result, searchPath+"/", "")

	if result == "" {
		result = "No matches found."
	}

	return agent.AgentToolResult{
		Content: []providers.ContentBlock{
			providers.TextContent{Type: "text", Text: result},
		},
	}, nil
}

func (t *GrepTool) executeBuiltin(ctx context.Context, pattern, searchPath, glob string, ignoreCase, literal bool, contextLines, limit int) (agent.AgentToolResult, error) {
	if literal {
		pattern = regexp.QuoteMeta(pattern)
	}

	flags := ""
	if ignoreCase {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return errorResult(fmt.Sprintf("invalid regex pattern: %v", err)), nil
	}

	var results []string
	matchCount := 0

	err = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if matchCount >= limit {
			return filepath.SkipAll
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == ".svn" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		// Apply glob filter
		if glob != "" {
			matched, _ := filepath.Match(glob, info.Name())
			if !matched {
				return nil
			}
		}

		// Skip binary files (simple heuristic: check first 512 bytes)
		if info.Size() > 10*1024*1024 { // Skip files > 10MB
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		relPath, _ := filepath.Rel(searchPath, path)
		if relPath == "" {
			relPath = path
		}

		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				matchCount++
				if matchCount > limit {
					break
				}
				truncatedLine := TruncateLine(line, GrepMaxLineLen)
				results = append(results, fmt.Sprintf("%s:%d:%s", relPath, lineNum, truncatedLine))
			}
		}

		return nil
	})

	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		return errorResult(fmt.Sprintf("search error: %v", err)), nil
	}

	if len(results) == 0 {
		return agent.AgentToolResult{
			Content: []providers.ContentBlock{
				providers.TextContent{Type: "text", Text: "No matches found."},
			},
		}, nil
	}

	text := strings.Join(results, "\n")
	if matchCount > limit {
		text += fmt.Sprintf("\n\n... (results limited to %d matches)", limit)
	}

	return agent.AgentToolResult{
		Content: []providers.ContentBlock{
			providers.TextContent{Type: "text", Text: text},
		},
	}, nil
}
