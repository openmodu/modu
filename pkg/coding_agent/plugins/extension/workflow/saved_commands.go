package workflow

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
)

var savedWorkflowCommandNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type savedWorkflowCommand struct {
	Name string
	Path string
}

func (e *Extension) registerSavedWorkflowCommands(api extension.ExtensionAPI) {
	workflows, err := discoverSavedWorkflowCommands(api.Cwd(), api.AgentDir())
	if err != nil {
		e.tell(fmt.Sprintf("Saved workflow command discovery failed: %v", err))
		return
	}
	for _, wf := range workflows {
		name := wf.Name
		path := wf.Path
		if savedWorkflowDirectCommandAvailable(api, name) {
			api.RegisterCommand(name, "Run saved workflow: /"+name+" [json-args]", func(args string) error {
				return e.cmdSavedWorkflow(name, path, args, "/"+name)
			})
		}
		api.RegisterCommand("workflow:"+name, "Run saved workflow: /workflow:"+name+" [json-args]", func(args string) error {
			return e.cmdSavedWorkflow(name, path, args, "/workflow:"+name)
		})
	}
}

var savedWorkflowReservedCommands = map[string]struct{}{
	"chain":            {},
	"compact":          {},
	"config":           {},
	"deep-research":    {},
	"effort":           {},
	"fork":             {},
	"goal":             {},
	"goal-cancel":      {},
	"goal-pause":       {},
	"goal-resume":      {},
	"goal-status":      {},
	"goal-watch":       {},
	"help":             {},
	"model":            {},
	"parallel":         {},
	"retry":            {},
	"run":              {},
	"session":          {},
	"subagents-doctor": {},
	"thinking":         {},
	"tools":            {},
	"tree":             {},
	"workflow":         {},
	"workflows":        {},
}

func savedWorkflowDirectCommandAvailable(api extension.ExtensionAPI, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if _, reserved := savedWorkflowReservedCommands[name]; reserved {
		return false
	}
	for _, cmd := range api.GetCommands() {
		if cmd.Name == name {
			return false
		}
	}
	return true
}

func discoverSavedWorkflowCommands(cwd, agentDir string) ([]savedWorkflowCommand, error) {
	type root struct {
		dir string
	}
	var roots []root
	for _, dir := range projectWorkflowDirs(cwd) {
		roots = append(roots, root{dir: dir})
	}
	for _, dir := range userWorkflowDirs(agentDir) {
		roots = append(roots, root{dir: dir})
	}
	seen := map[string]bool{}
	var out []savedWorkflowCommand
	for _, root := range roots {
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("list saved workflows %s: %w", root.dir, err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".js" {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".js")
			if !savedWorkflowCommandNameRE.MatchString(name) || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, savedWorkflowCommand{
				Name: name,
				Path: filepath.Join(root.dir, entry.Name()),
			})
		}
	}
	return out, nil
}

func (e *Extension) cmdSavedWorkflow(name, path, argsText, source string) error {
	args, err := decodeSavedWorkflowCommandArgs(argsText)
	if err != nil {
		e.tell(fmt.Sprintf("Workflow %s: %v", name, err))
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read saved workflow %s: %w", path, err)
	}
	script := normalizeScript(string(data))
	if script == "" {
		e.tell("Workflow " + name + ": saved workflow script is empty")
		return nil
	}
	scriptPath, runDir, err := persistWorkflowScript(e.api.SessionDir(), script)
	if err != nil {
		return err
	}
	exec := workflowExecution{
		Script:      script,
		Args:        args,
		Concurrency: e.cfg.Concurrency,
		MaxAgents:   e.cfg.MaxAgents,
		ScriptPath:  scriptPath,
		RunDir:      runDir,
	}
	if !e.approveWorkflowRun(exec, source) {
		e.tell("Workflow " + name + " cancelled before start.")
		return nil
	}
	runID := e.startBackgroundWorkflow(exec)
	text := fmt.Sprintf("Workflow %s started in background.\nRun: %s", name, runID)
	if scriptPath != "" {
		text += "\nScript: " + scriptPath
	}
	text += "\n" + workflowStartGuidance(runID)
	e.tell(text)
	return nil
}

func decodeSavedWorkflowCommandArgs(text string) (any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("args must be valid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("args must contain a single JSON value")
		}
		return nil, fmt.Errorf("args must be valid JSON: %w", err)
	}
	return value, nil
}
