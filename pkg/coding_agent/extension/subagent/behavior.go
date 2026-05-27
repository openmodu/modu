package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	csubagent "github.com/openmodu/modu/pkg/coding_agent/subagent"
)

const initialProgressContent = "# Progress\n\n## Status\nIn Progress\n\n## Tasks\n\n## Files Changed\n\n## Notes\n"

type readOptions struct {
	set      bool
	disabled bool
	paths    []string
}

func decodeReadOptions(raw any) (readOptions, error) {
	if raw == nil {
		return readOptions{}, nil
	}
	switch v := raw.(type) {
	case bool:
		return readOptions{set: true, disabled: !v}, nil
	case []string:
		return readOptions{set: true, paths: cleanStrings(v)}, nil
	case []any:
		paths := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return readOptions{}, fmt.Errorf("reads[%d] must be string, got %T", i, item)
			}
			if s = strings.TrimSpace(s); s != "" {
				paths = append(paths, s)
			}
		}
		return readOptions{set: true, paths: paths}, nil
	default:
		return readOptions{}, fmt.Errorf("reads must be an array or boolean, got %T", raw)
	}
}

func decodeProgressOption(raw any) (*bool, error) {
	if raw == nil {
		return nil, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return nil, fmt.Errorf("progress must be boolean, got %T", raw)
	}
	return &value, nil
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func applyTaskBehavior(ext *Extension, def *csubagent.SubagentDefinition, task string, opts callOptions) (string, error) {
	reads := effectiveReads(def, opts.reads)
	progress := effectiveProgress(def, opts.progress)
	if len(reads) == 0 && !progress {
		return task, nil
	}

	baseDir := instructionBaseDir(ext, opts.chainDir, opts.cwd)
	var prefix []string
	if len(reads) > 0 {
		files := make([]string, 0, len(reads))
		for _, read := range reads {
			files = append(files, resolveInstructionPath(baseDir, read))
		}
		prefix = append(prefix, fmt.Sprintf("[Read from: %s]", strings.Join(files, ", ")))
	}

	var suffix []string
	if progress {
		progressPath, err := progressFilePath(ext, opts.chainDir)
		if err != nil {
			return "", err
		}
		if opts.progressFirst {
			if err := writeInitialProgressFile(progressPath); err != nil {
				return "", err
			}
			suffix = append(suffix, "Create and maintain progress at: "+progressPath)
		} else {
			suffix = append(suffix, "Update progress at: "+progressPath)
		}
	}

	var b strings.Builder
	if len(prefix) > 0 {
		b.WriteString(strings.Join(prefix, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString(task)
	if len(suffix) > 0 {
		b.WriteString("\n\n---\n")
		b.WriteString(strings.Join(suffix, "\n"))
	}
	return b.String(), nil
}

func effectiveReads(def *csubagent.SubagentDefinition, opts readOptions) []string {
	if opts.set {
		if opts.disabled {
			return nil
		}
		if len(opts.paths) > 0 {
			return opts.paths
		}
	}
	if def == nil {
		return nil
	}
	return cleanStrings(def.DefaultReads)
}

func effectiveProgress(def *csubagent.SubagentDefinition, override *bool) bool {
	if override != nil {
		return *override
	}
	return def != nil && def.DefaultProgress
}

func callUsesProgress(ext *Extension, call callSpec) bool {
	if ext == nil || ext.loader == nil {
		return false
	}
	def, ok := ext.loader.Get(call.agent)
	if !ok {
		return false
	}
	return effectiveProgress(def, call.progress)
}

func instructionBaseDir(ext *Extension, chainDir, cwd string) string {
	if strings.TrimSpace(chainDir) != "" {
		return resolveBasePath(ext, chainDir, cwd)
	}
	if strings.TrimSpace(cwd) != "" {
		return resolveBasePath(ext, cwd, "")
	}
	if ext != nil && ext.api != nil && strings.TrimSpace(ext.api.Cwd()) != "" {
		return ext.api.Cwd()
	}
	return "."
}

func progressFilePath(ext *Extension, chainDir string) (string, error) {
	dir := strings.TrimSpace(chainDir)
	if dir == "" {
		if ext != nil && ext.api != nil {
			dir = filepath.Join(ext.api.AgentDir(), "tool-results", projectKey(ext.api.Cwd()), "subagents")
		} else {
			dir = "."
		}
	} else {
		dir = resolveBasePath(ext, dir, "")
	}
	return filepath.Join(dir, "progress.md"), nil
}

func resolveBasePath(ext *Extension, path, baseOverride string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "."
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	base := "."
	if strings.TrimSpace(baseOverride) != "" {
		base = resolveBasePath(ext, baseOverride, "")
	} else if ext != nil && ext.api != nil && strings.TrimSpace(ext.api.Cwd()) != "" {
		base = ext.api.Cwd()
	}
	return filepath.Clean(filepath.Join(base, path))
}

func resolveInstructionPath(baseDir, path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

// readProgressBody returns the trimmed contents of the progress.md the
// extension would write for this call. Empty string when no file exists or
// is unreadable — callers treat that as "nothing to append".
func readProgressBody(ext *Extension, args map[string]any) string {
	chainDir, _ := args["chainDir"].(string)
	path, err := progressFilePath(ext, chainDir)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// appendIncludedProgress optionally appends the progress.md body to text
// when the caller set includeProgress:true on the top-level call. The
// formatting matches pi's "result + delimiter + ## Progress" layout so
// downstream readers can split on the marker if they need to.
func appendIncludedProgress(ext *Extension, args map[string]any, text string) string {
	if include, _ := args["includeProgress"].(bool); !include {
		return text
	}
	body := readProgressBody(ext, args)
	if body == "" {
		return text
	}
	return strings.TrimRight(text, "\n") + "\n\n---\n\n## Progress\n\n" + body
}

func writeInitialProgressFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(initialProgressContent), 0o600)
}
