package tools

import (
	backendtask "github.com/openmodu/modu/pkg/coding_agent/tools/backend_task"
	"github.com/openmodu/modu/pkg/coding_agent/tools/bash"
	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	contextremaining "github.com/openmodu/modu/pkg/coding_agent/tools/context_remaining"
	"github.com/openmodu/modu/pkg/coding_agent/tools/edit"
	"github.com/openmodu/modu/pkg/coding_agent/tools/find"
	"github.com/openmodu/modu/pkg/coding_agent/tools/grep"
	"github.com/openmodu/modu/pkg/coding_agent/tools/ls"
	memorytool "github.com/openmodu/modu/pkg/coding_agent/tools/memory"
	"github.com/openmodu/modu/pkg/coding_agent/tools/planning"
	"github.com/openmodu/modu/pkg/coding_agent/tools/read"
	webtools "github.com/openmodu/modu/pkg/coding_agent/tools/web"
	worktreetool "github.com/openmodu/modu/pkg/coding_agent/tools/worktree"
	"github.com/openmodu/modu/pkg/coding_agent/tools/write"
	"github.com/openmodu/modu/pkg/types"
)

type ToolSet string

const (
	ToolSetCoding   ToolSet = "coding"
	ToolSetReadOnly ToolSet = "read-only"
	ToolSetAll      ToolSet = "all"

	FeatureMemory       = "memory"
	FeatureTodo         = "todo"
	FeatureTaskOutput   = "task_output"
	FeaturePlanMode     = "plan_mode"
	FeatureWorktreeMode = "worktree_mode"

	ValueMemoryStore = "memory_store"
	ValueTodoStore   = "todo_store"
	ValueTaskStore   = "task_store"
	ValuePlanMode    = "plan_mode"
	ValueWorktree    = "worktree"
	ValueContext     = "context_remaining"
)

type DefaultProvider struct {
	Set       ToolSet
	readState *common.FileReadState
}

func NewProvider(set ToolSet) DefaultProvider {
	if set == "" {
		set = ToolSetCoding
	}
	return DefaultProvider{Set: set, readState: common.NewFileReadState()}
}

func (p DefaultProvider) Tools(ctx types.ToolContext) []types.Tool {
	readState := p.state()
	out := append([]types.Tool{}, ctx.BaseTools...)
	if ctx.BaseTools == nil {
		out = p.baseTools(ctx.Cwd, readState)
	}
	out = append(out, ctx.ExtraTools...)
	if ctx.FeatureEnabled(FeatureMemory) {
		out = append(out, memorytool.NewMemoryTool(valueAs[memorytool.MemoryStore](ctx, ValueMemoryStore)))
	}
	if ctx.FeatureEnabled(FeatureTodo) {
		out = append(out, planning.NewTodoWriteTool(valueAs[planning.TodoStore](ctx, ValueTodoStore)))
	}
	if ctx.FeatureEnabled(FeatureTaskOutput) {
		out = append(out, backendtask.NewTaskOutputTool(valueAs[backendtask.BackgroundTaskStore](ctx, ValueTaskStore)))
	}
	if ctx.FeatureEnabled(FeaturePlanMode) {
		planMode := valueAs[planning.PlanModeManager](ctx, ValuePlanMode)
		out = append(out, planning.NewEnterPlanModeTool(planMode), planning.NewExitPlanModeTool(planMode))
	}
	if ctx.FeatureEnabled(FeatureWorktreeMode) {
		worktree := valueAs[worktreetool.WorktreeManager](ctx, ValueWorktree)
		out = append(out, worktreetool.NewEnterWorktreeTool(worktree), worktreetool.NewExitWorktreeTool(worktree))
	}
	out = append(out, contextremaining.New(valueAs[contextremaining.Provider](ctx, ValueContext)))
	return out
}

func (p DefaultProvider) Rebind(tool types.Tool, ctx types.ToolContext) (types.Tool, bool) {
	readState := p.state()
	switch tool.Name() {
	case "read":
		return read.NewTrackedTool(ctx.Cwd, readState), true
	case "write":
		return write.NewTrackedTool(ctx.Cwd, readState), true
	case "edit":
		return edit.NewTrackedTool(ctx.Cwd, readState), true
	case "bash":
		return bash.NewTool(ctx.Cwd), true
	case "grep":
		return grep.NewTool(ctx.Cwd), true
	case "find":
		return find.NewTool(ctx.Cwd), true
	case "ls":
		return ls.NewTool(ctx.Cwd), true
	case "web_fetch":
		return webtools.NewFetchTool(), true
	case "web_search":
		return webtools.NewSearchTool(), true
	default:
		return nil, false
	}
}

func (p DefaultProvider) state() *common.FileReadState {
	if p.readState != nil {
		return p.readState
	}
	return common.NewFileReadState()
}

func valueAs[T any](ctx types.ToolContext, name string) T {
	v, _ := ctx.Value(name).(T)
	return v
}

func (p DefaultProvider) baseTools(cwd string, readState *common.FileReadState) []types.Tool {
	if readState == nil {
		readState = p.state()
	}
	switch p.Set {
	case ToolSetReadOnly:
		return []types.Tool{
			read.NewTrackedTool(cwd, readState),
			grep.NewTool(cwd),
			find.NewTool(cwd),
			ls.NewTool(cwd),
		}
	case ToolSetAll:
		return []types.Tool{
			read.NewTrackedTool(cwd, readState),
			write.NewTrackedTool(cwd, readState),
			edit.NewTrackedTool(cwd, readState),
			bash.NewTool(cwd),
			grep.NewTool(cwd),
			find.NewTool(cwd),
			ls.NewTool(cwd),
		}
	default:
		return []types.Tool{
			read.NewTrackedTool(cwd, readState),
			bash.NewTool(cwd),
			edit.NewTrackedTool(cwd, readState),
			write.NewTrackedTool(cwd, readState),
			grep.NewTool(cwd),
			find.NewTool(cwd),
			ls.NewTool(cwd),
		}
	}
}

// CodingTools returns the core coding tools: read, bash, edit, write.
func CodingTools(cwd string) []types.Tool {
	return NewProvider(ToolSetCoding).baseTools(cwd, nil)
}

// ReadOnlyTools returns read-only tools: read, grep, find, ls.
func ReadOnlyTools(cwd string) []types.Tool {
	return NewProvider(ToolSetReadOnly).baseTools(cwd, nil)
}

// AllTools returns all available built-in coding tools.
func AllTools(cwd string) []types.Tool {
	return NewProvider(ToolSetAll).baseTools(cwd, nil)
}

// ResearchTools returns opt-in network research tools. They are not part of
// the default coding set and must be explicitly requested by child agents.
func ResearchTools() []types.Tool {
	return []types.Tool{
		webtools.NewFetchTool(),
		webtools.NewSearchTool(),
	}
}
