package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestAgentConversationMemoryEval(t *testing.T) {
	evals.Run(t, "agent remembers prior turn", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		a := agent.NewAgent(types.Config{
			InitialState: &types.State{
				SystemPrompt: "你是一个简洁、准确的中文助手。用户要求你记住的信息，后续对话必须沿用。",
				Model:        e.Model,
			},
			MaxTokens: intPtr(512),
		})

		if err := a.Prompt(context.Background(), "请记住项目代号是「青松」。只回复“已记住”。"); err != nil {
			e.Fatalf("first prompt: %v", err)
		}
		if err := a.Prompt(context.Background(), "刚才的项目代号是什么？只回答代号。"); err != nil {
			e.Fatalf("second prompt: %v", err)
		}

		output := evals.LastAssistantText(a.GetState().Messages)
		evals.ContainsT(e, "青松", output)
		evals.LLMRubricT(e, "回答基于上一轮对话，明确指出项目代号是青松", output)
	})
}

func TestAgentAvoidsUnnecessaryToolEval(t *testing.T) {
	evals.Run(t, "tool use: avoid unnecessary lookup", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		lookup := &recordingTool{
			name:   "lookup_number",
			desc:   "Look up external realtime numeric facts. Do not use for simple arithmetic.",
			params: objectSchema("query", "query", "string", "External lookup query"),
			result: "999",
		}
		a := newToolAgent(e.Model,
			"你是一个助手。只有需要外部实时查询时才调用 lookup_number；普通算术必须直接回答，不要调用工具。",
			lookup)

		if err := a.Prompt(context.Background(), "2+3 等于多少？只回答数字。"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}

		messages := a.GetState().Messages
		evals.AssertT(e, "agent does not call lookup_number for simple arithmetic",
			toolNamesDetail(messages), !evals.ToolCalled(messages, "lookup_number"))
		output := evals.LastAssistantText(messages)
		evals.ContainsT(e, "5", output)
		evals.NotContainsT(e, "999", output)
	})
}

func TestAgentStructuredJSONEval(t *testing.T) {
	evals.Run(t, "agent returns structured JSON", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		a := agent.NewAgent(types.Config{
			InitialState: &types.State{
				SystemPrompt: "你必须只输出一个 JSON 对象，不要 Markdown，不要代码块。字段 answer 是字符串，confidence 是 0 到 1 的数字。",
				Model:        e.Model,
			},
			MaxTokens: intPtr(512),
		})

		if err := a.Prompt(context.Background(), "请按指定 JSON 格式回答：2+2 等于多少？"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}

		output := evals.LastAssistantText(a.GetState().Messages)
		evals.NotContainsT(e, "```", output)

		var obj map[string]any
		err := json.Unmarshal([]byte(output), &obj)
		evals.AssertT(e, "assistant output is valid JSON object", output, err == nil && obj != nil)
		_, hasAnswer := obj["answer"]
		_, hasConfidence := obj["confidence"]
		evals.AssertT(e, "JSON output contains answer and confidence fields", output, hasAnswer && hasConfidence)
		evals.LLMRubricT(e, "JSON 的 answer 字段表达 2+2 的答案是 4", output)
	})
}

func intPtr(value int) *int {
	return &value
}

func toolNamesDetail(messages []types.AgentMessage) string {
	return fmt.Sprintf("tools called: %v", evals.ToolCallNames(messages))
}
