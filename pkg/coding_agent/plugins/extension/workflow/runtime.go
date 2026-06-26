package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

// structuredOutputMaxRetries bounds how many extra attempts an agent gets to
// satisfy its JSON schema before the call is treated as failed. Kept >1 so a
// single malformed response does not drop a result the workflow depends on.
const structuredOutputMaxRetries = 2

// jsPrelude defines parallel()/pipeline() in terms of native Promises. Because
// agent() returns a Promise resolved from a Go goroutine, Promise.all fans the
// real work out concurrently; the actual cap on simultaneous ForkSession calls
// is enforced inside runAgentAttempt via a shared semaphore. pipeline() runs
// each item through every stage independently with no barrier between stages,
// matching Claude-Code semantics. A throwing thunk/stage drops that slot to
// null rather than failing the whole batch.
const jsPrelude = `
globalThis.parallel = async function parallel(thunks) {
  if (!Array.isArray(thunks)) throw new TypeError("parallel() requires an array of functions");
  return await Promise.all(thunks.map(async (t) => {
    if (typeof t !== "function") throw new TypeError("parallel() items must be functions returning a promise");
    try { return await t(); } catch (e) { return null; }
  }));
};
globalThis.pipeline = async function pipeline(items, ...stages) {
  if (!Array.isArray(items)) throw new TypeError("pipeline() requires an array of items");
  if (stages.length === 0) throw new TypeError("pipeline() requires at least one stage");
  return await Promise.all(items.map(async (item, index) => {
    let value = item;
    for (const stage of stages) {
      try {
        value = await stage(value, item, index);
      } catch (e) {
        log("pipeline[" + index + "] failed: " + (e && e.message ? e.message : String(e)));
        return null;
      }
    }
    return value;
  }));
};
`

type runOptions struct {
	Cwd         string
	AgentDir    string
	Args        any
	Concurrency int
	BudgetTotal int
	MaxAgents   int
	ScriptPath  string
	RunDir      string
	OnUpdate    types.ToolUpdateCallback
	NestedDepth int
	State       *workflowRunState
	Resume      bool
	Activities  *workflowActivityRegistry
	Registry    *workflowRegistry
}

type runResult struct {
	Meta     metaInfo
	Result   any
	Snapshot workflowSnapshot
}

type runner struct {
	api        extension.ExtensionAPI
	opts       runOptions
	state      *workflowRunState
	tracker    *snapshotTracker
	meta       *metaInfo
	current    string
	usedAgent  atomic.Bool
	mu         sync.Mutex
	ctx        context.Context
	loop       *eventloop.EventLoop
	vm         *goja.Runtime
	jsonParse  goja.Callable
}

type workflowRunState struct {
	mu         sync.Mutex
	agentCount int
	spent      int
	reserved   int
	cache      *workflowAgentCache
	cursor     map[string]int

	semOnce sync.Once
	sem     chan struct{}
}

type workflowAgentReservation struct {
	Index          int
	BudgetReserved bool
}

type workflowReserveStatus string

const (
	workflowReserveOK              workflowReserveStatus = "ok"
	workflowReserveAgentLimit      workflowReserveStatus = "agent_limit"
	workflowReserveBudgetExhausted workflowReserveStatus = "budget_exhausted"
)

func newRunner(api extension.ExtensionAPI, opts runOptions) *runner {
	state := opts.State
	if state == nil {
		state = &workflowRunState{}
	}
	if state.cursor == nil {
		state.cursor = map[string]int{}
	}
	r := &runner{
		api:     api,
		opts:    opts,
		state:   state,
		tracker: newSnapshotTracker(opts.OnUpdate),
	}
	r.tracker.setScript(opts.ScriptPath, opts.RunDir)
	return r
}

func (s *workflowRunState) reserveAgent(maxAgents, budgetTotal int) (workflowAgentReservation, workflowReserveStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxAgents > 0 && s.agentCount >= maxAgents {
		return workflowAgentReservation{}, workflowReserveAgentLimit
	}
	if budgetTotal > 0 && s.spent+s.reserved >= budgetTotal {
		return workflowAgentReservation{}, workflowReserveBudgetExhausted
	}
	s.agentCount++
	reservation := workflowAgentReservation{Index: s.agentCount}
	if budgetTotal > 0 {
		s.reserved++
		reservation.BudgetReserved = true
	}
	return reservation, workflowReserveOK
}

func (s *workflowRunState) releaseBudgetReservation(reservation workflowAgentReservation) {
	if !reservation.BudgetReserved {
		return
	}
	s.mu.Lock()
	if s.reserved > 0 {
		s.reserved--
	}
	s.mu.Unlock()
}

// releaseReservation fully undoes reserveAgent (both the agent-count slot and the
// budget reservation). Used when an attempt is going to be redone — e.g. a user
// restart — so retrying the same logical agent doesn't permanently consume slots
// toward MaxAgents.
func (s *workflowRunState) releaseReservation(reservation workflowAgentReservation) {
	s.mu.Lock()
	if s.agentCount > 0 {
		s.agentCount--
	}
	if reservation.BudgetReserved && s.reserved > 0 {
		s.reserved--
	}
	s.mu.Unlock()
}

func (s *workflowRunState) commitBudgetSpend(reservation workflowAgentReservation, spent, budgetTotal int) {
	if spent < 0 {
		spent = 0
	}
	s.mu.Lock()
	if reservation.BudgetReserved && s.reserved > 0 {
		s.reserved--
	}
	s.spent += spent
	if budgetTotal > 0 && s.spent > budgetTotal {
		s.spent = budgetTotal
	}
	s.mu.Unlock()
}

// concurrencyLimit clamps the configured concurrency to a safe range. Applies to
// the whole run (shared across parallel/pipeline and nested workflows).
func concurrencyLimit(n int) int {
	if n <= 0 {
		n = 4
	}
	if n > 16 {
		n = 16
	}
	return n
}

func (r *runner) acquireSlot(ctx context.Context) error {
	r.state.semOnce.Do(func() {
		r.state.sem = make(chan struct{}, concurrencyLimit(r.opts.Concurrency))
	})
	select {
	case r.state.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *runner) releaseSlot() {
	select {
	case <-r.state.sem:
	default:
	}
}

func (r *runner) run(ctx context.Context, script string) (runResult, error) {
	r.ctx = ctx
	loop := eventloop.NewEventLoop(eventloop.EnableConsole(false))
	r.loop = loop

	var (
		result  any
		runErr  string
		hadErr  bool
		setupErr error
		done    = make(chan struct{})
		once    sync.Once
	)
	finish := func() { once.Do(func() { close(done) }) }

	loop.Start()
	loop.RunOnLoop(func(vm *goja.Runtime) {
		if err := r.installGlobals(vm); err != nil {
			setupErr = err
			finish()
			return
		}
		vm.Set("__resolve", func(call goja.FunctionCall) goja.Value {
			result = call.Argument(0).Export()
			finish()
			return goja.Undefined()
		})
		vm.Set("__reject", func(call goja.FunctionCall) goja.Value {
			runErr = call.Argument(0).String()
			hadErr = true
			finish()
			return goja.Undefined()
		})
		wrapped := jsPrelude + "\n(async () => { try { const __r = await (async () => {\n" +
			script + "\n})(); __resolve(__r === undefined ? null : __r); } catch (__e) { __reject(__e && __e.stack ? __e.stack : String(__e)); } })();"
		if _, err := vm.RunString(wrapped); err != nil {
			runErr = err.Error()
			hadErr = true
			finish()
		}
	})

	select {
	case <-done:
	case <-ctx.Done():
		loop.Terminate()
		r.tracker.skipRunning("aborted")
		return runResult{Meta: r.metaOrZero(), Snapshot: r.tracker.current()}, ctx.Err()
	}
	loop.Stop()

	if setupErr != nil {
		return runResult{Snapshot: r.tracker.current()}, setupErr
	}
	if ctx.Err() != nil {
		r.tracker.skipRunning("aborted")
		return runResult{Meta: r.metaOrZero(), Snapshot: r.tracker.current()}, ctx.Err()
	}
	if hadErr {
		return runResult{Meta: r.metaOrZero(), Snapshot: r.tracker.current()}, fmt.Errorf("%s", runErr)
	}
	if r.meta == nil {
		return runResult{Snapshot: r.tracker.current()}, fmt.Errorf("meta({name:..., description:...}) is required")
	}
	if !r.usedAgent.Load() {
		return runResult{Meta: *r.meta, Snapshot: r.tracker.current()}, fmt.Errorf("workflow scripts must call agent() or parallel() at least once")
	}
	snapshot := r.tracker.complete(result)
	return runResult{Meta: *r.meta, Result: result, Snapshot: snapshot}, nil
}

func (r *runner) metaOrZero() metaInfo {
	if r.meta != nil {
		return *r.meta
	}
	return metaInfo{}
}

func (r *runner) installGlobals(vm *goja.Runtime) error {
	r.vm = vm
	parse, ok := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	if !ok {
		return fmt.Errorf("JSON.parse unavailable")
	}
	r.jsonParse = parse

	must := func(err error) {
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
	}
	must(vm.Set("meta", r.jsMeta))
	must(vm.Set("phase", r.jsPhase))
	must(vm.Set("log", r.jsLog))
	must(vm.Set("agent", r.jsAgent))
	must(vm.Set("workflow", r.jsWorkflow))
	must(vm.Set("args", r.toJS(vm, r.opts.Args)))
	must(vm.Set("cwd", r.opts.Cwd))

	process := vm.NewObject()
	must(process.Set("cwd", func(goja.FunctionCall) goja.Value { return vm.ToValue(r.opts.Cwd) }))
	must(vm.Set("process", process))

	budget := vm.NewObject()
	if r.opts.BudgetTotal > 0 {
		must(budget.Set("total", r.opts.BudgetTotal))
	} else {
		must(budget.Set("total", goja.Null()))
	}
	must(budget.Set("spent", func(goja.FunctionCall) goja.Value {
		r.state.mu.Lock()
		spent := r.state.spent
		r.state.mu.Unlock()
		return vm.ToValue(spent)
	}))
	must(budget.Set("remaining", func(goja.FunctionCall) goja.Value {
		if r.opts.BudgetTotal <= 0 {
			return goja.Null()
		}
		r.state.mu.Lock()
		remaining := r.opts.BudgetTotal - r.state.spent
		r.state.mu.Unlock()
		if remaining < 0 {
			remaining = 0
		}
		return vm.ToValue(remaining)
	}))
	must(vm.Set("budget", budget))
	return nil
}

// toJS injects a Go value into the runtime as a NATIVE JS value (via JSON.parse)
// so scripts can use Array/Object prototype methods (.map/.filter/...) on agent
// results and args without host-object quirks. Must be called on the loop.
func (r *runner) toJS(vm *goja.Runtime, value any) goja.Value {
	data, err := json.Marshal(value)
	if err != nil {
		return goja.Null()
	}
	v, err := r.jsonParse(goja.Undefined(), vm.ToValue(string(data)))
	if err != nil {
		return goja.Null()
	}
	return v
}

func (r *runner) requireMeta(vm *goja.Runtime) {
	if r.meta == nil {
		panic(vm.ToValue("meta() must be called before phase/log/agent/parallel/pipeline"))
	}
}

func (r *runner) jsMeta(call goja.FunctionCall) goja.Value {
	vm := r.vm
	if r.meta != nil {
		panic(vm.ToValue("meta() may only be called once"))
	}
	meta, err := decodeMeta(call.Argument(0).Export())
	if err != nil {
		panic(vm.ToValue(err.Error()))
	}
	r.meta = &meta
	r.tracker.setMeta(meta)
	return goja.Undefined()
}

func (r *runner) jsPhase(call goja.FunctionCall) goja.Value {
	vm := r.vm
	r.requireMeta(vm)
	title := strings.TrimSpace(call.Argument(0).String())
	if title == "" {
		panic(vm.ToValue("phase title must be a non-empty string"))
	}
	r.mu.Lock()
	r.current = title
	r.mu.Unlock()
	r.tracker.addPhase(title)
	return goja.Undefined()
}

func (r *runner) jsLog(call goja.FunctionCall) goja.Value {
	vm := r.vm
	r.requireMeta(vm)
	r.tracker.addLog(call.Argument(0).String())
	return goja.Undefined()
}

func (r *runner) jsAgent(call goja.FunctionCall) goja.Value {
	vm := r.vm
	r.requireMeta(vm)
	prompt := call.Argument(0).String()
	opts, err := decodeAgentOptions(call.Argument(1).Export())
	if err != nil {
		panic(vm.ToValue(err.Error()))
	}
	// Capture the active phase synchronously, in script order, so a later
	// phase() call cannot mislabel an already-scheduled agent.
	r.mu.Lock()
	if opts.Phase == "" {
		opts.Phase = r.current
	}
	r.mu.Unlock()
	r.usedAgent.Store(true)

	p, resolve, reject := vm.NewPromise()
	ctx := r.ctx
	go func() {
		outcome, ok, runErr := r.runAgent(ctx, prompt, opts)
		r.loop.RunOnLoop(func(vm *goja.Runtime) {
			switch {
			case runErr != nil:
				reject(vm.ToValue(runErr.Error()))
			case !ok:
				resolve(goja.Null())
			default:
				resolve(r.toJS(vm, outcome.value))
			}
		})
	}()
	return vm.ToValue(p)
}

func (r *runner) jsWorkflow(call goja.FunctionCall) goja.Value {
	vm := r.vm
	r.requireMeta(vm)
	if r.opts.NestedDepth >= 1 {
		panic(vm.ToValue("nested workflow() calls are limited to one level"))
	}
	ref := strings.TrimSpace(call.Argument(0).String())
	if ref == "" {
		panic(vm.ToValue("workflow name or path is required"))
	}
	var childArgs any
	if a := call.Argument(1); !goja.IsUndefined(a) && !goja.IsNull(a) {
		childArgs = a.Export()
	}
	script, sourcePath, err := loadNestedWorkflowScript(ref, r.opts.Cwd, r.opts.AgentDir)
	if err != nil {
		panic(vm.ToValue(err.Error()))
	}

	p, resolve, reject := vm.NewPromise()
	ctx := r.ctx
	go func() {
		child := newRunner(r.api, runOptions{
			Cwd:         r.opts.Cwd,
			AgentDir:    r.opts.AgentDir,
			Args:        childArgs,
			Concurrency: r.opts.Concurrency,
			BudgetTotal: r.opts.BudgetTotal,
			MaxAgents:   r.opts.MaxAgents,
			ScriptPath:  sourcePath,
			NestedDepth: r.opts.NestedDepth + 1,
			State:       r.state,
			Resume:      r.opts.Resume,
			Activities:  r.opts.Activities,
			Registry:    r.opts.Registry,
		})
		res, err := child.run(ctx, script)
		r.loop.RunOnLoop(func(vm *goja.Runtime) {
			if err != nil {
				reject(vm.ToValue(fmt.Sprintf("workflow %q: %s", ref, err.Error())))
				return
			}
			r.usedAgent.Store(true)
			r.tracker.addLog(fmt.Sprintf("nested workflow %s completed with %d agent(s)", ref, res.Snapshot.AgentCount))
			resolve(r.toJS(vm, res.Result))
		})
	}()
	return vm.ToValue(p)
}

func (r *runner) runAgent(ctx context.Context, prompt string, opts agentOptions) (agentOutcome, bool, error) {
	if err := ctx.Err(); err != nil {
		return agentOutcome{}, false, err
	}
	phase := opts.Phase
	r.mu.Lock()
	if phase == "" {
		phase = r.current
	}
	r.mu.Unlock()

	result, ok, retryErr, err := r.runAgentAttempt(ctx, prompt, opts, phase, strings.TrimSpace(opts.Label))
	if err != nil || !ok || retryErr == nil || len(opts.Schema) == 0 {
		return result, ok, err
	}
	for attempt := 1; attempt <= structuredOutputMaxRetries; attempt++ {
		r.tracker.addLog(fmt.Sprintf("agent %s structured output retry %d: %v", result.label, attempt, retryErr))
		retryPrompt := structuredOutputRetryPrompt(prompt, result.text, retryErr)
		retryLabel := result.label + fmt.Sprintf(" retry %d", attempt)
		result, ok, retryErr, err = r.runAgentAttempt(ctx, retryPrompt, opts, phase, retryLabel)
		if err != nil || !ok {
			return agentOutcome{}, ok, err
		}
		if retryErr == nil {
			return result, true, nil
		}
	}
	r.tracker.addLog(fmt.Sprintf("agent %s structured output failed after %d retries: %v", result.label, structuredOutputMaxRetries, retryErr))
	return agentOutcome{}, false, nil
}

func (r *runner) runAgentAttempt(ctx context.Context, prompt string, opts agentOptions, phase, label string) (agentOutcome, bool, error, error) {
	if err := ctx.Err(); err != nil {
		return agentOutcome{}, false, nil, err
	}
	maxAgents := r.opts.MaxAgents
	if maxAgents <= 0 {
		maxAgents = DefaultConfig().MaxAgents
	}

	for {
		if err := ctx.Err(); err != nil {
			return agentOutcome{}, false, nil, err
		}
		reservation, status := r.state.reserveAgent(maxAgents, r.opts.BudgetTotal)
		switch status {
		case workflowReserveAgentLimit:
			r.tracker.addLog(fmt.Sprintf("agent skipped: workflow agent limit exceeded (max %d)", maxAgents))
			return agentOutcome{}, false, nil, nil
		case workflowReserveBudgetExhausted:
			r.tracker.addLog("agent skipped: workflow token budget exhausted")
			return agentOutcome{}, false, nil, nil
		}
		r.usedAgent.Store(true)

		attemptLabel := label
		if attemptLabel == "" {
			if phase != "" {
				attemptLabel = fmt.Sprintf("%s agent %d", phase, reservation.Index)
			} else {
				attemptLabel = fmt.Sprintf("workflow-agent-%d", reservation.Index)
			}
		}
		cacheKey := workflowAgentCacheKey(prompt, phase, attemptLabel, opts)
		if r.opts.Resume && r.state.cache != nil {
			r.state.mu.Lock()
			cursor := r.state.cursor[cacheKey]
			r.state.mu.Unlock()
			if entry, ok := r.state.cache.get(cacheKey, cursor); ok {
				r.state.mu.Lock()
				r.state.cursor[cacheKey] = cursor + 1
				r.state.mu.Unlock()
				r.state.commitBudgetSpend(reservation, entry.Spent, r.opts.BudgetTotal)
				r.tracker.cachedAgent(entry)
				r.tracker.addLog(fmt.Sprintf("agent %s resumed from cache", entry.Label))
				return agentOutcome{label: entry.Label, text: entry.Text, value: entry.Value}, true, nil, nil
			}
		}

		// Bound concurrent ForkSession calls across the whole run. parallel()
		// and pipeline() schedule every agent at once via Promise.all; this
		// semaphore is what actually caps simultaneous child sessions.
		if err := r.acquireSlot(ctx); err != nil {
			r.state.releaseBudgetReservation(reservation)
			r.tracker.addLog("agent skipped: aborted before start")
			return agentOutcome{}, false, nil, err
		}

		startedAt := time.Now()
		id := r.tracker.startAgent(attemptLabel, phase, prompt)
		bubbleID := workflowBubbleID(r.opts.RunDir, id)
		if r.opts.Activities != nil {
			r.opts.Activities.register(bubbleID, id)
		}
		childCtx, cancel := context.WithCancel(ctx)
		runID := workflowRunID(r.opts.RunDir)
		unregisterControl := func() workflowAgentControlAction { return "" }
		if r.opts.Registry != nil && runID != "" {
			unregisterControl = r.opts.Registry.registerAgentControl(runID, id, cancel)
		}
		task := workflowPrompt(prompt, phase, attemptLabel, opts.Schema)
		activity := workflowAgentActivity{}
		text, err := r.api.ForkSession(childCtx, extension.ForkOptions{
			Name:            attemptLabel,
			SystemPrompt:    "",
			Task:            task,
			AllowedTools:    opts.Tools,
			DisallowedTools: opts.DisallowedTools,
			Model:           opts.Model,
			Cwd:             opts.Cwd,
			Isolation:       opts.Isolation,
			PermissionMode:  opts.PermissionMode,
			MaxTurns:        opts.MaxTurns,
			ThinkingLevel:   opts.Thinking,
			Skills:          opts.Skills,
			MemoryScope:     opts.MemoryScope,
			BubbleTaskID:    bubbleID,
		})
		r.releaseSlot()
		action := unregisterControl()
		cancel()
		if r.opts.Activities != nil {
			activity = r.opts.Activities.snapshot(bubbleID)
			r.tracker.updateAgentActivity(id, activity)
			r.opts.Activities.unregister(bubbleID)
		}
		if action == workflowAgentActionStop {
			r.state.releaseBudgetReservation(reservation)
			r.tracker.finishAgent(id, statusSkipped, nil, "stopped by user")
			r.tracker.addLog(fmt.Sprintf("agent %s stopped by user", attemptLabel))
			return agentOutcome{}, false, nil, nil
		}
		if action == workflowAgentActionRestart {
			r.state.releaseReservation(reservation)
			r.tracker.finishAgent(id, statusSkipped, nil, "restart requested")
			r.tracker.addLog(fmt.Sprintf("agent %s restart requested", attemptLabel))
			continue
		}
		if err != nil {
			r.state.releaseBudgetReservation(reservation)
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				r.tracker.finishAgent(id, statusSkipped, nil, "aborted")
				return agentOutcome{}, false, nil, err
			}
			msg := fmt.Sprintf("agent %s failed: %v", attemptLabel, err)
			r.tracker.finishAgent(id, statusError, nil, err.Error())
			r.tracker.addLog(msg)
			return agentOutcome{}, false, nil, nil
		}
		spent := workflowSpendTokens(text, activity)
		r.state.commitBudgetSpend(reservation, spent, r.opts.BudgetTotal)
		out := agentOutcome{label: attemptLabel, text: text, value: text}
		if len(opts.Schema) > 0 {
			value, err := parseStructuredOutput(text, opts.Schema)
			if err != nil {
				r.tracker.finishAgent(id, statusError, text, err.Error(), spent)
				return out, true, err, nil
			}
			out.value = value
		}
		r.tracker.finishAgent(id, statusDone, out.value, "", spent)
		if r.state.cache != nil {
			endedAt := time.Now()
			r.state.cache.add(workflowAgentCacheEntry{
				Key:        cacheKey,
				Label:      attemptLabel,
				Phase:      phase,
				Prompt:     prompt,
				Text:       text,
				Value:      out.value,
				Spent:      spent,
				StartedAt:  startedAt,
				EndedAt:    endedAt,
				DurationMs: endedAt.Sub(startedAt).Milliseconds(),
			})
		}
		return out, true, nil, nil
	}
}

type agentOutcome struct {
	label string
	text  string
	value any
}

func workflowPrompt(prompt, phase, label string, schema map[string]any) string {
	var parts []string
	if phase != "" {
		parts = append(parts, "Workflow phase: "+phase)
	}
	if label != "" {
		parts = append(parts, "Task label: "+label)
	}
	parts = append(parts, prompt)
	if contract := schemaContractPrompt(schema); contract != "" {
		parts = append(parts, contract)
	}
	return strings.Join(parts, "\n\n")
}

func estimateTokens(value string) int {
	if value == "" {
		return 0
	}
	return (len(value) + 3) / 4
}

func workflowSpendTokens(text string, activity workflowAgentActivity) int {
	if activity.UsageTokens > 0 {
		return activity.UsageTokens
	}
	return estimateTokens(text)
}
