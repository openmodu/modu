// runtime_demo_live is the real-provider version of examples/runtime_demo.
// It talks to any OpenAI-compatible endpoint (OpenAI, LM Studio, Ollama,
// DeepSeek, ...) configured via environment variables — no StreamFn stub.
//
// The only thing we fake is the *crash*: a real provider won't fail on demand,
// so the lookup tool cancels the run's context right after it executes, which
// simulates the process being killed mid-run (after the tool committed, before
// the model's final answer). Everything else — the model calls, the recovery,
// the rewound branch — is the real provider.
//
// Setup:
//
//	export MODU_BASE_URL=https://api.openai.com/v1   # or http://localhost:1234/v1 for LM Studio
//	export MODU_API_KEY=sk-...                        # empty is fine for local endpoints
//	export MODU_MODEL=gpt-4o-mini                     # whatever your endpoint serves
//	export MODU_PROVIDER_ID=openai                    # any label; just must match the Model
//	go run .
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/runtime"
	"github.com/openmodu/modu/pkg/types"
)

const (
	dataDir   = "./runtime_demo_live_data"
	sessionID = "live-session"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	baseURL    = env("MODU_BASE_URL", "https://api.openai.com/v1")
	apiKey     = os.Getenv("MODU_API_KEY")
	modelID    = env("MODU_MODEL", "gpt-4o-mini")
	providerID = env("MODU_PROVIDER_ID", "openai")
)

// ---- a lookup tool that (optionally) simulates a crash after running --------

type LookupTool struct {
	once     sync.Once
	onCommit func() // called the first time the tool runs; used to fake a crash
}

func (t *LookupTool) Name() string        { return "lookup_population" }
func (t *LookupTool) Label() string       { return "Lookup Population" }
func (t *LookupTool) Description() string  { return "Look up the population of a city. Returns a number." }
func (t *LookupTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{"type": "string", "description": "City name"},
		},
		"required": []string{"city"},
	}
}

func (t *LookupTool) Execute(_ context.Context, _ string, args map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	city, _ := args["city"].(string)
	pop := map[string]string{"paris": "2,100,000", "tokyo": "13,960,000", "lagos": "15,400,000"}[normalize(city)]
	if pop == "" {
		pop = "unknown"
	}
	fmt.Printf("   🔧 [TOOL EXECUTED] lookup_population(%q) = %s\n", city, pop)
	if t.onCommit != nil {
		t.once.Do(t.onCommit) // simulate the kill right after the result is produced
	}
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: pop}},
	}, nil
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func newRuntime(tool types.Tool) *runtime.Runtime {
	a := agent.NewAgent(types.Config{
		InitialState: &types.State{
			SystemPrompt: "You are concise. Use the lookup_population tool when asked about a city's population, then state the answer in one sentence.",
			Model:        &types.Model{ID: modelID, ProviderID: providerID},
			Tools:        []types.Tool{tool},
		},
		// No StreamFn → the agent uses the registered real provider.
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
		fmt.Printf("   (no journal: %v)\n", err)
		return
	}
	for _, cp := range hist {
		label := cp.Label
		if label == "" {
			label = "-"
		}
		fmt.Printf("   seq=%d parent=%d status=%-9s msgs=%d label=%s\n", cp.Seq, cp.ParentSeq, cp.Status, len(cp.Messages), label)
	}
}

func main() {
	ctx := context.Background()
	_ = os.RemoveAll(dataDir)

	// Register the real provider. openai.New speaks the OpenAI chat-completions
	// protocol, which most local and hosted endpoints support.
	providers.Register(openai.New(providerID, openai.WithBaseURL(baseURL), openai.WithAPIKey(apiKey)))
	fmt.Printf("provider=%s model=%s base=%s\n\n", providerID, modelID, baseURL)

	// ---- Phase 1: real run that "crashes" right after the tool ----------
	fmt.Println("================ PHASE 1: run, then crash mid-flight ================")
	crashCtx, crash := context.WithCancel(ctx)
	rt1 := newRuntime(&LookupTool{onCommit: func() {
		fmt.Println("   💀 [SIMULATED CRASH] cancelling context right after the tool ran")
		crash()
	}})
	if err := rt1.Run(crashCtx, "What is the population of Paris?"); err != nil {
		fmt.Printf("   run stopped: %v\n", err)
	}
	fmt.Println("   journal on disk after crash:")
	printJournal(rt1)

	// ---- Phase 2: fresh runtime resumes against the real model ----------
	fmt.Println("\n================ PHASE 2: new process resumes (real model finishes) ================")
	rt2 := newRuntime(&LookupTool{}) // no crash hook this time
	resumed, err := rt2.Resume(ctx)
	if err != nil {
		fmt.Printf("   resume error: %v\n", err)
		return
	}
	fmt.Printf("   resumed=%v  (no '[TOOL EXECUTED]' above → tool was NOT re-run)\n", resumed)
	final, _ := rt2.Latest(ctx)
	fmt.Printf("   status=%s\n   answer: %s\n", final.Status, lastText(final))

	// ---- Phase 3: rewind to before the answer, regenerate a branch ------
	fmt.Println("\n================ PHASE 3: rewind to pre-answer, branch with the real model ================")
	rewindTo := lastToolResultSeq(rt2)
	rt3 := newRuntime(&LookupTool{})
	head, err := rt3.Rewind(ctx, rewindTo)
	if err != nil {
		fmt.Printf("   rewind error: %v\n", err)
		return
	}
	fmt.Printf("   forked new head seq=%d from seq=%d\n", head.Seq, head.ParentSeq)
	if _, err := rt3.Resume(ctx); err != nil {
		fmt.Printf("   resume error: %v\n", err)
		return
	}
	branched, _ := rt3.Latest(ctx)
	fmt.Printf("   branch answer: %s\n", lastText(branched))

	fmt.Println("\n   full lineage (original run + rewound branch, nothing lost):")
	printJournal(rt3)
	fmt.Printf("\n   👉 raw journal: cat %s/%s.jsonl\n", dataDir, sessionID)
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
	return "(no final answer yet)"
}

func lastToolResultSeq(rt *runtime.Runtime) int64 {
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
