package agent_test

import (
	"context"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/evals"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

func TestBasicAgentResponseEval(t *testing.T) {
	evals.Run(t, "basic chinese factual answer", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		a := agent.NewAgent(types.Config{
			InitialState: &types.State{
				SystemPrompt: "你是一个简洁、准确的中文助手。必须直接回答用户问题，不要编造额外事实。",
				Model:        e.Model,
			},
			MaxTokens: intPtr(256),
		})

		err := a.Prompt(context.Background(), "请用中文回答：法国的首都是哪里？")
		if err != nil {
			e.Fatalf("prompt agent: %v", err)
		}

		output := evals.LastAssistantText(a.GetState().Messages)
		if output == "" {
			e.Fatal("expected non-empty assistant output")
		}

		evals.LLMRubricT(e, "回答使用中文", output)
		evals.LLMRubricT(e, "回答明确指出法国首都是巴黎", output)
		evals.LLMRubricT(e, "回答没有声称法国首都是其他城市", output)
	})
}

func intPtr(value int) *int {
	return &value
}
