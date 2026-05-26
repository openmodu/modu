package subagent

import (
	"context"
	"fmt"
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
	return forkOne(ctx, ext, agentName, task)
}

// runParallel fans out an array of (agent, task) pairs as concurrent
// ForkSession calls. The aggregated result is one human-readable block
// per call, ordered by request index. One pair's failure does NOT
// cancel the rest — each pair's outcome is reported independently so
// the caller can act on partial success.
func runParallel(ctx context.Context, ext *Extension, args map[string]any) (string, error) {
	calls, err := decodeCallList(args["parallel"], "parallel")
	if err != nil {
		return "", err
	}
	if len(calls) == 0 {
		return "", fmt.Errorf("parallel mode requires a non-empty \"parallel\" array")
	}

	type outcome struct {
		text string
		err  error
	}
	results := make([]outcome, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func(idx int, call callSpec) {
			defer wg.Done()
			text, err := forkOne(ctx, ext, call.agent, call.task)
			results[idx] = outcome{text: text, err: err}
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
	return b.String(), nil
}

// runChain runs (agent, task) pairs in sequence. Each step's task may
// contain the literal "{previous}" token, which is substituted with the
// prior step's reply before dispatch. The first step sees "{previous}" as
// the empty string. The final step's reply is the chain's overall result.
//
// A failure in any step aborts the chain and surfaces that step's error.
func runChain(ctx context.Context, ext *Extension, args map[string]any) (string, error) {
	calls, err := decodeCallList(args["chain"], "chain")
	if err != nil {
		return "", err
	}
	if len(calls) == 0 {
		return "", fmt.Errorf("chain mode requires a non-empty \"chain\" array")
	}

	previous := ""
	for i, c := range calls {
		task := strings.ReplaceAll(c.task, "{previous}", previous)
		text, err := forkOne(ctx, ext, c.agent, task)
		if err != nil {
			return "", fmt.Errorf("chain step %d (%s): %w", i, c.agent, err)
		}
		previous = text
	}
	return previous, nil
}

// callSpec is one (agent, task) entry inside a parallel or chain list.
type callSpec struct {
	agent string
	task  string
}

// decodeCallList validates and unpacks `args["parallel"]` / `args["chain"]`.
// kind is included in error messages so the caller learns which field went
// wrong without inspecting the source.
func decodeCallList(raw any, kind string) ([]callSpec, error) {
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
		agent, _ := obj["agent"].(string)
		task, _ := obj["task"].(string)
		if agent == "" {
			return nil, fmt.Errorf("%s[%d]: missing \"agent\"", kind, i)
		}
		if task == "" {
			return nil, fmt.Errorf("%s[%d]: missing \"task\"", kind, i)
		}
		out = append(out, callSpec{agent: agent, task: task})
	}
	return out, nil
}

// forkOne resolves the named profile and dispatches one ForkSession call.
// Returns "agent not found" if the loader has no matching entry — that's
// always a user error rather than a system failure, so the message is
// short and explicit.
func forkOne(ctx context.Context, ext *Extension, agentName, task string) (string, error) {
	def, ok := ext.loader.Get(agentName)
	if !ok {
		return "", fmt.Errorf("agent %q not found", agentName)
	}
	return ext.api.ForkSession(ctx, forkOptionsFor(def, ext.cfg, task))
}

// forkOptionsFor translates a stored SubagentDefinition into an
// ExtensionAPI ForkOptions request, layering DefaultModel on top when the
// profile leaves Model empty.
//
// Background / Isolation / Skills / MemoryScope come from the profile —
// the model can't override them per call, which matches spawn_subagent's
// semantics (those fields are part of the profile contract, not the
// caller's choice).
func forkOptionsFor(def *csubagent.SubagentDefinition, cfg Config, task string) extension.ForkOptions {
	model := def.Model
	if model == "" {
		model = cfg.DefaultModel
	}
	return extension.ForkOptions{
		SystemPrompt:    def.SystemPrompt,
		Task:            task,
		AllowedTools:    def.Tools,
		DisallowedTools: def.DisallowedTools,
		Model:           model,
		ThinkingLevel:   string(def.ThinkingLevel),
		PermissionMode:  def.PermissionMode,
		MaxTurns:        def.MaxTurns,
		Background:      def.Background,
		Isolation:       def.Isolation,
		Skills:          def.Skills,
		MemoryScope:     def.MemoryScope,
	}
}
