# modu_eval

`modu_eval` 运行 LLM 评测、检查通过率，并查看生成的 `evals.jsonl`。确定性行为应优先使用普通 Go 测试；只有语义质量无法精确断言时才使用评分模型。

## 启动

运行评测并打开结果界面：

```bash
GOEVALS=1 \
EVAL_PROVIDER=lmstudio \
EVAL_BASE_URL=http://localhost:1234/v1 \
EVAL_MODEL=qwen/qwen3.6-35b-a3b \
go run ./cmd/modu_eval run -v ./pkg/agent -run Eval
```

CI 中使用 `check`；Go 测试失败或通过率低于阈值时，命令返回非零状态：

```bash
GOEVALS=1 \
EVAL_PROVIDER=lmstudio \
EVAL_BASE_URL=http://localhost:1234/v1 \
EVAL_MODEL=qwen/qwen3.6-35b-a3b \
go run ./cmd/modu_eval check -v ./pkg/agent -run Eval
```

## 常用命令与参数

| 命令或参数 | 用途 |
|---|---|
| `run [go test 参数]` | 运行评测并打开 TUI |
| `check [go test 参数]` | 运行评测，输出适合 CI 的摘要 |
| `check --min-pass-rate 0.8 ...` | 把每个测试的最低通过率设为 `0.8`；默认 `1.0` |
| `view -f evals.jsonl` | 查看已有结果 |
| `view --plain --output` | 输出包含结果摘录的纯文本报告 |
| `comment [go test 参数]` | 生成 `comment.md` |
| `agent <任务文件或目录>` | 对本地编码 Agent 运行 Markdown 任务集 |
| `agent --tui=false ...` | 在自动化环境中关闭交互界面 |

## 详细文档

环境变量、rubric、确定性断言、Agent 任务格式、结果目录和 TUI 操作见 [`docs/guides/modu-eval.md`](../../docs/guides/modu-eval.md)。
