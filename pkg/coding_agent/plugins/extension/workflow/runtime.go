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

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
	lua "github.com/yuin/gopher-lua"
)

const structuredOutputMaxRetries = 1

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
	usedAgent  atomic.Bool // set from parallel agent goroutines; read on the main thread
	mu         sync.Mutex
	stageDepth atomic.Int32
}

type workflowRunState struct {
	mu         sync.Mutex
	agentCount int
	spent      int
	reserved   int
	cache      *workflowAgentCache
	cursor     map[string]int
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

func (r *runner) run(ctx context.Context, script string) (runResult, error) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	L.SetContext(ctx)
	r.openSafeLibs(L)
	if err := r.installGlobals(L); err != nil {
		return runResult{Snapshot: r.tracker.current()}, err
	}
	if err := L.DoString(script); err != nil {
		if ctx.Err() != nil {
			r.tracker.skipRunning("aborted")
			return runResult{Snapshot: r.tracker.current()}, ctx.Err()
		}
		return runResult{Snapshot: r.tracker.current()}, err
	}
	if r.meta == nil {
		return runResult{Snapshot: r.tracker.current()}, fmt.Errorf("meta({name=..., description=...}) is required")
	}
	if !r.usedAgent.Load() {
		return runResult{Meta: *r.meta, Snapshot: r.tracker.current()}, fmt.Errorf("workflow scripts must call agent() or parallel() at least once")
	}
	value := lua.LNil
	if top := L.GetTop(); top > 0 {
		value = L.Get(top)
	}
	result, err := luaToGo(value)
	if err != nil {
		return runResult{Meta: *r.meta, Snapshot: r.tracker.current()}, fmt.Errorf("workflow result: %w", err)
	}
	snapshot := r.tracker.complete(result)
	return runResult{Meta: *r.meta, Result: result, Snapshot: snapshot}, nil
}

func (r *runner) openSafeLibs(L *lua.LState) {
	lua.OpenBase(L)
	lua.OpenTable(L)
	lua.OpenString(L)
	lua.OpenMath(L)
	for _, name := range []string{
		"dofile", "loadfile", "load", "require", "collectgarbage", "print",
	} {
		L.SetGlobal(name, lua.LNil)
	}
	if mathValue := L.GetGlobal("math"); mathValue.Type() == lua.LTTable {
		mathTable := mathValue.(*lua.LTable)
		mathTable.RawSetString("random", lua.LNil)
		mathTable.RawSetString("randomseed", lua.LNil)
	}
}

func (r *runner) installGlobals(L *lua.LState) error {
	L.SetGlobal("meta", L.NewFunction(r.luaMeta))
	L.SetGlobal("phase", L.NewFunction(r.luaPhase))
	L.SetGlobal("log", L.NewFunction(r.luaLog))
	L.SetGlobal("agent", L.NewFunction(r.luaAgent))
	L.SetGlobal("workflow", L.NewFunction(r.luaWorkflow))
	L.SetGlobal("parallel", L.NewFunction(r.luaParallel))
	L.SetGlobal("pipeline", L.NewFunction(r.luaPipeline))
	if r.opts.Args == nil {
		L.SetGlobal("args", lua.LNil)
	} else {
		L.SetGlobal("args", goToLua(L, r.opts.Args))
	}
	L.SetGlobal("cwd", lua.LString(r.opts.Cwd))

	process := L.NewTable()
	process.RawSetString("cwd", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(r.opts.Cwd))
		return 1
	}))
	L.SetGlobal("process", process)

	jsonTable := L.NewTable()
	jsonTable.RawSetString("encode", L.NewFunction(r.luaJSONEncode))
	jsonTable.RawSetString("decode", L.NewFunction(r.luaJSONDecode))
	jsonTable.RawSetString("null", jsonNullValue(L))
	L.SetGlobal("json", jsonTable)

	budget := L.NewTable()
	if r.opts.BudgetTotal > 0 {
		budget.RawSetString("total", lua.LNumber(r.opts.BudgetTotal))
	} else {
		budget.RawSetString("total", lua.LNil)
	}
	budget.RawSetString("spent", L.NewFunction(func(L *lua.LState) int {
		r.state.mu.Lock()
		spent := r.state.spent
		r.state.mu.Unlock()
		L.Push(lua.LNumber(spent))
		return 1
	}))
	budget.RawSetString("remaining", L.NewFunction(func(L *lua.LState) int {
		if r.opts.BudgetTotal <= 0 {
			L.Push(lua.LNil)
			return 1
		}
		r.state.mu.Lock()
		remaining := r.opts.BudgetTotal - r.state.spent
		r.state.mu.Unlock()
		if remaining < 0 {
			remaining = 0
		}
		L.Push(lua.LNumber(remaining))
		return 1
	}))
	L.SetGlobal("budget", budget)
	return nil
}

func (r *runner) requireMeta() error {
	if r.meta == nil {
		return fmt.Errorf("meta() must be called before phase/log/agent/parallel/pipeline")
	}
	return nil
}

func (r *runner) luaMeta(L *lua.LState) int {
	if r.meta != nil {
		L.RaiseError("meta() may only be called once")
		return 0
	}
	table := L.CheckTable(1)
	meta, err := decodeMeta(table)
	if err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	r.meta = &meta
	r.tracker.setMeta(meta)
	return 0
}

func (r *runner) luaPhase(L *lua.LState) int {
	if err := r.requireMeta(); err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	title := strings.TrimSpace(L.CheckString(1))
	if title == "" {
		L.RaiseError("phase title must be a non-empty string")
		return 0
	}
	r.mu.Lock()
	r.current = title
	r.mu.Unlock()
	r.tracker.addPhase(title)
	return 0
}

func (r *runner) luaLog(L *lua.LState) int {
	if err := r.requireMeta(); err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	r.tracker.addLog(L.CheckString(1))
	return 0
}

func (r *runner) luaAgent(L *lua.LState) int {
	if err := r.requireMeta(); err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	prompt := L.CheckString(1)
	opts := agentOptions{}
	if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
		table, ok := L.Get(2).(*lua.LTable)
		if !ok {
			L.RaiseError("agent options must be a table")
			return 0
		}
		var err error
		opts, err = decodeAgentOptions(table)
		if err != nil {
			L.RaiseError("%s", err.Error())
			return 0
		}
	}
	result, ok, err := r.runAgent(L.Context(), prompt, opts)
	if err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	L.Push(goToLua(L, result.value))
	return 1
}

func (r *runner) luaWorkflow(L *lua.LState) int {
	if err := r.requireMeta(); err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	if r.opts.NestedDepth >= 1 {
		L.RaiseError("nested workflow() calls are limited to one level")
		return 0
	}
	ref := strings.TrimSpace(L.CheckString(1))
	if ref == "" {
		L.RaiseError("workflow name or path is required")
		return 0
	}
	var childArgs any
	if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
		value, err := luaToGo(L.Get(2))
		if err != nil {
			L.RaiseError("workflow args must be JSON-compatible: %s", err.Error())
			return 0
		}
		childArgs = value
	}
	script, sourcePath, err := loadNestedWorkflowScript(ref, r.opts.Cwd, r.opts.AgentDir)
	if err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
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
	result, err := child.run(L.Context(), script)
	if err != nil {
		L.RaiseError("workflow %q: %s", ref, err.Error())
		return 0
	}
	r.usedAgent.Store(true)
	r.tracker.addLog(fmt.Sprintf("nested workflow %s completed with %d agent(s)", ref, result.Snapshot.AgentCount))
	L.Push(goToLua(L, result.Result))
	return 1
}

func (r *runner) luaParallel(L *lua.LState) int {
	if err := r.requireMeta(); err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	taskTable := L.CheckTable(1)
	tasks, err := decodeParallelTasks(taskTable)
	if err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	limit := r.opts.Concurrency
	if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
		options, ok := L.Get(2).(*lua.LTable)
		if !ok {
			L.RaiseError("parallel options must be a table")
			return 0
		}
		if n := intField(options, "concurrency"); n > 0 {
			limit = n
		}
	}
	results := r.runParallel(L.Context(), tasks, limit)
	out := L.NewTable()
	for i, result := range results {
		if result.ok {
			out.RawSetInt(i+1, goToLua(L, result.value))
		} else {
			out.RawSetInt(i+1, jsonNullValue(L))
		}
	}
	L.Push(out)
	return 1
}

// luaPipeline runs each item through the full stage chain. The stages are Lua
// closures and gopher-lua's LState is single-threaded, so items run SEQUENTIALLY
// on the one VM — there is no way to execute Lua closures concurrently on a
// shared state. For concurrent agent fan-out use parallel() (its tasks are data,
// not closures, so they fork in parallel), including parallel() *inside* a stage.
// The concurrency option is accepted for compatibility but does not apply here.
func (r *runner) luaPipeline(L *lua.LState) int {
	if r.stageDepth.Load() > 0 {
		L.RaiseError("nested pipeline() calls are not supported")
		return 0
	}
	if err := r.requireMeta(); err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	items := L.CheckTable(1)
	stages := L.CheckTable(2)
	if items.Len() == 0 {
		L.Push(L.NewTable())
		return 1
	}
	if stages.Len() == 0 {
		L.RaiseError("pipeline() requires at least one stage")
		return 0
	}
	if L.GetTop() >= 3 && L.Get(3) != lua.LNil {
		if _, ok := L.Get(3).(*lua.LTable); !ok {
			L.RaiseError("pipeline options must be a table")
			return 0
		}
	}

	stageValues := make([]lua.LValue, stages.Len())
	for i := range stageValues {
		stage := stages.RawGetInt(i + 1)
		if stage.Type() != lua.LTFunction {
			L.RaiseError("pipeline stage %d must be a function", i+1)
			return 0
		}
		stageValues[i] = stage
	}

	out := L.NewTable()
	for i := 1; i <= items.Len(); i++ {
		if err := L.Context().Err(); err != nil {
			L.RaiseError("%s", err.Error())
			return 0
		}
		value, ok := r.runPipelineItem(L, i, items.RawGetInt(i), stageValues)
		if ok {
			out.RawSetInt(i, goToLua(L, value))
		} else {
			out.RawSetInt(i, jsonNullValue(L))
		}
	}
	L.Push(out)
	return 1
}

func (r *runner) runPipelineItem(L *lua.LState, index int, original lua.LValue, stages []lua.LValue) (any, bool) {
	value := original
	for _, stage := range stages {
		r.stageDepth.Add(1)
		err := L.CallByParam(lua.P{Fn: stage, NRet: 1, Protect: true}, value, original, lua.LNumber(index))
		if err == nil {
			value = L.Get(-1)
			L.Pop(1)
		}
		r.stageDepth.Add(-1)
		if err != nil {
			r.tracker.addLog(fmt.Sprintf("pipeline[%d] failed: %v", index, err))
			return nil, false
		}
	}
	out, err := luaToGo(value)
	if err != nil {
		r.tracker.addLog(fmt.Sprintf("pipeline[%d] failed: %v", index, err))
		return nil, false
	}
	return out, true
}

func (r *runner) luaJSONEncode(L *lua.LState) int {
	value, err := luaToGo(L.Get(1))
	if err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	data, err := json.Marshal(value)
	if err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	L.Push(lua.LString(data))
	return 1
}

func (r *runner) luaJSONDecode(L *lua.LState) int {
	text := L.CheckString(1)
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	L.Push(goToLua(L, value))
	return 1
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
	r.tracker.addLog(fmt.Sprintf("agent %s structured output failed after %d retry: %v", result.label, structuredOutputMaxRetries, retryErr))
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

type parallelOutcome struct {
	agentOutcome
	ok bool
}

func (r *runner) runParallel(ctx context.Context, tasks []parallelTask, limit int) []parallelOutcome {
	if limit <= 0 {
		limit = r.opts.Concurrency
	}
	if limit <= 0 {
		limit = 4
	}
	if limit > 16 {
		limit = 16
	}
	results := make([]parallelOutcome, len(tasks))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, task parallelTask) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			result, ok, err := r.runAgent(ctx, task.Prompt, task.agentOptions)
			if err != nil {
				return
			}
			results[idx] = parallelOutcome{agentOutcome: result, ok: ok}
		}(i, task)
	}
	wg.Wait()
	return results
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
