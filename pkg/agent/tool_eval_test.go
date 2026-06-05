package agent_test

import (
	"context"
	"fmt"
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

// TestToolUseSelectionEval checks the agent picks the RIGHT tool among several
// plausible ones: a weather question must route to get_weather, and the unrelated
// stock tool must not be touched.
func TestToolUseSelectionEval(t *testing.T) {
	evals.Run(t, "tool use: select correct tool", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		weather := &recordingTool{
			name:   "get_weather",
			desc:   "Get the current weather (temperature and conditions) for a city.",
			params: objectSchema("city", "city", "string", "City name"),
			result: "广州: 30°C, 雷阵雨",
		}
		stock := &recordingTool{
			name:   "get_stock_price",
			desc:   "Get the latest stock price for a ticker symbol.",
			params: objectSchema("ticker", "ticker", "string", "Stock ticker symbol"),
			result: "AAPL: 412.50",
		}
		a := newToolAgent(e.Model,
			"你是一个助手。你有两个工具：get_weather 查天气、get_stock_price 查股价。"+
				"根据用户问题选择正确的工具，不要调用不相关的工具，也不要编造数据。",
			weather, stock)

		if err := a.Prompt(context.Background(), "广州现在天气怎么样？"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		evals.ToolCalledT(e, messages, "get_weather")
		evals.AssertT(e, "agent does not call the unrelated get_stock_price tool",
			toolNamesDetail(messages), !evals.ToolCalled(messages, "get_stock_price"))
		output := evals.LastAssistantText(messages)
		evals.ContainsT(e, "30", output)
		evals.NotContainsT(e, "412", output)
	})
}

// TestToolUseParallelEntitiesEval checks the agent issues the tool once per
// entity when asked about several, rather than answering only one or fabricating
// the rest. The tool returns a city-specific result, so the final answer can
// only be right if both cities were actually looked up.
func TestToolUseParallelEntitiesEval(t *testing.T) {
	evals.Run(t, "tool use: one call per entity", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		weather := &cityWeatherTool{
			recordingTool: recordingTool{
				name:   "get_weather",
				desc:   "Get the current weather for a single city. Call it once per city.",
				params: objectSchema("city", "city", "string", "City name"),
			},
			byCity: map[string]string{"北京": "北京: 24°C, 晴", "上海": "上海: 21°C, 阴"},
		}
		a := newToolAgent(e.Model,
			"你是一个助手。查询天气必须调用 get_weather 工具，每个城市单独调用一次，不要编造天气数据。",
			weather)

		if err := a.Prompt(context.Background(), "请分别告诉我北京和上海现在的天气。"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		evals.ToolCalledT(e, messages, "get_weather")
		// The tool must have been asked about both cities (one call each).
		cities := map[string]bool{}
		for _, c := range weather.calls {
			if s, _ := c["city"].(string); strings.Contains(s, "北京") {
				cities["北京"] = true
			} else if strings.Contains(s, "上海") {
				cities["上海"] = true
			}
		}
		evals.AssertT(e, "get_weather is called for both 北京 and 上海",
			fmt.Sprintf("calls: %v", weather.calls), cities["北京"] && cities["上海"])

		output := evals.LastAssistantText(messages)
		evals.ContainsT(e, "24", output) // 北京's temperature from the tool
		evals.ContainsT(e, "21", output) // 上海's temperature from the tool
		evals.LLMRubricT(e,
			"回答分别给出了北京（24度、晴）和上海（21度、阴）两个城市的天气，两座城市都提到了，没有把它们混为一谈", output)
	})
}

// cityWeatherTool is a recordingTool whose Execute returns a per-city result.
type cityWeatherTool struct {
	recordingTool
	byCity map[string]string
}

func (t *cityWeatherTool) Execute(_ context.Context, _ string, args map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	t.calls = append(t.calls, args)
	city, _ := args["city"].(string)
	text := "未知城市: 无数据"
	for name, result := range t.byCity {
		if strings.Contains(city, name) {
			text = result
			break
		}
	}
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}, nil
}

// TestToolUseEnumParamEval checks the agent fills a constrained (enum) argument
// correctly: asked for Fahrenheit, it must pass unit="fahrenheit", not the
// default. Argument correctness is asserted on the recorded call, not prose.
func TestToolUseEnumParamEval(t *testing.T) {
	evals.Run(t, "tool use: enum argument", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		weather := &recordingTool{
			name: "get_weather",
			desc: "Get the current weather for a city in the requested unit.",
			params: map[string]any{
				"type":     "object",
				"required": []any{"city", "unit"},
				"properties": map[string]any{
					"city": map[string]any{"type": "string", "description": "City name"},
					"unit": map[string]any{
						"type":        "string",
						"enum":        []any{"celsius", "fahrenheit"},
						"description": "Temperature unit",
					},
				},
			},
			result: "New York: 77°F, sunny",
		}
		a := newToolAgent(e.Model,
			"你是一个助手。查询天气必须调用 get_weather，并根据用户要求的温度单位设置 unit 参数（celsius 或 fahrenheit）。",
			weather)

		if err := a.Prompt(context.Background(), "纽约现在多少度？请用华氏度（Fahrenheit）回答。"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		evals.ToolCalledT(e, messages, "get_weather")
		if len(weather.calls) == 0 {
			e.Fatalf("get_weather was never called")
		}
		unit, _ := weather.calls[0]["unit"].(string)
		evals.AssertT(e, "get_weather called with unit=\"fahrenheit\"",
			fmt.Sprintf("calls: %v", weather.calls), unit == "fahrenheit")
	})
}

// TestToolUseNumericArgEval checks the agent passes numeric arguments with the
// correct JSON type (numbers, not stringified) when the schema declares integer
// params. The judge inspects the recorded call's decoded Go types, so it is exact
// and immune to how the model phrases its prose answer.
func TestToolUseNumericArgEval(t *testing.T) {
	evals.Run(t, "tool use: typed numeric arguments", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		add := &recordingTool{
			name: "add",
			desc: "Add two integers and return their sum.",
			params: map[string]any{
				"type":     "object",
				"required": []any{"a", "b"},
				"properties": map[string]any{
					"a": map[string]any{"type": "integer", "description": "First addend"},
					"b": map[string]any{"type": "integer", "description": "Second addend"},
				},
			},
			result: "579",
		}
		a := newToolAgent(e.Model,
			"你是一个助手。任何加法都必须调用 add 工具，参数 a、b 是整数。", add)

		if err := a.Prompt(context.Background(), "123 加 456 等于多少？"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		evals.ToolCalledT(e, messages, "add")
		if len(add.calls) == 0 {
			e.Fatalf("add was never called")
		}
		// JSON numbers decode to float64; a stringified "123" would decode to string.
		args := add.calls[0]
		aNum, aOK := args["a"].(float64)
		bNum, bOK := args["b"].(float64)
		evals.AssertT(e, "add received a and b as JSON numbers (not strings)",
			fmt.Sprintf("calls: %v", add.calls), aOK && bOK)
		evals.AssertT(e, "add received a=123, b=456",
			fmt.Sprintf("calls: %v", add.calls), aNum == 123 && bNum == 456)
	})
}

// TestToolUseAnswerFromContextEval checks the agent does NOT call a tool when the
// needed data is already supplied in the prompt: it should compute from context
// rather than reach for the lookup tool.
func TestToolUseAnswerFromContextEval(t *testing.T) {
	evals.Run(t, "tool use: answer from given context", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		lookup := &recordingTool{
			name:   "get_temperature",
			desc:   "Look up the current temperature for a city.",
			params: objectSchema("city", "city", "string", "City name"),
			result: "0",
		}
		a := newToolAgent(e.Model,
			"你是一个助手。只有当你缺少所需信息时才调用 get_temperature。如果用户已经在问题里给出了数据，直接用它计算，不要调用工具。",
			lookup)

		if err := a.Prompt(context.Background(),
			"已知北京现在是 28 摄氏度，上海比北京低 5 度。上海现在多少摄氏度？只回答数字。"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		evals.AssertT(e, "agent does not call get_temperature when data is in the prompt",
			toolNamesDetail(messages), !evals.ToolCalled(messages, "get_temperature"))
		output := evals.LastAssistantText(messages)
		evals.ContainsT(e, "23", output)
	})
}

// TestToolUseAskWhenMissingArgsEval checks the agent asks the user for missing
// required information instead of calling the tool with a fabricated argument.
// The request is deliberately underspecified (no destination, no date); a good
// agent clarifies first. The deterministic gate — the tool was NOT called — is
// exact and immune to output phrasing.
func TestToolUseAskWhenMissingArgsEval(t *testing.T) {
	evals.Run(t, "tool use: ask for missing args, don't fabricate", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		book := &recordingTool{
			name: "book_flight",
			desc: "Book a flight. Requires a destination city and a departure date.",
			params: map[string]any{
				"type":     "object",
				"required": []any{"destination", "date"},
				"properties": map[string]any{
					"destination": map[string]any{"type": "string", "description": "Destination city"},
					"date":        map[string]any{"type": "string", "description": "Departure date, YYYY-MM-DD"},
				},
			},
			result: "已预订",
		}
		a := newToolAgent(e.Model,
			"你是一个订票助手。预订机票必须调用 book_flight，且需要目的地和出发日期。"+
				"如果用户没有提供必需的信息，先向用户询问，不要自己编造目的地或日期。",
			book)

		if err := a.Prompt(context.Background(), "帮我订一张机票。"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}
		messages := a.GetState().Messages

		// Deterministic: it must NOT have booked with made-up args.
		evals.AssertT(e, "agent does not call book_flight with fabricated args",
			toolNamesDetail(messages), !evals.ToolCalled(messages, "book_flight"))
		output := evals.LastAssistantText(messages)
		evals.LLMRubricT(e, "回答向用户询问缺少的信息（目的地和/或出发日期），没有擅自下单或编造行程", output)
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
