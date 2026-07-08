package bash

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

// backgroundResult returns a success result for a background process.
func backgroundResult(pid int) types.ToolResult {
	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Process started in background (pid %d).", pid),
			},
		},
		Details: map[string]any{"pid": pid, "background": true},
	}
}

const (
	defaultBashTimeoutSeconds = 120
	maxBashTimeoutSeconds     = 600
)

var leadingSleepPattern = regexp.MustCompile(`^\s*sleep\s+([0-9]+(?:\.[0-9]+)?s?)(?:\s*(?:&&|;)\s*(.*))?\s*$`)
var sedInPlacePattern = regexp.MustCompile(`^\s*sed\s+(?:.*\s)?(?:-i(?:$|[\s'".])|--in-place(?:$|[\s=]))`)

// BashTool implements the bash command execution tool.
type BashTool struct {
	cwd       string
	artifacts *common.ArtifactStore
}

func NewTool(cwd string) types.Tool {
	return &BashTool{cwd: cwd}
}

func NewToolWithArtifacts(cwd string, artifacts *common.ArtifactStore) types.Tool {
	return &BashTool{cwd: cwd, artifacts: artifacts}
}

func (t *BashTool) Name() string  { return "bash" }
func (t *BashTool) Label() string { return "Bash Command" }
func (t *BashTool) Description() string {
	return `Execute a bash command and return its output.

Usage:
- Use this tool for builds, tests, linters, package managers, git inspection, and terminal operations that genuinely require a shell.
- Do not use bash for normal file reads, content search, file-pattern search, source edits, or file creation when read, grep, find, edit, write, or ls can do the job.
- Prefer absolute paths or paths relative to the working directory, and quote paths that contain spaces.
- Avoid changing directories unless the user asks; keep command effects scoped to the working directory.
- Use timeout to set execution timeout. Values up to 600 are treated as seconds for compatibility; larger values are treated as Claude-style milliseconds. The timeout_ms alias is accepted. Default 120 seconds, max 600 seconds. Numeric strings such as "1000" are accepted.
- Use background=true for long-running servers or daemons that should not block. The run_in_background alias is accepted for Claude Code compatibility. Boolean strings "true" and "false" are accepted. The command starts in a detached process group and returns immediately with the PID.
- The dangerouslyDisableSandbox parameter is accepted for Claude Code compatibility, but sandbox policy is controlled by the host process in this implementation.
- Foreground sleep commands of 2 seconds or longer are blocked; use run_in_background=true for intentional waits.
- In-place sed edits are blocked; use the edit tool so the replacement is targeted and reviewable.
- Simple cat/head/tail/tac/nl/less/more file reads, including common line/byte-count flags, are blocked; use the read tool so offset/limit and binary safeguards apply.
- Simple grep/rg/sed/awk content searches, including common search and glob flags, are blocked; use the grep tool so output modes, paging, and path handling stay consistent.
- Simple find/fd file-name searches are blocked; use the find tool so hidden-file, ignore, limit, and path display behavior stay consistent.
- Simple ls directory listings are blocked; use the ls tool so directory-only validation, ignore filters, and output limits stay consistent.
- For git operations, inspect state first and never run destructive commands, skip hooks, commit, push, amend, reset, clean, or delete branches unless the user explicitly asks.`
}

func (t *BashTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute",
			},
			"timeout": map[string]any{
				"anyOf":       semanticBashIntegerSchema(),
				"description": "Timeout. Values up to 600 are seconds; larger values are treated as milliseconds for Claude Code compatibility. Default 120 seconds, max 600 seconds.",
			},
			"timeout_ms": map[string]any{
				"anyOf":       semanticBashIntegerSchema(),
				"description": "Timeout in milliseconds, accepted for compatibility.",
			},
			"background": map[string]any{
				"anyOf":       semanticBashBooleanSchema(),
				"description": "Run the command in the background and return immediately with the PID. Use this for long-running servers or daemons.",
			},
			"run_in_background": map[string]any{
				"anyOf":       semanticBashBooleanSchema(),
				"description": "Alias for background, accepted for Claude Code compatibility.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional short active-voice description of what this command does. Useful for complex commands and UI review.",
			},
			"dangerouslyDisableSandbox": map[string]any{
				"anyOf":       semanticBashBooleanSchema(),
				"description": "Accepted for Claude Code compatibility. Sandbox policy is controlled by the host process in this implementation.",
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return common.ErrorResult("command is required"), nil
	}

	timeout := parseTimeout(args)

	// Background mode: start and detach immediately.
	background, _ := common.ToSemanticBool(args["background"])
	runInBackground, _ := common.ToSemanticBool(args["run_in_background"])
	background = background || runInBackground
	if isSedInPlaceEdit(command) {
		return common.ErrorResult("Blocked: in-place sed edits must use the edit tool instead of bash so replacements are targeted and reviewable."), nil
	}
	if isSimpleFileReadCommand(command) {
		return common.ErrorResult("Blocked: use the read tool for normal file reads instead of bash so offset/limit and binary safeguards apply."), nil
	}
	if isSimpleContentSearchCommand(command) {
		return common.ErrorResult("Blocked: use the grep tool for normal content search instead of bash so output modes, paging, and path handling stay consistent."), nil
	}
	if isSimpleFilePatternSearchCommand(command) {
		return common.ErrorResult("Blocked: use the find tool for normal file-name searches instead of bash so hidden-file, ignore, limit, and path display behavior stay consistent."), nil
	}
	if isSimpleDirectoryListCommand(command) {
		return common.ErrorResult("Blocked: use the ls tool for normal directory listings instead of bash so directory-only validation, ignore filters, and output limits stay consistent."), nil
	}
	if !background {
		if sleepPattern, blocked := detectBlockedSleepPattern(command); blocked {
			return common.ErrorResult(fmt.Sprintf("Blocked: %s. Run blocking commands in the background with run_in_background=true. If you genuinely need a delay for rate limiting or pacing, keep it under 2 seconds.", sleepPattern)), nil
		}
	}
	if background {
		cmd := exec.Command("bash", "-c", command)
		cmd.Dir = t.cwd
		cmd.Env = os.Environ()
		// Detach from parent's stdout/stderr so cmd.Run doesn't block.
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return common.ErrorResult(fmt.Sprintf("failed to start background command: %v", err)), nil
		}
		pid := cmd.Process.Pid
		// Reap the process asynchronously to avoid zombies.
		go func() { _ = cmd.Wait() }()
		return backgroundResult(pid), nil
	}

	// Create context with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", command)
	cmd.Dir = t.cwd

	// Inherit environment
	cmd.Env = os.Environ()

	// Set process group so we can kill all child processes
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var output strings.Builder
	if stdout.Len() > 0 {
		output.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString(stderr.String())
	}

	result := output.String()

	preview := common.PreviewText(result, common.TextPreviewOptions{
		ToolCallID:    toolCallID,
		ArtifactName:  "output",
		ArtifactStore: t.artifacts,
		Strategy:      common.PreviewTail,
		MaxLines:      common.BashMaxLines,
		MaxBytes:      common.DefaultMaxBytes,
	})

	exitCode := 0
	timedOut := false

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			timedOut = true
			// Kill process group
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if !timedOut {
			return common.ErrorResult(fmt.Sprintf("failed to execute command: %v", err)), nil
		}
	}

	var text string
	if timedOut {
		text = fmt.Sprintf("Command timed out after %s.\n", formatTimeout(timeout))
		if preview.Text != "" {
			text += "Partial output:\n" + preview.Text
		}
	} else {
		text = preview.Text
		if exitCode != 0 {
			text += fmt.Sprintf("\n\nExit code: %d", exitCode)
		}
	}

	if text == "" {
		text = "(no output)"
	}

	details := map[string]any{
		"exitCode": exitCode,
		"timedOut": timedOut,
		"timeout":  formatTimeout(timeout),
	}
	for k, v := range preview.Details {
		details[k] = v
	}
	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: text,
			},
		},
		Details: details,
	}, nil
}

func detectBlockedSleepPattern(command string) (string, bool) {
	match := leadingSleepPattern.FindStringSubmatch(command)
	if match == nil {
		return "", false
	}

	sleepArg := match[1]
	seconds, err := strconv.ParseFloat(strings.TrimSuffix(sleepArg, "s"), 64)
	if err != nil || seconds < 2 {
		return "", false
	}

	rest := ""
	if len(match) > 2 {
		rest = strings.TrimSpace(match[2])
	}
	if rest != "" {
		return fmt.Sprintf("sleep %s followed by: %s", sleepArg, rest), true
	}
	return fmt.Sprintf("standalone sleep %s", sleepArg), true
}

func isSedInPlaceEdit(command string) bool {
	return sedInPlacePattern.MatchString(command)
}

func isSimpleFileReadCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "|&;<>()$`*?[]{}") {
		return false
	}

	fields, ok := splitSimpleShellFields(command)
	if !ok {
		return false
	}
	if len(fields) < 2 {
		return false
	}

	cmd := fields[0]
	if cmd != "cat" && cmd != "head" && cmd != "tail" && cmd != "tac" && cmd != "nl" && cmd != "less" && cmd != "more" {
		return false
	}
	return hasOnlySimpleReadArgs(cmd, fields[1:])
}

func isSimpleContentSearchCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "|&;<>()$`") {
		return false
	}

	fields, ok := splitSimpleShellFields(command)
	if !ok {
		return false
	}
	if len(fields) < 2 {
		return false
	}
	cmd := fields[0]
	if cmd != "grep" && cmd != "rg" {
		return isSimpleSedContentSearch(fields) || isSimpleAwkContentSearch(fields)
	}
	return hasOnlySimpleContentSearchArgs(cmd, fields[1:])
}

func isSimpleSedContentSearch(fields []string) bool {
	if len(fields) < 3 || fields[0] != "sed" {
		return false
	}
	sawNoPrint := false
	scriptIndex := -1
	for i := 1; i < len(fields); i++ {
		arg := stripShellQuotes(fields[i])
		switch {
		case arg == "-n" || arg == "--quiet" || arg == "--silent":
			sawNoPrint = true
		case strings.HasPrefix(arg, "-") && strings.Contains(arg, "n") && strings.TrimLeft(arg, "-Enr") == "":
			sawNoPrint = true
		case scriptIndex == -1:
			scriptIndex = i
		default:
			if !isSimpleSearchPath(arg) {
				return false
			}
		}
	}
	if !sawNoPrint || scriptIndex == -1 {
		return false
	}
	return isSimpleSedPrintScript(stripShellQuotes(fields[scriptIndex]))
}

func isSimpleSedPrintScript(script string) bool {
	if script == "" || strings.ContainsAny(script, "|&;<>()$`") {
		return false
	}
	if len(script) < 4 || script[0] != '/' {
		return false
	}
	lastSlash := strings.LastIndex(script[1:], "/")
	if lastSlash < 0 {
		return false
	}
	lastSlash++
	suffix := script[lastSlash+1:]
	return suffix == "p" || suffix == "Ip" || suffix == "ip"
}

func isSimpleAwkContentSearch(fields []string) bool {
	if len(fields) < 2 || fields[0] != "awk" {
		return false
	}
	scriptIndex := -1
	for i := 1; i < len(fields); i++ {
		arg := stripShellQuotes(fields[i])
		if arg == "" {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			return false
		}
		if scriptIndex == -1 {
			scriptIndex = i
			continue
		}
		if !isSimpleSearchPath(arg) {
			return false
		}
	}
	return scriptIndex != -1 && isSimpleAwkPrintScript(stripShellQuotes(fields[scriptIndex]))
}

func isSimpleAwkPrintScript(script string) bool {
	if script == "" || strings.ContainsAny(script, "|&;<>()$`") {
		return false
	}
	if strings.HasPrefix(script, "/") && strings.HasSuffix(script, "/") && len(script) >= 2 {
		return true
	}
	if strings.HasPrefix(script, "/") && strings.HasSuffix(script, "/ {print}") {
		return true
	}
	return false
}

func isSimpleFilePatternSearchCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "|&;<>()$`") {
		return false
	}

	fields, ok := splitSimpleShellFields(command)
	if !ok {
		return false
	}
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "fd", "fdfind":
		return hasOnlySimpleFdArgs(fields[1:])
	case "find":
		return hasOnlySimpleFindArgs(fields[1:])
	default:
		return false
	}
}

func isSimpleDirectoryListCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "|&;<>()$`*?[]{}") {
		return false
	}

	fields, ok := splitSimpleShellFields(command)
	if !ok {
		return false
	}
	if len(fields) == 0 || fields[0] != "ls" {
		return false
	}
	pathCount := 0
	for _, raw := range fields[1:] {
		arg := stripShellQuotes(raw)
		if arg == "" {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			if !isSimpleLsFlag(arg) {
				return false
			}
			continue
		}
		pathCount++
		if pathCount > 1 {
			return false
		}
	}
	return true
}

func isSimpleLsFlag(flag string) bool {
	if !strings.HasPrefix(flag, "-") || flag == "-" {
		return false
	}
	if strings.HasPrefix(flag, "--") {
		switch flag {
		case "--all", "--almost-all", "--human-readable", "--classify":
			return true
		default:
			return false
		}
	}
	for _, r := range strings.TrimPrefix(flag, "-") {
		if !strings.ContainsRune("aAlhFGp1", r) {
			return false
		}
	}
	return true
}

func hasOnlySimpleFdArgs(args []string) bool {
	patterns := 0
	for i := 0; i < len(args); i++ {
		arg := stripShellQuotes(args[i])
		if arg == "" {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			switch arg {
			case "-H", "--hidden", "-I", "--no-ignore", "-g", "--glob", "-s", "--case-sensitive", "-i", "--ignore-case", "-p", "--full-path":
				continue
			case "-t", "--type", "-e", "--extension", "-d", "--max-depth", "--min-depth", "--exact-depth":
				if i+1 >= len(args) || strings.HasPrefix(stripShellQuotes(args[i+1]), "-") {
					return false
				}
				i++
				continue
			default:
				return false
			}
		}
		patterns++
	}
	return patterns > 0 && patterns <= 2
}

func hasOnlySimpleContentSearchArgs(cmd string, args []string) bool {
	positionals := 0
	patternFromFlag := false
	endOptions := false
	for i := 0; i < len(args); i++ {
		arg := stripShellQuotes(args[i])
		if arg == "" || strings.ContainsAny(arg, "|&;<>()$`") {
			return false
		}
		if arg == "--" {
			endOptions = true
			continue
		}
		if endOptions || !strings.HasPrefix(arg, "-") {
			positionals++
			continue
		}

		consumedValue, consumedPattern, ok := isSimpleContentSearchFlag(cmd, arg, args[i+1:])
		if !ok {
			return false
		}
		if consumedPattern {
			patternFromFlag = true
		}
		if consumedValue {
			i++
		}
	}

	if patternFromFlag {
		return positionals <= 1
	}
	return positionals >= 1 && positionals <= 2
}

func isSimpleContentSearchFlag(cmd, flag string, remaining []string) (bool, bool, bool) {
	switch {
	case flag == "-e" || flag == "--regexp":
		return len(remaining) > 0 && isSimpleSearchValue(stripShellQuotes(remaining[0])), true, len(remaining) > 0 && isSimpleSearchValue(stripShellQuotes(remaining[0]))
	case strings.HasPrefix(flag, "-e") && len(flag) > 2:
		return false, true, isSimpleSearchValue(flag[2:])
	case strings.HasPrefix(flag, "--regexp="):
		return false, true, isSimpleSearchValue(strings.TrimPrefix(flag, "--regexp="))
	case flag == "-C" || flag == "--context" || flag == "-A" || flag == "--after-context" || flag == "-B" || flag == "--before-context":
		return len(remaining) > 0 && isCountArg(stripShellQuotes(remaining[0])), false, len(remaining) > 0 && isCountArg(stripShellQuotes(remaining[0]))
	case strings.HasPrefix(flag, "--context="):
		return false, false, isCountArg(strings.TrimPrefix(flag, "--context="))
	case strings.HasPrefix(flag, "--after-context="):
		return false, false, isCountArg(strings.TrimPrefix(flag, "--after-context="))
	case strings.HasPrefix(flag, "--before-context="):
		return false, false, isCountArg(strings.TrimPrefix(flag, "--before-context="))
	case strings.HasPrefix(flag, "-C") && len(flag) > 2:
		return false, false, isCountArg(flag[2:])
	case strings.HasPrefix(flag, "-A") && len(flag) > 2:
		return false, false, isCountArg(flag[2:])
	case strings.HasPrefix(flag, "-B") && len(flag) > 2:
		return false, false, isCountArg(flag[2:])
	case cmd == "rg" && (flag == "-g" || flag == "--glob" || flag == "-t" || flag == "--type"):
		return len(remaining) > 0 && isSimpleSearchValue(stripShellQuotes(remaining[0])), false, len(remaining) > 0 && isSimpleSearchValue(stripShellQuotes(remaining[0]))
	case cmd == "rg" && strings.HasPrefix(flag, "--glob="):
		return false, false, isSimpleSearchValue(strings.TrimPrefix(flag, "--glob="))
	case cmd == "rg" && strings.HasPrefix(flag, "--type="):
		return false, false, isSimpleSearchValue(strings.TrimPrefix(flag, "--type="))
	case cmd == "rg" && (strings.HasPrefix(flag, "-g") || strings.HasPrefix(flag, "-t")) && len(flag) > 2:
		return false, false, isSimpleSearchValue(flag[2:])
	case strings.HasPrefix(flag, "--"):
		return false, false, isSimpleLongContentSearchFlag(cmd, flag)
	default:
		return false, false, isSimpleShortContentSearchFlag(cmd, flag)
	}
}

func isSimpleLongContentSearchFlag(cmd, flag string) bool {
	switch flag {
	case "--ignore-case", "--case-sensitive", "--line-number", "--no-line-number",
		"--files-with-matches", "--files-without-match", "--count", "--fixed-strings",
		"--word-regexp", "--invert-match", "--recursive", "--hidden", "--no-ignore",
		"--multiline", "--multiline-dotall":
		return true
	case "--extended-regexp", "--basic-regexp":
		return cmd == "grep"
	default:
		return false
	}
}

func isSimpleShortContentSearchFlag(cmd, flag string) bool {
	if !strings.HasPrefix(flag, "-") || flag == "-" {
		return false
	}
	for _, r := range strings.TrimPrefix(flag, "-") {
		if strings.ContainsRune("inlLcvwF", r) {
			continue
		}
		if cmd == "grep" && (r == 'R' || r == 'r' || r == 'E' || r == 'G') {
			continue
		}
		if cmd == "rg" && (r == 'S' || r == 'U') {
			continue
		}
		return false
	}
	return true
}

func isSimpleSearchValue(value string) bool {
	return value != "" && !strings.ContainsAny(value, "|&;<>()$`")
}

func isSimpleSearchPath(path string) bool {
	return path != "" && !strings.ContainsAny(path, "|&;<>()$`")
}

func hasOnlySimpleFindArgs(args []string) bool {
	hasName := false
	for i := 0; i < len(args); i++ {
		arg := stripShellQuotes(args[i])
		if arg == "" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "-name", "-iname":
			if i+1 >= len(args) {
				return false
			}
			pattern := stripShellQuotes(args[i+1])
			if pattern == "" || strings.HasPrefix(pattern, "-") {
				return false
			}
			hasName = true
			i++
		case "-type":
			if i+1 >= len(args) || stripShellQuotes(args[i+1]) != "f" {
				return false
			}
			i++
		case "-maxdepth", "-mindepth":
			if i+1 >= len(args) || !isCountArg(stripShellQuotes(args[i+1])) {
				return false
			}
			i++
		case "-print":
			continue
		default:
			return false
		}
	}
	return hasName
}

func stripShellQuotes(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func splitSimpleShellFields(command string) ([]string, bool) {
	var fields []string
	var current strings.Builder
	var quote rune
	inToken := false

	for _, r := range strings.TrimSpace(command) {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				inToken = true
				continue
			}
			current.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inToken {
				fields = append(fields, current.String())
				current.Reset()
				inToken = false
			}
		default:
			current.WriteRune(r)
			inToken = true
		}
	}

	if quote != 0 {
		return nil, false
	}
	if inToken {
		fields = append(fields, current.String())
	}
	return fields, true
}

func hasOnlySimpleReadArgs(cmd string, args []string) bool {
	pathCount := 0
	endOptions := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			continue
		}
		if arg == "--" {
			endOptions = true
			continue
		}
		if endOptions || !strings.HasPrefix(arg, "-") {
			if !isSimpleReadPath(arg) {
				return false
			}
			pathCount++
			continue
		}

		if cmd == "cat" {
			if !isCatReadFlag(arg) {
				return false
			}
			continue
		}
		if cmd == "tac" || cmd == "nl" || cmd == "less" || cmd == "more" {
			return false
		}

		consumedValue, ok := isHeadTailReadFlag(arg, args[i+1:])
		if !ok {
			return false
		}
		if consumedValue {
			i++
		}
	}
	return pathCount > 0
}

func isSimpleReadPath(path string) bool {
	return path != "" && !strings.ContainsAny(path, "|&;<>()$`*?[]{}")
}

func isCatReadFlag(flag string) bool {
	if !strings.HasPrefix(flag, "-") || flag == "-" {
		return false
	}
	if strings.HasPrefix(flag, "--") {
		switch flag {
		case "--number", "--number-nonblank", "--squeeze-blank", "--show-ends", "--show-tabs", "--show-all":
			return true
		default:
			return false
		}
	}
	for _, r := range strings.TrimPrefix(flag, "-") {
		if !strings.ContainsRune("AbEnstTv", r) {
			return false
		}
	}
	return true
}

func isHeadTailReadFlag(flag string, remaining []string) (bool, bool) {
	switch {
	case flag == "-q" || flag == "-v" || flag == "--quiet" || flag == "--silent" || flag == "--verbose":
		return false, true
	case flag == "-n" || flag == "-c" || flag == "--lines" || flag == "--bytes":
		return len(remaining) > 0 && isCountArg(remaining[0]), len(remaining) > 0 && isCountArg(remaining[0])
	case strings.HasPrefix(flag, "--lines=") || strings.HasPrefix(flag, "--bytes="):
		return false, isCountArg(strings.TrimPrefix(strings.TrimPrefix(flag, "--lines="), "--bytes="))
	case strings.HasPrefix(flag, "-n") || strings.HasPrefix(flag, "-c"):
		return false, isCountArg(flag[2:])
	case len(flag) > 1 && flag[0] == '-' && isCountArg(flag[1:]):
		return false, true
	default:
		return false, false
	}
}

func isCountArg(value string) bool {
	value = strings.TrimPrefix(value, "+")
	value = strings.TrimPrefix(value, "-")
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func semanticBashBooleanSchema() []map[string]any {
	return []map[string]any{
		{"type": "boolean"},
		{"type": "string", "enum": []string{"true", "false"}},
	}
}

func semanticBashIntegerSchema() []map[string]any {
	return []map[string]any{
		{"type": "integer"},
		{"type": "string", "pattern": `^-?\d+$`},
	}
}

func parseTimeout(args map[string]any) time.Duration {
	timeoutSeconds := defaultBashTimeoutSeconds
	if v, ok := args["timeout_ms"]; ok {
		timeoutMS, _ := common.ToSemanticInt(v)
		if timeoutMS <= 0 {
			return time.Duration(timeoutSeconds) * time.Second
		}
		return clampTimeout(time.Duration(timeoutMS) * time.Millisecond)
	}

	if v, ok := args["timeout"]; ok {
		timeout, _ := common.ToSemanticInt(v)
		if timeout <= 0 {
			return time.Duration(timeoutSeconds) * time.Second
		}
		// Ambiguity heuristic: values within the max-seconds budget are read as
		// seconds, larger values as Claude-style milliseconds. This makes the
		// boundary discontinuous (600 -> 600s, 601 -> 601ms), which is fine
		// because 601s would exceed maxBashTimeoutSeconds and get clamped anyway.
		if timeout > maxBashTimeoutSeconds {
			return clampTimeout(time.Duration(timeout) * time.Millisecond)
		}
		return time.Duration(timeout) * time.Second
	}

	return time.Duration(timeoutSeconds) * time.Second
}

func clampTimeout(timeout time.Duration) time.Duration {
	if timeout > time.Duration(maxBashTimeoutSeconds)*time.Second {
		return time.Duration(maxBashTimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		return time.Duration(defaultBashTimeoutSeconds) * time.Second
	}
	return timeout
}

func formatTimeout(timeout time.Duration) string {
	if timeout%time.Second == 0 {
		return fmt.Sprintf("%.0f seconds", timeout.Seconds())
	}
	return fmt.Sprintf("%d milliseconds", timeout.Milliseconds())
}
