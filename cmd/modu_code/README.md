# modu_code

`modu_code` 是运行在终端中的 AI 编程助手，能在当前工作目录中读写文件、搜索代码并执行命令。

## 安装与启动

需要 Go，具体版本见仓库根目录的 `go.mod`。

```bash
go run ./cmd/modu_code
```

也可以编译后运行：

```bash
go build -o modu_code ./cmd/modu_code
./modu_code
```

启动前至少配置一个模型。以下示例使用 DeepSeek：

```bash
export DEEPSEEK_API_KEY=sk-xxx
go run ./cmd/modu_code
```

没有 provider 时，交互 TUI 会打开配置引导；print、RPC 和 ACP 等非交互模式会直接退出。

## 常用参数

| 参数 | 用途 |
|---|---|
| `-p "<prompt>"` | 执行一次 prompt，输出结果后退出 |
| `--json` | 与 `-p` 配合，输出 NDJSON 事件流 |
| `--rpc` | 通过 stdin/stdout 使用 JSON-line RPC |
| `--acp` | 作为 ACP stdio server 运行 |
| `--no-approve` | 自动允许工具执行；仅在你信任输入和工作区时使用 |
| `--resume <id>` | 用完整 session id 或唯一前缀恢复会话 |
| `--worktree` | 在隔离的 Git worktree 中启动 |

一次性执行示例：

```bash
go run ./cmd/modu_code -p "总结 cmd/modu_code 的职责" --no-approve
```

`--no-approve` 会跳过工具审批，不适合处理不可信 prompt。

## 详细文档

模型配置、TUI 快捷键、斜杠命令、渠道、会话和扩展说明见 [`docs/guides/modu-code.md`](../../docs/guides/modu-code.md)。引擎内部机制见 [`pkg/coding_agent/README.md`](../../pkg/coding_agent/README.md)。
