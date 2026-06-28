package find

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

const defaultFindLimit = 1000

// FindTool implements the file search tool using glob patterns.
type FindTool struct {
	cwd string
}

func NewTool(cwd string) types.Tool {
	return &FindTool{cwd: cwd}
}

func (t *FindTool) Name() string  { return "find" }
func (t *FindTool) Label() string { return "Find Files" }
func (t *FindTool) Description() string {
	return `Find files by glob pattern.

Usage:
- Use this tool when you need to locate files by name or path pattern; prefer it over running shell find or ls through bash.
- Supports patterns such as "**/*.go", "src/*.ts", or "*_test.go".
- Uses fd when available and falls back to a built-in filesystem walk.
- Respects .gitignore when using fd. The built-in fallback skips common generated or vendor directories.
- Returns relative file paths. Narrow broad searches with path and pattern.`
}

func (t *FindTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Glob pattern to match files (e.g., '**/*.go', 'src/*.ts')",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory to search in (default: cwd)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results (default 1000)",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *FindTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return common.ErrorResult("pattern is required"), nil
	}

	searchPath := t.cwd
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = common.ResolveToCwd(p, t.cwd)
	}

	limit := defaultFindLimit
	if v, ok := args["limit"]; ok {
		limit = common.ToInt(v)
		if limit <= 0 {
			limit = defaultFindLimit
		}
	}

	// Try fd first
	if fdPath, err := exec.LookPath("fd"); err == nil {
		return t.executeFd(ctx, fdPath, pattern, searchPath, limit)
	}

	// Fallback to built-in
	return t.executeBuiltin(ctx, pattern, searchPath, limit)
}

func (t *FindTool) executeFd(ctx context.Context, fdPath, pattern, searchPath string, limit int) (types.ToolResult, error) {
	args := []string{
		"--type", "f",
		"--color", "never",
		"--max-results", fmt.Sprintf("%d", limit),
		"--glob", pattern,
	}

	cmd := exec.CommandContext(ctx, fdPath, args...)
	cmd.Dir = searchPath

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return types.ToolResult{
				Content: []types.ContentBlock{
					&types.TextContent{Type: "text", Text: "No files found."},
				},
			}, nil
		}
		// fd might not support all options, fall back
		return t.executeBuiltin(ctx, pattern, searchPath, limit)
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "No files found."},
			},
		}, nil
	}

	lines := strings.Split(result, "\n")
	sort.Strings(lines)
	if len(lines) >= limit {
		result = strings.Join(lines, "\n") + fmt.Sprintf("\n\n... (limited to %d results)", limit)
	} else {
		result = strings.Join(lines, "\n")
	}
	matchedPaths := make([]string, 0, min(len(lines), 20))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matchedPaths = append(matchedPaths, filepath.Join(searchPath, line))
		if len(matchedPaths) >= 20 {
			break
		}
	}

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: result},
		},
		Details: map[string]any{
			"path":          searchPath,
			"matched_paths": matchedPaths,
		},
	}, nil
}

func (t *FindTool) executeBuiltin(ctx context.Context, pattern, searchPath string, limit int) (types.ToolResult, error) {
	var results []string
	skipDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		".svn":         true,
		"vendor":       true,
		"__pycache__":  true,
	}

	err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(results) >= limit {
			return filepath.SkipAll
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(searchPath, path)
		if relPath == "" {
			relPath = path
		}

		// Match against the pattern
		matched, _ := filepath.Match(pattern, info.Name())
		if !matched {
			// Try matching full relative path with doublestar-like behavior
			matched, _ = filepath.Match(pattern, relPath)
		}
		if !matched && strings.Contains(pattern, "**") {
			// Simple ** support: replace ** with *
			simplePattern := strings.ReplaceAll(pattern, "**/", "")
			matched, _ = filepath.Match(simplePattern, info.Name())
		}

		if matched {
			results = append(results, relPath)
		}

		return nil
	})

	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		return common.ErrorResult(fmt.Sprintf("search error: %v", err)), nil
	}

	if len(results) == 0 {
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "No files found."},
			},
		}, nil
	}

	sort.Strings(results)
	text := strings.Join(results, "\n")
	if len(results) >= limit {
		text += fmt.Sprintf("\n\n... (limited to %d results)", limit)
	}

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
		Details: map[string]any{
			"path":          searchPath,
			"matched_paths": absolutePaths(searchPath, results, 20),
		},
	}, nil
}

func absolutePaths(base string, rels []string, limit int) []string {
	out := make([]string, 0, min(len(rels), limit))
	for _, rel := range rels {
		out = append(out, filepath.Join(base, rel))
		if len(out) >= limit {
			break
		}
	}
	return out
}
