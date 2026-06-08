// runtime_demo shows pkg/runtime's three properties — resumable, rewindable,
// re-entrant — with no real LLM required. The model is a scripted stub so the
// whole thing runs offline via `go run .`.
//
// Story:
//  1. A run calls a tool (adds 2+3), then the "provider" CRASHES mid-run.
//  2. A fresh process Resumes from the on-disk journal and finishes — and the
//     tool is NOT executed a second time (re-entrancy).
//  3. We Rewind to the moment right before the model's final answer and take a
//     different branch, without losing history.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/runtime"
	"github.com/openmodu/modu/pkg/types"
)

const (
	dataDir   = "./runtime_demo_data"
	sessionID = "demo-session"
)

// ---- a tool that announces every time it runs, so re-execution is visible ----

type AddTool struct{}

func (t *AddTool) Name() string        { return "add" }
func (t *AddTool) Label() string       { return "Add" }
func (t *AddTool) Description() string  { return "Add two numbers" }
func (t *AddTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "number"},
			"b": map[string]any{"type": "number"},
		},
		"required": []string{"a", "b"},
	}
}

func (t *AddTool) Execute(_ context.Context, _ string, args map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	a, b := toFloat(args["a"]), toFloat(args["b"])
	fmt.Printf("   🔧 [TOOL EXECUTED] add(%g, %g) = %g\n", a, b, a+b)
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: fmt.Sprintf("%g", a+b)}},
	}, nil
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

// ---- scripted "LLM": each Complete pops the next turn ---------------------

type turn struct {
	msg *types.AssistantMessage
	err error
}

type script struct {
	turns []turn
	idx   int
}

func (s *script) streamFn() types.StreamFn {
	return func(_ context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		if s.idx >= len(s.turns) {
			return nil, errors.New("script exhausted")
		}
		t := s.turns[s.idx]
		s.idx++
		if t.err != nil {
			return nil, t.err
		}
		stream := types.NewEventStream()
		go func() {
			stream.Push(types.StreamEvent{Type: types.EventDone, Reason: t.msg.StopReason, Message: t.msg})
			stream.Resolve(t.msg, nil)
			stream.Close()
		}()
		return stream, nil
	}
}

func toolCall(id string, a, b int) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:       types.RoleAssistant,
		StopReason: "toolUse",
		Content:    []types.ContentBlock{&types.ToolCallContent{Type: "toolCall", ID: id, Name: "add", Arguments: map[string]any{"a": a, "b": b}}},
		Timestamp:  time.Now().UnixMilli(),
	}
}

func text(s string) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:       types.RoleAssistant,
		StopReason: "stop",
		Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: s}},
		Timestamp:  time.Now().UnixMilli(),
	}
}

// newRuntime builds a fresh agent (simulating a new process) wired to the same
// on-disk journal and session id.
func newRuntime(sc *script) *runtime.Runtime {
	a := agent.NewAgent(types.Config{
		InitialState: &types.State{
			SystemPrompt: "You add numbers.",
			Model:        &types.Model{ID: "scripted", ProviderID: "stub"},
			Tools:        []types.Tool{&AddTool{}},
		},
		StreamFn: sc.streamFn(),
	})
	store, err := runtime.NewFileStore(dataDir)
	if err != nil {
		panic(err)
	}
	return runtime.New(a, store, sessionID)
}

func printJournal(rt *runtime.Runtime) {
	hist, err := rt.History(context.Background())
	if err != nil {
		fmt.Printf("   (no journal yet: %v)\n", err)
		return
	}
	for _, cp := range hist {
		label := cp.Label
		if label == "" {
			label = "-"
		}
		fmt.Printf("   seq=%d parent=%d status=%-9s msgs=%d label=%s\n",
			cp.Seq, cp.ParentSeq, cp.Status, len(cp.Messages), label)
	}
}

func main() {
	ctx := context.Background()
	_ = os.RemoveAll(dataDir) // fresh start each run so the demo is deterministic

	// One shared script spans all phases; idx persists across "processes".
	sc := &script{turns: []turn{
		{msg: toolCall("call-1", 2, 3)},                      // turn 0: ask to add
		{err: errors.New("simulated provider crash kill-9")}, // turn 1: CRASH
		{msg: text("The sum is 5.")},                         // turn 2: served on resume
		{msg: text("Confirmed again: 5 (rewound branch).")},  // turn 3: served on the rewind branch
	}}

	// ---- Phase 1: run, and crash mid-flight ------------------------------
	fmt.Println("================ PHASE 1: run until it crashes ================")
	rt1 := newRuntime(sc)
	if err := rt1.Run(ctx, "What is 2 + 3?"); err != nil {
		fmt.Printf("   💥 run failed (as designed): %v\n", err)
	}
	fmt.Println("   journal on disk after crash:")
	printJournal(rt1)

	// ---- Phase 2: a brand-new runtime resumes from disk ------------------
	fmt.Println("\n================ PHASE 2: new process resumes ================")
	rt2 := newRuntime(sc) // fresh agent + store, same session id
	resumed, err := rt2.Resume(ctx)
	if err != nil {
		fmt.Printf("   resume error: %v\n", err)
	}
	fmt.Printf("   resumed=%v  → notice the tool did NOT run again above\n", resumed)
	final, _ := rt2.Latest(ctx)
	fmt.Printf("   final status=%s, answer=%q\n", final.Status, lastText(final))
	fmt.Println("   journal now:")
	printJournal(rt2)

	// ---- Phase 3: rewind to before the final answer, take a new branch ---
	fmt.Println("\n================ PHASE 3: rewind + branch ================")
	rewindTo := findToolResultSeq(rt2) // the checkpoint right after the tool ran
	fmt.Printf("   rewinding to seq=%d (state: question asked, tool done, no answer yet)\n", rewindTo)
	rt3 := newRuntime(sc)
	head, err := rt3.Rewind(ctx, rewindTo)
	if err != nil {
		fmt.Printf("   rewind error: %v\n", err)
		return
	}
	fmt.Printf("   forked new head seq=%d (parent=%d)\n", head.Seq, head.ParentSeq)
	resumed, _ = rt3.Resume(ctx) // continue along the new branch
	fmt.Printf("   resumed=%v\n", resumed)
	branched, _ := rt3.Latest(ctx)
	fmt.Printf("   branch answer=%q  (history preserved, tool still not re-run)\n", lastText(branched))

	fmt.Println("\n   full lineage:")
	printJournal(rt3)
	fmt.Printf("\n   👉 inspect the raw journal: cat %s/%s.jsonl\n", dataDir, sessionID)
}

func lastText(cp runtime.Checkpoint) string {
	msgs, err := cp.Restore()
	if err != nil || len(msgs) == 0 {
		return ""
	}
	if a, ok := msgs[len(msgs)-1].(types.AssistantMessage); ok {
		for _, b := range a.Content {
			if t, ok := b.(*types.TextContent); ok {
				return t.Text
			}
		}
	}
	return "(last message is not a final answer)"
}

// findToolResultSeq returns the seq of the last checkpoint whose final message
// is a tool result — i.e. the point just before the model's answer.
func findToolResultSeq(rt *runtime.Runtime) int64 {
	hist, _ := rt.History(context.Background())
	var seq int64
	for _, cp := range hist {
		msgs, err := cp.Restore()
		if err != nil || len(msgs) == 0 {
			continue
		}
		if _, ok := msgs[len(msgs)-1].(types.ToolResultMessage); ok {
			seq = cp.Seq
		}
	}
	return seq
}
