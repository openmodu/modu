# modu_eval 评测指南

`modu_eval` 运行 LLM 评测、汇总通过率，并查看生成的 `evals.jsonl`。它解决的是精确断言难以覆盖的语义质量问题，例如回答是否满足约束、工具调用是否合理、摘要是否保留关键信息。

确定性行为应继续使用普通 Go 断言。能比较字符串、检查文件或执行命令得到答案时，不要花一次评分模型调用。

## 两种评测模式

| 模式 | 输入 | 输出 | 适用场景 |
|---|---|---|---|
| Go eval test | 带 `Eval` 的 Go 测试 | `evals.jsonl` | 直接调用包 API，组合确定性断言和 LLM rubric |
| Agent task | 带 YAML frontmatter 的 Markdown 任务 | 每任务一个 `result.json`，可选 `summary.md` | 从进程外评测 `modu_code` 或其他本地编码 Agent |

两种模式互不替代。要验证包内状态时写 Go eval；要验证 Agent 在临时工作区中能否完成任务时写 Markdown task。

## 运行 Go eval

至少设置 `EVAL_MODEL`。下面示例连接本地 LM Studio：

```bash
GOEVALS=1 \
EVAL_PROVIDER=lmstudio \
EVAL_BASE_URL=http://localhost:1234/v1 \
EVAL_MODEL=qwen/qwen3.6-35b-a3b \
go run ./cmd/modu_eval run -v ./pkg/agent -run Eval
```

`run` 会把参数转交给 `go test`，测试结束后查找 `evals.jsonl` 并打开 TUI。

CI 中使用 `check`：

```bash
GOEVALS=1 \
EVAL_PROVIDER=lmstudio \
EVAL_BASE_URL=http://localhost:1234/v1 \
EVAL_MODEL=qwen/qwen3.6-35b-a3b \
go run ./cmd/modu_eval check -v ./pkg/agent -run Eval
```

满足任一条件时 `check` 返回非零状态：

- `go test` 失败。
- 某个测试的通过率低于 `--min-pass-rate`。

最低通过率默认为 `1.0`。对于允许概率波动的 rubric，可以重复执行并降低阈值：

```bash
GOEVALS=5 \
EVAL_PROVIDER=lmstudio \
EVAL_BASE_URL=http://localhost:1234/v1 \
EVAL_MODEL=qwen/qwen3.6-35b-a3b \
go run ./cmd/modu_eval check --min-pass-rate 0.8 -v ./pkg/agent -run Eval
```

这里的判据是“每个测试至少通过 80% 的记录”，不是所有记录合并后的总通过率。

生成适合 GitHub 评论的报告：

```bash
go run ./cmd/modu_eval comment -v ./pkg/agent -run Eval
```

结果写入当前目录的 `comment.md`。

## 查看已有结果

打开交互界面：

```bash
go run ./cmd/modu_eval view -f evals.jsonl
```

自动化环境中输出纯文本：

```bash
go run ./cmd/modu_eval view --plain --output -f evals.jsonl
```

`--failures-only` 只显示失败项，`--output` 让纯文本报告包含输出摘录。

Rubric 结果 TUI 的按键：

```text
↑/↓ 或 j/k     移动
Enter           打开详情
Esc             返回列表
f               切换仅失败项
q 或 Ctrl+C     退出
```

详情包含 Provider、评分模型、rubric、被评输出、评分理由和分数。

## 配置模型

被测模型：

| 环境变量 | 作用 |
|---|---|
| `EVAL_PROVIDER` | Provider id；可用逗号分隔多个值，默认 `lmstudio` |
| `EVAL_BASE_URL` | OpenAI 兼容 base URL；默认值随 Provider 变化 |
| `EVAL_API_KEY` | 被测 Provider 的 API key |
| `EVAL_MODEL` | 被测模型；必填 |

还支持 Provider 专用覆盖：

```text
EVAL_OPENAI_BASE_URL
EVAL_OPENAI_API_KEY
EVAL_OPENAI_MODEL
EVAL_LMSTUDIO_BASE_URL
EVAL_LMSTUDIO_MODEL
```

评分模型：

| 环境变量 | 作用 |
|---|---|
| `GRADER_PROVIDER` | 评分 Provider；默认复用被测 Provider |
| `GRADER_BASE_URL` | 评分 Provider 的 base URL |
| `GRADER_API_KEY` | 评分 Provider 的 API key |
| `GRADER_MODEL` | 评分模型；默认复用 `EVAL_MODEL` |

被测模型与评分模型相同会引入自评偏差。对结果稳定性有要求时，使用独立且能力足够的评分模型，并保留模型 id 和 Prompt 版本。

## 编写 Go eval

Eval 测试与被测包放在一起，测试名包含 `Eval`，方便用 `-run Eval` 选择：

```go
func TestBasicAgentResponseEval(t *testing.T) {
	evals.Run(t, "basic chinese factual answer", func(e *evals.EvalT) {
		providers.Register(e.Provider)

		a := agent.NewAgent(types.Config{
			InitialState: &types.State{
				SystemPrompt: "你是一个简洁、准确的中文助手。",
				Model:        e.Model,
			},
		})

		if err := a.Prompt(context.Background(), "请用中文回答：法国的首都是哪里？"); err != nil {
			e.Fatalf("prompt agent: %v", err)
		}

		output := evals.LastAssistantText(a.GetState().Messages)
		if output == "" {
			e.Fatal("expected non-empty assistant output")
		}

		evals.LLMRubricT(e, "回答使用中文", output)
		evals.LLMRubricT(e, "回答明确指出法国的首都是巴黎", output)
	})
}
```

一条 rubric 只判断一件事。把“正确、简洁、使用中文且有引用”写在同一条 rubric 中，失败后无法知道问题在哪。

### 优先使用确定性断言

`pkg/evals` 提供会记录结果的确定性检查，不调用评分模型：

```go
evals.ContainsT(e, "巴黎", output)
evals.NotContainsT(e, "伦敦", output)
evals.RegexpT(e, `^\d{4}-\d{2}-\d{2}$`, output)
evals.ToolCalledT(e, a.GetState().Messages, "read")
```

`ToolCalledT` 检查消息中记录的真实工具调用，不相信模型在文本中自称“我调用了工具”。如果工具应产生文件或命令结果，还要继续断言副作用。

### 处理概率波动

`LLMRubricT` 在 rubric 不通过时让测试失败。需要按多次运行的总体比例设门槛时，使用 `LLMRubricSoft`：

```go
evals.LLMRubricSoft(e, "回答明确指出法国的首都是巴黎", output)
```

然后用 `GOEVALS=N` 重复执行，并通过 `check --min-pass-rate` 设置每个测试的最低比例。Rubric 只有在评分结果 `pass=true` 且 `score >= 0.6` 时计为通过。

## 运行 Agent task

`modu_eval agent` 在临时工作区中运行 Markdown 任务，调用本地编码 Agent，执行确定性检查，并为每个任务写 `result.json`。

默认被测命令等价于：

```bash
go run ./cmd/modu_code --no-approve --json -p "<task prompt>"
```

运行仓库内的任务集：

```bash
go run ./cmd/modu_eval agent eval/tasks/modu_code --keep-going
```

默认结果目录为 `eval/results/modu-code-<timestamp>/`。每个结果包含 stdout、stderr、Assistant 文本、解析到的工具调用、工作区快照、检查结果和分数；默认还会生成 `summary.md`。

在自动化环境关闭运行中 TUI：

```bash
go run ./cmd/modu_eval agent --tui=false eval/tasks/modu_code --keep-going
```

改用已经安装的 `modu_code`：

```bash
go run ./cmd/modu_eval agent \
  --agent modu_code \
  --agent-arg --no-approve \
  eval/tasks/modu_code
```

常用参数：

| 参数 | 默认值 | 作用 |
|---|---|---|
| `--agent` | `go` | Agent 可执行文件 |
| `--agent-arg` | `run ./cmd/modu_code --no-approve` | prompt 前的参数；可重复 |
| `--prompt-arg` | `-p` | 传递 prompt 的参数名；空值表示位置参数 |
| `--json-output` | `true` | 请求 `modu_code` 输出 JSON 事件并解析工具信息 |
| `--output` | 自动生成目录 | 结果目录 |
| `--timeout` | `300` | 每个任务的最长秒数 |
| `--keep-going` | `false` | 任务失败后继续 |
| `--summary` | `true` | 写入 `summary.md` |
| `--tui` | `true` | 终端可交互时打开结果界面 |

`--no-approve` 会自动允许 Agent 工具执行。Task 必须在 runner 创建的临时工作区中运行，且输入应受信任。

## 编写 Agent task

Task 是带 YAML frontmatter 的 Markdown：

```markdown
---
id: edit_readme
name: edit README
timeout_seconds: 120
workspace_files:
  - path: "README.md"
    content: |
      # Demo
checks:
  - name: assistant responded
    type: assistant_responded
  - name: readme updated
    type: file_contains
    path: "README.md"
    value: "Usage"
---

## Prompt

Update README.md with a Usage section.
```

支持的确定性检查：

- `assistant_responded`
- `output_contains`、`output_not_contains`、`output_regex`
- `tool_called`，要求启用 JSON 输出
- `file_exists`、`file_contains`、`file_not_contains`、`file_regex`
- `command_succeeds`，例如 `command: ["go", "test", "./..."]`

如果被测 Agent 不能直接写本地工作区，可以返回文件 artifact：

````markdown
```file path="relative/path.ext"
content
```
````

Runner 会在评分前把 artifact 写入工作区。不要只检查 Assistant 是否声称完成；至少用文件断言或 `command_succeeds` 验证结果。

Agent 结果 TUI 的按键：

```text
↑/↓ 或 j/k     移动任务
Enter           打开任务详情
Esc             返回摘要
f               切换仅失败项
q 或 Ctrl+C     运行结束后退出
```

## 示例与验收

仓库内示例：

- `pkg/agent/agent_eval_test.go`：事实回答 rubric。
- `pkg/agent/tool_eval_test.go`：真实工具调用和结果依据。
- `pkg/coding_agent/coding_eval_test.go`：临时目录中的文件副作用、Go 代码和 rubric。

CLI 的无模型单元测试：

```bash
go test ./cmd/modu_eval
go test ./pkg/evals
```

查看当前参数而不依赖本文中的默认值：

```bash
go run ./cmd/modu_eval --help
go run ./cmd/modu_eval agent --help
```

真正的 LLM eval 还依赖可访问的模型服务，不能用单元测试通过代替。验收一次评测至少要记录 Provider、模型 id、Prompt 或 task 版本、重复次数、通过率阈值和生成的结果文件。
