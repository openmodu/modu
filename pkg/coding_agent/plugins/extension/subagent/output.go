package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type outputOptions struct {
	path     string
	mode     string
	fileOnly bool
}

func decodeOutputOptions(args map[string]any) outputOptions {
	path, _ := args["output"].(string)
	mode, _ := args["outputMode"].(string)
	mode = strings.TrimSpace(mode)
	return outputOptions{
		path:     strings.TrimSpace(path),
		mode:     mode,
		fileOnly: strings.EqualFold(mode, "file-only"),
	}
}

func applyOutputOptions(ext *Extension, args map[string]any, text string) (string, error) {
	opts := decodeOutputOptions(args)
	if opts.path == "" {
		return text, nil
	}
	if extractTaskID(text) != "" {
		return text, nil
	}
	path, err := resolveOutputPath(ext, opts.path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", err
	}
	ref := outputReference(path, text)
	if opts.fileOnly {
		return ref, nil
	}
	return strings.TrimSpace(text) + "\n\n" + ref, nil
}

func resolveOutputPath(ext *Extension, raw string) (string, error) {
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	base := "."
	if ext != nil && ext.api != nil {
		base = filepath.Join(ext.api.AgentDir(), "tool-results", projectKey(ext.api.Cwd()), "subagents")
	}
	path := filepath.Clean(filepath.Join(base, raw))
	root := filepath.Clean(base)
	if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", fmt.Errorf("output path escapes subagent output directory: %s", raw)
	}
	return path, nil
}

func resolveForkOutputPath(ext *Extension, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return resolveOutputPath(ext, raw)
}

func outputReference(path, text string) string {
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	return fmt.Sprintf("Output saved to: %s (%d bytes, %d lines).", path, len([]byte(text)), lines)
}

func projectKey(cwd string) string {
	key := strings.ReplaceAll(strings.TrimPrefix(cwd, string(filepath.Separator)), string(filepath.Separator), "_")
	if key == "" {
		return "root"
	}
	return key
}
