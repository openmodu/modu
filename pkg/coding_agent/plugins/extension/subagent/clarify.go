package subagent

import (
	"fmt"
	"strings"
)

// clarifyRequested reports whether the caller asked for the clarify
// preview/confirm gate. Missing or non-boolean values default to false so
// pre-existing callers keep their non-interactive dispatch.
func clarifyRequested(args map[string]any) bool {
	v, _ := args["clarify"].(bool)
	return v
}

// runClarifyGate builds a human-readable summary of the dispatch the
// caller is about to make and sends it through api.Confirm. Returns
// (proceed, abortText).
//
// Without a TUI editor (pi-subagents' clarify shows an in-line preview
// that the user can edit before running), the gate here is preview +
// yes/no — the user either proceeds or aborts. The abort path returns
// the preview text in the tool result so the orchestrator's LLM can see
// what would have run.
func runClarifyGate(ext *Extension, mode string, args map[string]any) (bool, string) {
	if ext == nil || ext.api == nil {
		return true, ""
	}
	preview := buildClarifyPreview(mode, args)
	if ok := ext.api.Confirm("Subagent dispatch", preview, false); ok {
		return true, ""
	}
	return false, "Dispatch aborted via clarify gate.\n\n" + preview
}

func buildClarifyPreview(mode string, args map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "About to dispatch a subagent in `%s` mode.", mode)
	if ctxMode, _ := args["context"].(string); ctxMode != "" {
		fmt.Fprintf(&b, " context=%s.", ctxMode)
	}
	if async, ok := args["async"].(bool); ok && async {
		b.WriteString(" async=true.")
	}
	switch mode {
	case "single":
		writeSingleClarify(&b, args)
	case "parallel":
		writeParallelClarify(&b, args)
	case "chain":
		writeChainClarify(&b, args)
	}
	if includeProgress, _ := args["includeProgress"].(bool); includeProgress {
		b.WriteString("\n[includeProgress] result will append progress.md")
	}
	if artifacts, _ := args["artifacts"].(bool); artifacts {
		b.WriteString("\n[artifacts] per-run input/output/metadata will be written")
	}
	if worktree, _ := args["worktree"].(bool); worktree {
		b.WriteString("\n[worktree] every parallel child runs in its own git worktree")
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeSingleClarify(b *strings.Builder, args map[string]any) {
	agent, _ := args["agent"].(string)
	task, _ := args["task"].(string)
	fmt.Fprintf(b, "\n- single: agent=%s task=%s", agent, summariseTask(task))
}

func writeParallelClarify(b *strings.Builder, args map[string]any) {
	raw := args["parallel"]
	kind := "parallel"
	if raw == nil {
		raw = args["tasks"]
		kind = "tasks"
	}
	items, _ := raw.([]any)
	if len(items) == 0 {
		fmt.Fprintf(b, "\n- %s: (empty)", kind)
		return
	}
	fmt.Fprintf(b, "\n- %s (%d items):", kind, len(items))
	for i, item := range items {
		obj, _ := item.(map[string]any)
		agent, _ := obj["agent"].(string)
		task, _ := obj["task"].(string)
		count, _ := obj["count"].(int)
		if count == 0 {
			if f, ok := obj["count"].(float64); ok {
				count = int(f)
			}
		}
		if count <= 1 {
			fmt.Fprintf(b, "\n  [%d] %s: %s", i, agent, summariseTask(task))
		} else {
			fmt.Fprintf(b, "\n  [%d] %s × %d: %s", i, agent, count, summariseTask(task))
		}
	}
}

func writeChainClarify(b *strings.Builder, args map[string]any) {
	steps, _ := args["chain"].([]any)
	if len(steps) == 0 {
		b.WriteString("\n- chain: (empty)")
		return
	}
	fmt.Fprintf(b, "\n- chain (%d steps):", len(steps))
	for i, step := range steps {
		obj, _ := step.(map[string]any)
		if parallel, ok := obj["parallel"].([]any); ok {
			fmt.Fprintf(b, "\n  [%d] parallel group (%d items)", i, len(parallel))
			for j, item := range parallel {
				inner, _ := item.(map[string]any)
				agent, _ := inner["agent"].(string)
				task, _ := inner["task"].(string)
				fmt.Fprintf(b, "\n      [%d] %s: %s", j, agent, summariseTask(task))
			}
			continue
		}
		agent, _ := obj["agent"].(string)
		task, _ := obj["task"].(string)
		fmt.Fprintf(b, "\n  [%d] %s: %s", i, agent, summariseTask(task))
	}
}

// summariseTask keeps preview lines readable: a single line, cap at 100
// chars with an ellipsis when truncated.
func summariseTask(task string) string {
	task = strings.TrimSpace(task)
	task = strings.ReplaceAll(task, "\n", " ")
	const max = 100
	if len(task) > max {
		return task[:max] + "…"
	}
	if task == "" {
		return "(empty)"
	}
	return task
}
