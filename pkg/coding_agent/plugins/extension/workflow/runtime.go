package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
	lua "github.com/yuin/gopher-lua"
)

type runOptions struct {
	Cwd         string
	Args        any
	Concurrency int
	OnUpdate    types.ToolUpdateCallback
}

type runResult struct {
	Meta     metaInfo
	Result   any
	Snapshot workflowSnapshot
}

type runner struct {
	api        extension.ExtensionAPI
	opts       runOptions
	tracker    *snapshotTracker
	meta       *metaInfo
	current    string
	agentCount int
	spent      int
	usedAgent  bool
	mu         sync.Mutex
}

func newRunner(api extension.ExtensionAPI, opts runOptions) *runner {
	return &runner{
		api:     api,
		opts:    opts,
		tracker: newSnapshotTracker(opts.OnUpdate),
	}
}

func (r *runner) run(ctx context.Context, script string) (runResult, error) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	L.SetContext(ctx)
	r.openSafeLibs(L)
	if err := r.installGlobals(L); err != nil {
		return runResult{}, err
	}
	if err := L.DoString(script); err != nil {
		if ctx.Err() != nil {
			r.tracker.skipRunning("aborted")
			return runResult{}, ctx.Err()
		}
		return runResult{}, err
	}
	if r.meta == nil {
		return runResult{}, fmt.Errorf("meta({name=..., description=...}) is required")
	}
	if !r.usedAgent {
		return runResult{}, fmt.Errorf("workflow scripts must call agent() or parallel() at least once")
	}
	value := lua.LNil
	if top := L.GetTop(); top > 0 {
		value = L.Get(top)
	}
	result, err := luaToGo(value)
	if err != nil {
		return runResult{}, fmt.Errorf("workflow result: %w", err)
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
	budget.RawSetString("total", lua.LNil)
	budget.RawSetString("spent", L.NewFunction(func(L *lua.LState) int {
		r.mu.Lock()
		spent := r.spent
		r.mu.Unlock()
		L.Push(lua.LNumber(spent))
		return 1
	}))
	budget.RawSetString("remaining", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNumber(1 << 60))
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
	r.current = title
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
	text, ok, err := r.runAgent(L.Context(), prompt, opts)
	if err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	L.Push(lua.LString(text))
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
			out.RawSetInt(i+1, lua.LString(result.text))
		} else {
			out.RawSetInt(i+1, jsonNullValue(L))
		}
	}
	L.Push(out)
	return 1
}

func (r *runner) luaPipeline(L *lua.LState) int {
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
	out := L.NewTable()
	for i := 1; i <= items.Len(); i++ {
		original := items.RawGetInt(i)
		value := original
		failed := false
		for j := 1; j <= stages.Len(); j++ {
			stage := stages.RawGetInt(j)
			if stage.Type() != lua.LTFunction {
				L.RaiseError("pipeline stage %d must be a function", j)
				return 0
			}
			err := L.CallByParam(lua.P{Fn: stage, NRet: 1, Protect: true}, value, original, lua.LNumber(i))
			if err != nil {
				r.tracker.addLog(fmt.Sprintf("pipeline[%d] failed: %v", i, err))
				out.RawSetInt(i, jsonNullValue(L))
				failed = true
				break
			}
			value = L.Get(-1)
			L.Pop(1)
		}
		if !failed {
			out.RawSetInt(i, value)
		}
	}
	L.Push(out)
	return 1
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

func (r *runner) runAgent(ctx context.Context, prompt string, opts agentOptions) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	r.mu.Lock()
	r.usedAgent = true
	r.agentCount++
	idx := r.agentCount
	phase := opts.Phase
	if phase == "" {
		phase = r.current
	}
	label := strings.TrimSpace(opts.Label)
	if label == "" {
		if phase != "" {
			label = fmt.Sprintf("%s agent %d", phase, idx)
		} else {
			label = fmt.Sprintf("workflow-agent-%d", idx)
		}
	}
	r.mu.Unlock()

	id := r.tracker.startAgent(label, phase, prompt)
	task := workflowPrompt(prompt, phase, label)
	text, err := r.api.ForkSession(ctx, extension.ForkOptions{
		Name:            label,
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
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			r.tracker.finishAgent(id, statusSkipped, nil, "aborted")
			return "", false, err
		}
		msg := fmt.Sprintf("agent %s failed: %v", label, err)
		r.tracker.finishAgent(id, statusError, nil, err.Error())
		r.tracker.addLog(msg)
		return "", false, nil
	}
	r.mu.Lock()
	r.spent += estimateTokens(text)
	r.mu.Unlock()
	r.tracker.finishAgent(id, statusDone, text, "")
	return text, true, nil
}

type parallelOutcome struct {
	text string
	ok   bool
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
			text, ok, err := r.runAgent(ctx, task.Prompt, task.agentOptions)
			if err != nil {
				return
			}
			results[idx] = parallelOutcome{text: text, ok: ok}
		}(i, task)
	}
	wg.Wait()
	return results
}

func workflowPrompt(prompt, phase, label string) string {
	var parts []string
	if phase != "" {
		parts = append(parts, "Workflow phase: "+phase)
	}
	if label != "" {
		parts = append(parts, "Task label: "+label)
	}
	parts = append(parts, prompt)
	return strings.Join(parts, "\n\n")
}

func estimateTokens(value string) int {
	if value == "" {
		return 0
	}
	return (len(value) + 3) / 4
}
