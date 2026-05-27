package subagent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/coding_agent/extension"
	csubagent "github.com/openmodu/modu/pkg/coding_agent/subagent"
)

// runSingle delegates one (agent, task) pair via ForkSession and returns
// the child's final text. Errors include "agent not found" and any
// failures bubbling up from ForkSession.
func runSingle(ctx context.Context, ext *Extension, args map[string]any) (string, error) {
	agentName, _ := args["agent"].(string)
	task, _ := args["task"].(string)
	if agentName == "" {
		return "", fmt.Errorf("single mode requires \"agent\"")
	}
	if task == "" {
		return "", fmt.Errorf("single mode requires \"task\"")
	}
	output := decodeOutputOptions(args)
	outputPath, err := resolveForkOutputPath(ext, output.path)
	if err != nil {
		return "", err
	}
	reads, err := decodeReadOptions(args["reads"])
	if err != nil {
		return "", err
	}
	progress, err := decodeProgressOption(args["progress"])
	if err != nil {
		return "", err
	}
	skill, err := decodeSkillOverride(args["skill"])
	if err != nil {
		return "", err
	}
	chainDir, _ := args["chainDir"].(string)
	model, _ := args["model"].(string)
	contextMode, _ := args["context"].(string)
	cwd, _ := args["cwd"].(string)
	thinking, _ := args["thinking"].(string)
	sessionDir, _ := args["sessionDir"].(string)
	task = applyTemplateVars(task, substitutions{
		chainDir: resolveChainDirForSubst(ext, chainDir),
	})
	background := optionalBool(args, "async")
	// force_top_level_async only fires when the caller omits async. An
	// explicit async:false still wins so callers can opt out of the global
	// default per call.
	if background == nil && ext.cfg.ForceTopLevelAsync {
		yes := true
		background = &yes
	}
	return forkOne(ctx, ext, agentName, task, callOptions{
		background:    background,
		outputPath:    outputPath,
		outputMode:    output.mode,
		reads:         reads,
		progress:      progress,
		progressFirst: true,
		chainDir:      chainDir,
		model:         model,
		skill:         skill,
		contextMode:   contextMode,
		cwd:           cwd,
		thinking:      thinking,
		sessionDir:    sessionDir,
	})
}

// runParallel fans out an array of (agent, task) pairs as concurrent
// ForkSession calls. The aggregated result is one human-readable block
// per call, ordered by request index. One pair's failure does NOT
// cancel the rest — each pair's outcome is reported independently so
// the caller can act on partial success.
func runParallel(ctx context.Context, ext *Extension, args map[string]any) (string, error) {
	raw := args["parallel"]
	kind := "parallel"
	if raw == nil {
		raw = args["tasks"]
		kind = "tasks"
	}
	calls, err := decodeCallList(raw, kind)
	if err != nil {
		return "", err
	}
	if len(calls) == 0 {
		return "", fmt.Errorf("parallel mode requires a non-empty \"parallel\" array")
	}
	concurrency, err := optionalPositiveInt(args["concurrency"])
	if err != nil {
		return "", err
	}
	topChainDir, _ := args["chainDir"].(string)
	topContext, _ := args["context"].(string)
	topWorktree, _ := args["worktree"].(bool)
	topSessionDir, _ := args["sessionDir"].(string)
	progressCreated := false
	return runParallelCalls(ctx, ext, calls, parallelOptions{
		chainDir:        topChainDir,
		contextMode:     topContext,
		concurrency:     concurrency,
		resolvedChain:   resolveChainDirForSubst(ext, topChainDir),
		worktree:        topWorktree,
		sessionDir:      topSessionDir,
		progressCreated: &progressCreated,
	})
}

type outcome struct {
	text string
	err  error
}

// substitutions captures the template variables we replace inside task
// strings before dispatching the child. Mirrors the {previous} / {task} /
// {chain_dir} contract from pi-subagents.
type substitutions struct {
	previous string // prior chain step's reply, "" for the first step / top-level parallel
	task     string // chain's first sequential step's raw task, "" outside chain context
	chainDir string // resolved (absolute) shared chain dir, "" when unresolvable
}

func applyTemplateVars(task string, subs substitutions) string {
	task = strings.ReplaceAll(task, "{previous}", subs.previous)
	task = strings.ReplaceAll(task, "{task}", subs.task)
	task = strings.ReplaceAll(task, "{chain_dir}", subs.chainDir)
	return task
}

// resolveChainDirForSubst returns the absolute chain dir to expose via
// {chain_dir}. Falls back to the default subagents runtime dir when chainDir
// is unset so the variable always points somewhere usable.
func resolveChainDirForSubst(ext *Extension, chainDir string) string {
	if strings.TrimSpace(chainDir) != "" {
		return resolveBasePath(ext, chainDir, "")
	}
	if ext != nil && ext.api != nil {
		return filepath.Join(ext.api.AgentDir(), "tool-results", projectKey(ext.api.Cwd()), "subagents")
	}
	return ""
}

type parallelOptions struct {
	chainDir        string
	contextMode     string
	concurrency     int
	previous        string
	chainTask       string
	resolvedChain   string
	failFast        bool
	worktree        bool
	sessionDir      string
	progressCreated *bool
}

func runParallelCalls(ctx context.Context, ext *Extension, calls []callSpec, opts parallelOptions) (string, error) {
	results := make([]outcome, len(calls))
	firstProgress := firstProgressCall(ext, calls, opts.progressCreated)
	var sem chan struct{}
	if opts.concurrency > 0 && opts.concurrency < len(calls) {
		sem = make(chan struct{}, opts.concurrency)
	}
	childCtx := ctx
	var cancel context.CancelFunc
	if opts.failFast {
		childCtx, cancel = context.WithCancel(ctx)
		defer cancel()
	}
	var (
		failMu   sync.Mutex
		firstErr error
	)
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func(idx int, call callSpec) {
			defer wg.Done()
			if sem != nil {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-childCtx.Done():
					results[idx] = outcome{err: childCtx.Err()}
					return
				}
			}
			var text string
			outputPath, err := resolveForkOutputPath(ext, call.output)
			if err == nil {
				chainDir := strings.TrimSpace(call.chainDir)
				if chainDir == "" {
					chainDir = opts.chainDir
				}
				// {chain_dir} prefers the per-item chainDir when set so a
				// nested group can carry its own subdir; otherwise inherits
				// the surrounding chain's resolved value.
				resolvedChain := opts.resolvedChain
				if strings.TrimSpace(call.chainDir) != "" {
					resolvedChain = resolveChainDirForSubst(ext, call.chainDir)
				}
				task := applyTemplateVars(call.task, substitutions{
					previous: opts.previous,
					task:     opts.chainTask,
					chainDir: resolvedChain,
				})
				isolationOverride := ""
				if opts.worktree {
					isolationOverride = "worktree"
				}
				text, err = forkOne(childCtx, ext, call.agent, task, callOptions{
					outputPath:    outputPath,
					outputMode:    call.outputMode,
					reads:         call.reads,
					progress:      call.progress,
					progressFirst: idx == firstProgress,
					chainDir:      chainDir,
					model:         call.model,
					skill:         call.skill,
					contextMode:   opts.contextMode,
					cwd:           call.cwd,
					isolation:     isolationOverride,
					thinking:      call.thinking,
					sessionDir:    opts.sessionDir,
				})
			}
			if err == nil {
				text, err = applyOutputOptions(ext, map[string]any{"output": call.output, "outputMode": call.outputMode}, text)
			}
			results[idx] = outcome{text: text, err: err}
			if err != nil && opts.failFast {
				failMu.Lock()
				if firstErr == nil {
					firstErr = err
					if cancel != nil {
						cancel()
					}
				}
				failMu.Unlock()
			}
		}(i, c)
	}
	wg.Wait()

	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "## [%d] %s\n", i, calls[i].agent)
		if r.err != nil {
			fmt.Fprintf(&b, "ERROR: %v\n", r.err)
		} else {
			b.WriteString(strings.TrimSpace(r.text))
			b.WriteString("\n")
		}
		if i < len(results)-1 {
			b.WriteString("\n")
		}
	}
	// firstErr is non-nil only when failFast=true and at least one child
	// errored. Callers (chain dispatch) use it to abort; top-level parallel
	// passes failFast=false and ignores the error.
	return b.String(), firstErr
}

// runChain runs (agent, task) pairs in sequence. Each step's task may
// contain the literal "{previous}" token, which is substituted with the
// prior step's reply before dispatch. The first step sees "{previous}" as
// the empty string. The final step's reply is the chain's overall result.
//
// A failure in any step aborts the chain and surfaces that step's error.
func runChain(ctx context.Context, ext *Extension, args map[string]any) (string, error) {
	steps, err := decodeChainSteps(args["chain"])
	if err != nil {
		return "", err
	}
	if len(steps) == 0 {
		return "", fmt.Errorf("chain mode requires a non-empty \"chain\" array")
	}

	previous := ""
	progressCreated := false
	topContext, _ := args["context"].(string)
	topChainDir, _ := args["chainDir"].(string)
	topSessionDir, _ := args["sessionDir"].(string)
	topConcurrency, err := optionalPositiveInt(args["concurrency"])
	if err != nil {
		return "", err
	}
	// chainTask anchors {task} across the chain. Use the first sequential
	// step's raw task; if the chain opens with a parallel group there is no
	// natural anchor, so leave it empty.
	chainTask := ""
	if len(steps) > 0 && !steps[0].parallel {
		chainTask = steps[0].call.task
	}
	topResolvedChainDir := resolveChainDirForSubst(ext, topChainDir)
	for i, step := range steps {
		if step.parallel {
			chainDir := strings.TrimSpace(step.chainDir)
			if chainDir == "" {
				chainDir = topChainDir
			}
			resolvedChain := topResolvedChainDir
			if strings.TrimSpace(step.chainDir) != "" {
				resolvedChain = resolveChainDirForSubst(ext, step.chainDir)
			}
			concurrency := step.concurrency
			if concurrency == 0 {
				concurrency = topConcurrency
			}
			text, err := runParallelCalls(ctx, ext, step.calls, parallelOptions{
				chainDir:        chainDir,
				contextMode:     topContext,
				concurrency:     concurrency,
				previous:        previous,
				chainTask:       chainTask,
				resolvedChain:   resolvedChain,
				failFast:        step.failFast,
				worktree:        step.worktree,
				sessionDir:      topSessionDir,
				progressCreated: &progressCreated,
			})
			if err != nil {
				return "", fmt.Errorf("chain step %d (parallel, fail-fast): %w", i, err)
			}
			previous = text
			continue
		}
		c := step.call
		chainDir := strings.TrimSpace(c.chainDir)
		if chainDir == "" {
			chainDir = topChainDir
		}
		resolvedChain := topResolvedChainDir
		if strings.TrimSpace(c.chainDir) != "" {
			resolvedChain = resolveChainDirForSubst(ext, c.chainDir)
		}
		task := applyTemplateVars(c.task, substitutions{
			previous: previous,
			task:     chainTask,
			chainDir: resolvedChain,
		})
		outputPath, err := resolveForkOutputPath(ext, c.output)
		if err != nil {
			return "", fmt.Errorf("chain step %d (%s): %w", i, c.agent, err)
		}
		progressFirst := false
		if callUsesProgress(ext, c) && !progressCreated {
			progressFirst = true
			progressCreated = true
		}
		text, err := forkOne(ctx, ext, c.agent, task, callOptions{
			outputPath:    outputPath,
			outputMode:    c.outputMode,
			reads:         c.reads,
			progress:      c.progress,
			progressFirst: progressFirst,
			chainDir:      chainDir,
			model:         c.model,
			skill:         c.skill,
			contextMode:   topContext,
			cwd:           c.cwd,
			thinking:      c.thinking,
			sessionDir:    topSessionDir,
		})
		if err != nil {
			return "", fmt.Errorf("chain step %d (%s): %w", i, c.agent, err)
		}
		text, err = applyOutputOptions(ext, map[string]any{"output": c.output, "outputMode": c.outputMode}, text)
		if err != nil {
			return "", fmt.Errorf("chain step %d (%s): %w", i, c.agent, err)
		}
		previous = text
	}
	return previous, nil
}

type chainStep struct {
	parallel    bool
	call        callSpec
	calls       []callSpec
	chainDir    string
	concurrency int
	failFast    bool
	worktree    bool
}

// callSpec is one (agent, task) entry inside a parallel or chain list.
type callSpec struct {
	agent      string
	task       string
	output     string
	outputMode string
	reads      readOptions
	progress   *bool
	chainDir   string
	model      string
	skill      skillOverride
	cwd        string
	thinking   string
}

type callOptions struct {
	background    *bool
	parentID      string
	outputPath    string
	outputMode    string
	reads         readOptions
	progress      *bool
	progressFirst bool
	chainDir      string
	model         string
	skill         skillOverride
	contextMode   string
	cwd           string
	// isolation, when set, overrides the profile's def.Isolation. Used by
	// callers (top-level parallel/tasks and chain[].parallel groups) that
	// want to force `worktree:true` regardless of the profile.
	isolation string
	// thinking, when set, overrides the profile's ThinkingLevel for this
	// call only. Empty means "inherit profile".
	thinking string
	// sessionDir, when non-empty, requests the host to place this child's
	// per-run files under a caller-supplied parent path. Only meaningful
	// for background forks; ignored otherwise.
	sessionDir string
}

// decodeCallList validates and unpacks `args["parallel"]` / `args["chain"]`.
// kind is included in error messages so the caller learns which field went
// wrong without inspecting the source.
func decodeCallList(raw any, kind string) ([]callSpec, error) {
	return decodeCallListWithDefault(raw, kind, false)
}

func decodeCallListWithDefault(raw any, kind string, defaultPrevious bool) ([]callSpec, error) {
	if raw == nil {
		return nil, fmt.Errorf("%s mode requires a %q array", kind, kind)
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%q must be an array, got %T", kind, raw)
	}
	out := make([]callSpec, 0, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: expected object, got %T", kind, i, item)
		}
		calls, err := decodeCallObject(obj, fmt.Sprintf("%s[%d]", kind, i), defaultPrevious)
		if err != nil {
			return nil, err
		}
		out = append(out, calls...)
	}
	return out, nil
}

func decodeChainSteps(raw any) ([]chainStep, error) {
	if raw == nil {
		return nil, fmt.Errorf("chain mode requires a \"chain\" array")
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("\"chain\" must be an array, got %T", raw)
	}
	steps := make([]chainStep, 0, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("chain[%d]: expected object, got %T", i, item)
		}
		if rawParallel, ok := obj["parallel"]; ok {
			calls, err := decodeCallListWithDefault(rawParallel, fmt.Sprintf("chain[%d].parallel", i), true)
			if err != nil {
				return nil, err
			}
			if len(calls) == 0 {
				return nil, fmt.Errorf("chain[%d].parallel requires a non-empty array", i)
			}
			concurrency, err := optionalPositiveInt(obj["concurrency"])
			if err != nil {
				return nil, fmt.Errorf("chain[%d]: %w", i, err)
			}
			chainDir, _ := obj["chainDir"].(string)
			failFast, _ := obj["failFast"].(bool)
			worktree, _ := obj["worktree"].(bool)
			steps = append(steps, chainStep{
				parallel:    true,
				calls:       calls,
				chainDir:    chainDir,
				concurrency: concurrency,
				failFast:    failFast,
				worktree:    worktree,
			})
			continue
		}
		calls, err := decodeCallObject(obj, fmt.Sprintf("chain[%d]", i), false)
		if err != nil {
			return nil, err
		}
		if len(calls) != 1 {
			return nil, fmt.Errorf("chain[%d]: count is only supported inside parallel groups", i)
		}
		steps = append(steps, chainStep{call: calls[0]})
	}
	return steps, nil
}

func decodeCallObject(obj map[string]any, label string, defaultPrevious bool) ([]callSpec, error) {
	agent, _ := obj["agent"].(string)
	task, _ := obj["task"].(string)
	if agent == "" {
		return nil, fmt.Errorf("%s: missing \"agent\"", label)
	}
	if task == "" {
		if defaultPrevious {
			task = "{previous}"
		} else {
			return nil, fmt.Errorf("%s: missing \"task\"", label)
		}
	}
	output, _ := obj["output"].(string)
	outputMode, _ := obj["outputMode"].(string)
	count, err := optionalItemCount(obj["count"])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	reads, err := decodeReadOptions(obj["reads"])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	progress, err := decodeProgressOption(obj["progress"])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	skill, err := decodeSkillOverride(obj["skill"])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	chainDir, _ := obj["chainDir"].(string)
	model, _ := obj["model"].(string)
	cwd, _ := obj["cwd"].(string)
	thinking, _ := obj["thinking"].(string)
	out := make([]callSpec, 0, count)
	for repeat := 0; repeat < count; repeat++ {
		out = append(out, callSpec{
			agent:      agent,
			task:       task,
			output:     output,
			outputMode: outputMode,
			reads:      reads,
			progress:   progress,
			chainDir:   chainDir,
			model:      model,
			skill:      skill,
			cwd:        cwd,
			thinking:   thinking,
		})
	}
	return out, nil
}

func optionalItemCount(raw any) (int, error) {
	if raw == nil {
		return 1, nil
	}
	n, ok := numericInt(raw)
	if !ok || n < 1 {
		return 0, fmt.Errorf("count must be an integer >= 1")
	}
	return n, nil
}

func optionalPositiveInt(raw any) (int, error) {
	if raw == nil {
		return 0, nil
	}
	n, ok := numericInt(raw)
	if !ok || n < 1 {
		return 0, fmt.Errorf("concurrency must be an integer >= 1")
	}
	return n, nil
}

func numericInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

func firstProgressCall(ext *Extension, calls []callSpec, progressCreated *bool) int {
	if progressCreated != nil && *progressCreated {
		return -1
	}
	for i, call := range calls {
		if callUsesProgress(ext, call) {
			if progressCreated != nil {
				*progressCreated = true
			}
			return i
		}
	}
	return -1
}

// forkOne resolves the named profile and dispatches one ForkSession call.
// Returns "agent not found" if the loader has no matching entry — that's
// always a user error rather than a system failure, so the message is
// short and explicit.
func forkOne(ctx context.Context, ext *Extension, agentName, task string, opts callOptions) (string, error) {
	def, ok := ext.loader.Get(agentName)
	if !ok {
		return "", fmt.Errorf("agent %q not found", agentName)
	}
	depth := subagentDepth(ctx)
	if ext.cfg.MaxDepth >= 0 && depth >= ext.cfg.MaxDepth {
		return "", fmt.Errorf("subagent max_depth=%d reached; %q cannot spawn another subagent", ext.cfg.MaxDepth, agentName)
	}
	task, err := applyTaskBehavior(ext, def, task, opts)
	if err != nil {
		return "", err
	}
	return ext.api.ForkSession(withSubagentDepth(ctx, depth+1), forkOptionsFor(def, ext.cfg, task, opts))
}

// forkOptionsFor translates a stored SubagentDefinition into an
// ExtensionAPI ForkOptions request, layering DefaultModel on top when the
// profile leaves Model empty.
//
// Isolation / Skills / MemoryScope come from the profile. Background normally
// does too, but single-mode callers can override it with async for pi-style
// one-off background delegation.
func forkOptionsFor(def *csubagent.SubagentDefinition, cfg Config, task string, opts callOptions) extension.ForkOptions {
	background := def.Background
	if opts.background != nil {
		background = *opts.background
	}
	return extension.ForkOptions{
		Name:            def.Name,
		SystemPrompt:    def.SystemPrompt,
		Task:            task,
		AllowedTools:    def.Tools,
		DisallowedTools: def.DisallowedTools,
		Model:           effectiveModel(def, cfg, opts.model),
		Context:         effectiveContext(def, opts.contextMode),
		Cwd:             strings.TrimSpace(opts.cwd),
		ThinkingLevel:   effectiveThinking(def, opts.thinking),
		PermissionMode:  def.PermissionMode,
		MaxTurns:        def.MaxTurns,
		Background:      background,
		ParentTaskID:    opts.parentID,
		OutputPath:      opts.outputPath,
		OutputMode:      opts.outputMode,
		Isolation:       effectiveIsolation(def, opts.isolation),
		Skills:          effectiveSkills(def, opts.skill),
		MemoryScope:     def.MemoryScope,
		SessionDir:      strings.TrimSpace(opts.sessionDir),
	}
}

func optionalBool(args map[string]any, key string) *bool {
	raw, ok := args[key]
	if !ok {
		return nil
	}
	value, ok := raw.(bool)
	if !ok {
		return nil
	}
	return &value
}
