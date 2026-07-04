package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type agentEvalOptions struct {
	Agent          string
	AgentArgs      []string
	PromptArg      string
	JSONOutput     bool
	OutputDir      string
	TimeoutSeconds int
	KeepGoing      bool
	Summary        bool
}

type agentTask struct {
	ID             string          `yaml:"id"`
	Name           string          `yaml:"name"`
	Category       string          `yaml:"category"`
	GradingType    string          `yaml:"grading_type"`
	TimeoutSeconds int             `yaml:"timeout_seconds"`
	WorkspaceFiles []workspaceFile `yaml:"workspace_files"`
	Checks         []agentCheck    `yaml:"checks"`

	Prompt     string `yaml:"-"`
	SourcePath string `yaml:"-"`
}

type workspaceFile struct {
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
	Mode    string `yaml:"mode"`
}

type agentCheck struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Path    string   `yaml:"path"`
	Value   string   `yaml:"value"`
	Pattern string   `yaml:"pattern"`
	Command []string `yaml:"command"`
}

type agentRunResult struct {
	TaskID                  string              `json:"task_id"`
	Name                    string              `json:"name"`
	Category                string              `json:"category,omitempty"`
	GradingType             string              `json:"grading_type,omitempty"`
	Status                  string              `json:"status"`
	Error                   string              `json:"error,omitempty"`
	Scores                  map[string]float64  `json:"scores"`
	CheckResults            []agentCheckResult  `json:"check_results"`
	ExecutionTimeSeconds    float64             `json:"execution_time_seconds"`
	AgentCommand            []string            `json:"agent_command"`
	AgentExitCode           int                 `json:"agent_exit_code"`
	Stdout                  string              `json:"stdout"`
	Stderr                  string              `json:"stderr"`
	AssistantText           string              `json:"assistant_text,omitempty"`
	ToolCalls               []string            `json:"tool_calls,omitempty"`
	WorkspacePath           string              `json:"workspace_path"`
	WorkspaceFiles          []workspaceSnapshot `json:"workspace_files"`
	ExtractedArtifacts      []workspaceSnapshot `json:"extracted_artifacts,omitempty"`
	SourceTask              string              `json:"source_task"`
	TaskHash                string              `json:"task_hash"`
	TaskTimeoutSeconds      int                 `json:"task_timeout_seconds"`
	EffectiveTimeoutSeconds int                 `json:"effective_timeout_seconds"`
}

type agentCheckResult struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Pass      bool     `json:"pass"`
	Score     float64  `json:"score"`
	Reason    string   `json:"reason,omitempty"`
	Command   []string `json:"command,omitempty"`
	Output    string   `json:"output,omitempty"`
	ExitCode  int      `json:"exit_code,omitempty"`
	ErrorText string   `json:"error,omitempty"`
}

type workspaceSnapshot struct {
	Path   string `json:"path"`
	Size   int64  `json:"size_bytes"`
	SHA256 string `json:"sha256"`
}

func runAgentEvalCommand(paths []string, opts agentEvalOptions) error {
	if opts.Agent == "" {
		opts.Agent = "go"
	}
	if len(opts.AgentArgs) == 0 {
		opts.AgentArgs = []string{"run", "./cmd/modu_code", "--no-approve"}
	}
	if opts.TimeoutSeconds <= 0 {
		opts.TimeoutSeconds = 300
	}
	if len(paths) == 0 {
		paths = []string{filepath.Join("eval", "tasks", "modu_code")}
	}

	taskPaths, err := collectAgentTaskPaths(paths)
	if err != nil {
		return err
	}
	if len(taskPaths) == 0 {
		return fmt.Errorf("no agent eval tasks found")
	}
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Join("eval", "results", "modu-code-"+time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	var results []agentRunResult
	var failed int
	for _, taskPath := range taskPaths {
		task, err := parseAgentTask(taskPath)
		if err != nil {
			return err
		}
		result := runAgentTask(task, opts)
		results = append(results, result)
		if err := writeAgentResult(opts.OutputDir, result); err != nil {
			return err
		}
		printAgentResultLine(result)
		if result.Status != "success" {
			failed++
			if !opts.KeepGoing {
				break
			}
		}
	}
	if opts.Summary {
		if err := writeAgentSummary(opts.OutputDir, results); err != nil {
			return err
		}
	}
	fmt.Printf("agent eval results: %s\n", opts.OutputDir)
	if failed > 0 {
		return fmt.Errorf("%d agent eval task(s) failed", failed)
	}
	return nil
}

func collectAgentTaskPaths(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range paths {
		if raw == "" {
			continue
		}
		matches, err := filepath.Glob(raw)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			matches = []string{raw}
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			if info.IsDir() {
				err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
						return nil
					}
					if !seen[p] {
						seen[p] = true
						out = append(out, p)
					}
					return nil
				})
				if err != nil {
					return nil, err
				}
				continue
			}
			if !strings.HasSuffix(path, ".md") {
				continue
			}
			if !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func parseAgentTask(path string) (agentTask, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return agentTask{}, err
	}
	front, body, ok := splitMarkdownFrontmatter(string(data))
	if !ok {
		return agentTask{}, fmt.Errorf("%s: missing YAML frontmatter", path)
	}
	var task agentTask
	if err := yaml.Unmarshal([]byte(front), &task); err != nil {
		return agentTask{}, fmt.Errorf("%s: parse frontmatter: %w", path, err)
	}
	prompt := markdownSection(body, "Prompt")
	if strings.TrimSpace(prompt) == "" {
		return agentTask{}, fmt.Errorf("%s: missing ## Prompt section", path)
	}
	if task.ID == "" {
		task.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if task.Name == "" {
		task.Name = task.ID
	}
	if task.GradingType == "" {
		task.GradingType = "deterministic"
	}
	task.Prompt = strings.TrimSpace(prompt)
	task.SourcePath = path
	return task, nil
}

func splitMarkdownFrontmatter(content string) (string, string, bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", strings.TrimSpace(content), false
	}
	idx := strings.Index(normalized[4:], "\n---")
	if idx < 0 {
		return "", strings.TrimSpace(content), false
	}
	front := normalized[4 : 4+idx]
	body := strings.TrimSpace(normalized[4+idx+4:])
	return front, body, true
}

func markdownSection(body, name string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	header := "## " + strings.TrimSpace(name)
	inSection := false
	var out []string
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if inSection {
				break
			}
			inSection = strings.TrimSpace(line) == header
			continue
		}
		if inSection {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func runAgentTask(task agentTask, opts agentEvalOptions) agentRunResult {
	started := time.Now()
	workspace, err := os.MkdirTemp("", "modu-agent-eval-"+safeName(task.ID)+"-")
	if err != nil {
		return agentRunError(task, opts, "", nil, time.Since(started), fmt.Errorf("workspace: %w", err))
	}

	written, err := materializeAgentWorkspace(workspace, task.WorkspaceFiles)
	if err != nil {
		return agentRunError(task, opts, workspace, written, time.Since(started), err)
	}

	timeout := task.TimeoutSeconds
	if timeout <= 0 {
		timeout = opts.TimeoutSeconds
	}
	if opts.TimeoutSeconds > 0 && timeout > opts.TimeoutSeconds {
		timeout = opts.TimeoutSeconds
	}

	cmdArgs := buildAgentCommandArgs(opts, task.Prompt)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, opts.Agent, cmdArgs...)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "PWD="+workspace)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	exitCode := 0
	status := "success"
	var runErr error
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout"
			runErr = fmt.Errorf("agent timed out after %ds", timeout)
			exitCode = -1
		} else {
			status = "error"
			runErr = err
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
	}

	stdoutText := stdout.String()
	stderrText := stderr.String()
	assistantText, toolCalls := parseAgentStdout(stdoutText, opts.JSONOutput)
	artifacts, artifactErr := extractAgentFileArtifacts(workspace, stdoutText)
	if status == "success" && artifactErr != nil {
		status = "artifact_error"
		runErr = artifactErr
	}

	checkResults := []agentCheckResult{}
	scores := map[string]float64{}
	if status == "success" {
		checkResults = runAgentChecks(task.Checks, workspace, stdoutText, assistantText, toolCalls)
		for _, check := range checkResults {
			scores[check.Name] = check.Score
			if !check.Pass && status == "success" {
				status = "failed"
			}
		}
	}

	snapshot, _ := snapshotWorkspace(workspace)
	return agentRunResult{
		TaskID:                  task.ID,
		Name:                    task.Name,
		Category:                task.Category,
		GradingType:             task.GradingType,
		Status:                  status,
		Error:                   errorString(runErr),
		Scores:                  scores,
		CheckResults:            checkResults,
		ExecutionTimeSeconds:    time.Since(started).Seconds(),
		AgentCommand:            append([]string{opts.Agent}, cmdArgs...),
		AgentExitCode:           exitCode,
		Stdout:                  stdoutText,
		Stderr:                  stderrText,
		AssistantText:           assistantText,
		ToolCalls:               toolCalls,
		WorkspacePath:           workspace,
		WorkspaceFiles:          snapshot,
		ExtractedArtifacts:      artifacts,
		SourceTask:              task.SourcePath,
		TaskHash:                fileSHA256(task.SourcePath),
		TaskTimeoutSeconds:      task.TimeoutSeconds,
		EffectiveTimeoutSeconds: timeout,
	}
}

func agentRunError(task agentTask, opts agentEvalOptions, workspace string, files []workspaceSnapshot, elapsed time.Duration, err error) agentRunResult {
	return agentRunResult{
		TaskID:               task.ID,
		Name:                 task.Name,
		Category:             task.Category,
		GradingType:          task.GradingType,
		Status:               "error",
		Error:                errorString(err),
		Scores:               map[string]float64{},
		ExecutionTimeSeconds: elapsed.Seconds(),
		AgentCommand:         append([]string{opts.Agent}, opts.AgentArgs...),
		AgentExitCode:        -1,
		WorkspacePath:        workspace,
		WorkspaceFiles:       files,
		SourceTask:           task.SourcePath,
		TaskHash:             fileSHA256(task.SourcePath),
	}
}

func buildAgentCommandArgs(opts agentEvalOptions, prompt string) []string {
	args := append([]string(nil), opts.AgentArgs...)
	if opts.JSONOutput && !containsArg(args, "--json") {
		args = append(args, "--json")
	}
	if opts.PromptArg != "" {
		args = append(args, opts.PromptArg, prompt)
	} else {
		args = append(args, prompt)
	}
	return args
}

func containsArg(args []string, arg string) bool {
	for _, item := range args {
		if item == arg {
			return true
		}
	}
	return false
}

func materializeAgentWorkspace(workspace string, files []workspaceFile) ([]workspaceSnapshot, error) {
	var written []workspaceSnapshot
	for _, file := range files {
		target, err := safeWorkspacePath(workspace, file.Path)
		if err != nil {
			return written, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return written, err
		}
		if err := os.WriteFile(target, []byte(file.Content), 0644); err != nil {
			return written, err
		}
		if file.Mode != "" {
			mode, err := strconvParseFileMode(file.Mode)
			if err != nil {
				return written, err
			}
			if err := os.Chmod(target, mode); err != nil {
				return written, err
			}
		}
		snap, err := snapshotFile(workspace, target)
		if err != nil {
			return written, err
		}
		written = append(written, snap)
	}
	return written, nil
}

func strconvParseFileMode(raw string) (os.FileMode, error) {
	var value uint64
	_, err := fmt.Sscanf(raw, "%o", &value)
	if err != nil {
		return 0, fmt.Errorf("invalid file mode %q", raw)
	}
	return os.FileMode(value), nil
}

func safeWorkspacePath(workspace, rawPath string) (string, error) {
	if strings.TrimSpace(rawPath) == "" {
		return "", fmt.Errorf("workspace file path cannot be empty")
	}
	if filepath.IsAbs(rawPath) {
		return "", fmt.Errorf("workspace file path must be relative: %s", rawPath)
	}
	clean := filepath.Clean(rawPath)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("workspace file path escapes workspace: %s", rawPath)
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, clean))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace file path escapes workspace: %s", rawPath)
	}
	return target, nil
}

func parseAgentStdout(stdout string, jsonOutput bool) (string, []string) {
	if !jsonOutput {
		return strings.TrimSpace(stdout), nil
	}
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 1024*1024), 20*1024*1024)
	var text strings.Builder
	var tools []string
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		if tool, _ := line["toolName"].(string); tool != "" {
			tools = append(tools, tool)
		}
		if delta, _ := line["message"].(string); delta != "" {
			text.WriteString(delta)
		}
	}
	return strings.TrimSpace(text.String()), tools
}

var fileFenceRE = regexp.MustCompile("(?s)```(?:file\\s+path=|file_path=|file:|file=)[\"']?([^\"'\\n]+)[\"']?[^\\n]*\\n(.*?)\\n```")

func extractAgentFileArtifacts(workspace, stdout string) ([]workspaceSnapshot, error) {
	var out []workspaceSnapshot
	for _, match := range fileFenceRE.FindAllStringSubmatch(stdout, -1) {
		rawPath := strings.TrimSpace(match[1])
		rawPath = strings.TrimPrefix(rawPath, "./")
		target, err := safeWorkspacePath(workspace, rawPath)
		if err != nil {
			return out, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return out, err
		}
		if err := os.WriteFile(target, []byte(match[2]), 0644); err != nil {
			return out, err
		}
		snap, err := snapshotFile(workspace, target)
		if err != nil {
			return out, err
		}
		out = append(out, snap)
	}
	return out, nil
}

func runAgentChecks(checks []agentCheck, workspace, stdout, assistantText string, toolCalls []string) []agentCheckResult {
	results := make([]agentCheckResult, 0, len(checks))
	for _, check := range checks {
		results = append(results, runAgentCheck(check, workspace, stdout, assistantText, toolCalls))
	}
	return results
}

func runAgentCheck(check agentCheck, workspace, stdout, assistantText string, toolCalls []string) agentCheckResult {
	name := check.Name
	if name == "" {
		name = check.Type
	}
	result := agentCheckResult{Name: name, Type: check.Type}
	pass := false
	reason := ""
	switch check.Type {
	case "assistant_responded":
		pass = strings.TrimSpace(assistantText) != "" || strings.TrimSpace(stdout) != ""
	case "output_contains":
		pass = strings.Contains(stdout+"\n"+assistantText, check.Value)
	case "output_not_contains":
		pass = !strings.Contains(stdout+"\n"+assistantText, check.Value)
	case "output_regex":
		pass, reason = regexpCheck(check.Pattern, stdout+"\n"+assistantText)
	case "tool_called":
		pass = stringInSlice(check.Value, toolCalls)
	case "file_exists":
		target, err := safeWorkspacePath(workspace, check.Path)
		if err != nil {
			reason = err.Error()
			break
		}
		_, statErr := os.Stat(target)
		pass = statErr == nil
		if statErr != nil {
			reason = statErr.Error()
		}
	case "file_contains", "file_not_contains", "file_regex":
		content, err := readCheckFile(workspace, check.Path)
		if err != nil {
			reason = err.Error()
			break
		}
		switch check.Type {
		case "file_contains":
			pass = strings.Contains(content, check.Value)
		case "file_not_contains":
			pass = !strings.Contains(content, check.Value)
		case "file_regex":
			pass, reason = regexpCheck(check.Pattern, content)
		}
	case "command_succeeds":
		cmdResult := runCheckCommand(workspace, check.Command)
		result.Command = check.Command
		result.Output = cmdResult.Output
		result.ExitCode = cmdResult.ExitCode
		result.ErrorText = cmdResult.ErrorText
		pass = cmdResult.Pass
		reason = cmdResult.ErrorText
	default:
		reason = "unknown check type"
	}
	result.Pass = pass
	if pass {
		result.Score = 1.0
	} else {
		result.Score = 0.0
	}
	result.Reason = reason
	return result
}

func regexpCheck(pattern, value string) (bool, string) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, err.Error()
	}
	return re.MatchString(value), ""
}

func readCheckFile(workspace, rawPath string) (string, error) {
	target, err := safeWorkspacePath(workspace, rawPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type checkCommandResult struct {
	Pass      bool
	Output    string
	ExitCode  int
	ErrorText string
}

func runCheckCommand(workspace string, command []string) checkCommandResult {
	if len(command) == 0 {
		return checkCommandResult{ExitCode: -1, ErrorText: "empty command"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = workspace
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	result := checkCommandResult{Output: output.String()}
	if err == nil {
		result.Pass = true
		return result
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.ExitCode = -1
		result.ErrorText = "command timed out"
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}
	result.ErrorText = err.Error()
	return result
}

func stringInSlice(value string, values []string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func snapshotWorkspace(workspace string) ([]workspaceSnapshot, error) {
	var out []workspaceSnapshot
	err := filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		snap, err := snapshotFile(workspace, path)
		if err != nil {
			return err
		}
		out = append(out, snap)
		return nil
	})
	return out, err
}

func snapshotFile(workspace, path string) (workspaceSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return workspaceSnapshot{}, err
	}
	rel, err := filepath.Rel(workspace, path)
	if err != nil {
		return workspaceSnapshot{}, err
	}
	sum := sha256.Sum256(data)
	return workspaceSnapshot{Path: filepath.ToSlash(rel), Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sum)}, nil
}

func fileSHA256(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func writeAgentResult(outputRoot string, result agentRunResult) error {
	dir := filepath.Join(outputRoot, safeName(result.TaskID))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "result.json"), append(data, '\n'), 0644)
}

func writeAgentSummary(outputRoot string, results []agentRunResult) error {
	var passed, totalChecks, passedChecks int
	for _, result := range results {
		if result.Status == "success" {
			passed++
		}
		for _, check := range result.CheckResults {
			totalChecks++
			if check.Pass {
				passedChecks++
			}
		}
	}
	var b strings.Builder
	b.WriteString("# modu_code Agent Eval Summary\n\n")
	b.WriteString(fmt.Sprintf("- Tasks: %d\n", len(results)))
	b.WriteString(fmt.Sprintf("- Passed tasks: %d\n", passed))
	b.WriteString(fmt.Sprintf("- Check pass rate: %.1f%% (%d/%d)\n\n", percent(passedChecks, totalChecks), passedChecks, totalChecks))
	b.WriteString("| Task | Status | Checks |\n")
	b.WriteString("|---|---:|---:|\n")
	for _, result := range results {
		checkPassed := 0
		for _, check := range result.CheckResults {
			if check.Pass {
				checkPassed++
			}
		}
		b.WriteString(fmt.Sprintf("| `%s` | %s | %d/%d |\n", result.TaskID, result.Status, checkPassed, len(result.CheckResults)))
	}
	return os.WriteFile(filepath.Join(outputRoot, "summary.md"), []byte(b.String()), 0644)
}

func printAgentResultLine(result agentRunResult) {
	checkPassed := 0
	for _, check := range result.CheckResults {
		if check.Pass {
			checkPassed++
		}
	}
	fmt.Printf("%-40s %-14s checks=%d/%d time=%.1fs\n", result.TaskID, result.Status, checkPassed, len(result.CheckResults), result.ExecutionTimeSeconds)
}

func percent(passed, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(passed) / float64(total) * 100
}

func safeName(value string) string {
	if value == "" {
		return "task"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "task"
	}
	return out
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
