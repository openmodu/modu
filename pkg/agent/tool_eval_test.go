package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/evals"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

// recordingTool is a deterministic tool for evals: it returns a fixed result and
// records the arguments it was called with, so an eval can assert the model both
// invoked the tool and passed sensible arguments.
type recordingTool struct {
	name    string
	desc    string
	params  any
	result  string
	isError bool
	calls   []map[string]any
}

func (t *recordingTool) Name() string        { return t.name }
func (t *recordingTool) Label() string       { return t.name }
func (t *recordingTool) Description() string { return t.desc }
func (t *recordingTool) Parameters() any     { return t.params }
func (t *recordingTool) Execute(_ context.Context, _ string, args map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	t.calls = append(t.calls, args)
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: t.result}},
		IsError: t.isError,
	}, nil
}

func objectSchema(required string, prop string, propType string, propDesc string) map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{required},
		"properties": map[string]any{
			prop: map[string]any{"type": propType, "description": propDesc},
		},
	}
}

func newToolAgent(model *types.Model, systemPrompt string, tools ...types.Tool) *agent.Agent {
	return agent.NewAgent(types.Config{
		InitialState: &types.State{
			SystemPrompt: systemPrompt,
			Model:        model,
			Tools:        tools,
		},
		MaxTokens: intPtr(2048), // reasoning models need room to think AND answer
		MaxSteps:  8,
	})
}

// TestToolUseWeatherEval checks the agent calls a tool when it lacks the
// information, passes the right argument, and grounds its answer in the tool
// result instead of fabricating one.
func TestToolUseWeatherEval(t *testing.T) {
	evals.Run(t, "tool use: weather lookup", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		weather := &recordingTool{
			name:   "get_weather",
			desc:   "Get the current weather for a city. Returns temperature and conditions.",
			params: objectSchema("city", "city", "string", "City name, e.g. 北京"),
			result: "北京: 26°C, 晴",
		}
		a := newToolAgent(e.Model,
			"你是一个助手。需要查询天气时必须调用 get_weather 工具，不要凭空编造天气数据。",
			weather)

		if err := a.Prompt(context.Background(), "北京现在天气怎么样？"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		// Deterministic: the tool was actually invoked with a city argument.
		evals.ToolCalledT(e, messages, "get_weather")
		if len(weather.calls) == 0 || weather.calls[0]["city"] == nil {
			e.Fatalf("expected get_weather to be called with a city arg, got %v", weather.calls)
		}

		// The answer must reflect the tool's returned temperature.
		output := evals.LastAssistantText(messages)
		evals.ContainsT(e, "26", output)
		evals.LLMRubricT(e, "回答转述了工具返回的天气（26度、晴），没有编造其他天气数据", output)
	})
}

// TestToolUseCalculatorEval checks the agent routes a computation it is told to
// delegate through the tool and uses the returned value verbatim.
func TestToolUseCalculatorEval(t *testing.T) {
	evals.Run(t, "tool use: calculator", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		calc := &recordingTool{
			name:   "calculate",
			desc:   "Evaluate an arithmetic expression and return the exact result.",
			params: objectSchema("expression", "expression", "string", "Arithmetic expression, e.g. 12*34"),
			result: "7006652",
		}
		a := newToolAgent(e.Model,
			"你是一个助手。任何算术计算都必须调用 calculate 工具得到结果，不要自己心算。",
			calc)

		if err := a.Prompt(context.Background(), "1234 乘以 5678 等于多少？"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		evals.ToolCalledT(e, messages, "calculate")
		output := evals.LastAssistantText(messages)
		evals.ContainsT(e, "7006652", output)
		evals.LLMRubricT(e, "回答给出的乘积是 7006652，与工具返回值一致", output)
	})
}

// TestToolUseMultiStepEval checks the agent chains two tools: it must look up the
// user's city first, then feed that city into the weather tool.
func TestToolUseMultiStepEval(t *testing.T) {
	evals.Run(t, "tool use: chained lookup", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		userCity := &recordingTool{
			name:   "get_user_city",
			desc:   "Look up which city a given user lives in.",
			params: objectSchema("user", "user", "string", "User name"),
			result: "上海",
		}
		weather := &recordingTool{
			name:   "get_weather",
			desc:   "Get the current weather for a city.",
			params: objectSchema("city", "city", "string", "City name"),
			result: "上海: 19°C, 多云",
		}
		a := newToolAgent(e.Model,
			"你是一个助手，必须用工具获取信息、不要编造。回答用户问题前，先用 get_user_city 查出用户所在城市，再用 get_weather 查该城市的天气。",
			userCity, weather)

		if err := a.Prompt(context.Background(), "用户 alice 那边现在天气怎么样？"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		evals.ToolCalledT(e, messages, "get_user_city")
		evals.ToolCalledT(e, messages, "get_weather")

		// The chain worked only if the weather tool received the city the first
		// tool returned.
		if len(weather.calls) == 0 {
			e.Fatalf("get_weather was never called")
		}
		city, _ := weather.calls[0]["city"].(string)
		if !strings.Contains(city, "上海") {
			e.Fatalf("expected get_weather called with the city from get_user_city (上海), got %q", city)
		}

		output := evals.LastAssistantText(messages)
		evals.LLMRubricT(e, "回答转述了上海的天气（19度、多云），体现出是先查到用户在上海、再查的该城市天气", output)
	})
}

// TestToolUseErrorHandlingEval checks the agent reports a tool failure honestly
// instead of fabricating a result when the tool returns an error.
func TestToolUseErrorHandlingEval(t *testing.T) {
	evals.Run(t, "tool use: error handling", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		failing := &recordingTool{
			name:    "get_weather",
			desc:    "Get the current weather for a city.",
			params:  objectSchema("city", "city", "string", "City name"),
			result:  "ERROR: weather service unavailable (503)",
			isError: true,
		}
		a := newToolAgent(e.Model,
			"你是一个助手。查询天气必须调用 get_weather 工具。如果工具返回错误，要如实告诉用户查询失败，绝不能编造天气数据。",
			failing)

		if err := a.Prompt(context.Background(), "北京今天天气怎么样？"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		evals.ToolCalledT(e, messages, "get_weather")
		output := evals.LastAssistantText(messages)
		evals.LLMRubricT(e, "回答说明天气查询失败或服务不可用，且没有编造任何具体的天气数据（例如温度、晴雨）", output)
	})
}
