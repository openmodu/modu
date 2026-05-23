# modu_cron

Cron-driven agent runner built on modu's CodingAgent. Daemon + CLI dual-form:
the daemon owns the schedule, the CLI manages tasks.

## 状态

`daemon` / `list` 已可用，每个 tick 起一个独立 `CodingSession` 跑配置好的 prompt，事件流写入任务日志文件。`add` / `run` / `rm` / `logs` 子命令占位待业务期落地。

## 安装

```
go build ./cmd/modu_cron
```

## 用法

```
modu_cron [-c <config>] <subcommand>
```

默认配置: `~/.modu_cron/config.yaml`（用 `-c` 覆盖）。示例见 `config.example.yaml`。

### 子命令

| 命令 | 状态 | 说明 |
|------|------|------|
| `daemon` | ✅ | 跑调度循环, 按 cron 表达式触发 agent, `Ctrl+C` 退出 |
| `list` | ✅ | 列出当前配置中的所有任务 |
| `logs <id>` | ✅ | 查看任务历史 (`--tail` / `--file <name>` / `--json`) |
| `add` | ✅ | 交互式添加任务（id/cron/prompt/enabled/on_overlap）|
| `rm <id>` | ✅ | 删除任务（TTY 下默认问，`--yes` 跳过；非 TTY 必须 `--yes`）|
| `run <id>` | ✅ | 立即触发一次（忽略 `enabled` 和 cron 表达式，用于调试）|

## Provider 配置

`modu_cron` 只读环境变量（与 `modu_code` 同序），第一项匹配即用：

| 变量 | 必填补充 |
|------|----------|
| `ANTHROPIC_API_KEY` | `ANTHROPIC_MODEL` |
| `OPENAI_API_KEY` | 可选 `OPENAI_MODEL`（默认 `gpt-4o`）、`OPENAI_BASE_URL` |
| `DEEPSEEK_API_KEY` | 可选 `DEEPSEEK_MODEL`（默认 `deepseek-chat`）|
| `OLLAMA_HOST` | `OLLAMA_MODEL` |
| `LMSTUDIO_BASE_URL` | 可选 `LMSTUDIO_MODEL` |

任何一项都没配 → daemon 走 **dry mode**，只打 tick 日志、不调用 LLM，方便先验证调度。

## 任务管理

```
modu_cron add                  # 交互式输入 id/cron/prompt/enabled/on_overlap
modu_cron list                 # 查看现有任务
modu_cron rm <id>              # 交互确认后删除
modu_cron rm <id> --yes        # 直接删除（非 TTY 场景必需）
modu_cron run <id>             # 立即跑一次（调试用：跳过 enabled 和 cron 时间表）
```

`run` 触发一次后即退出，事件流写入 `~/.modu_cron/logs/<id>/...`，跟 daemon 同一目录，所以可以接着 `logs <id> --tail` 看详情。`run` 必须配置 provider env，dry mode 对它无意义。

> daemon 启动后不会监听 `config.yaml` 变化。`add` / `rm` 后需要重启 daemon 才生效。
> `add` / `rm` 会用 `yaml.Marshal` 重写整个文件，**用户在 YAML 里写的注释会丢失**。

## 配置文件

```yaml
tasks:
  - id: heartbeat
    cron: "*/10 * * * * *"   # 6 字段格式: sec min hour dom mon dow
    prompt: "say hello"
    enabled: true
    on_overlap: skip          # skip | queue | kill, 默认 skip
```

### 并发策略 `on_overlap`

任务上一次执行未结束，下一次 tick 又到了的处理方式：

| 策略 | 行为 |
|------|------|
| `skip`（默认） | 丢弃新 tick，打 warning |
| `queue` | 排队执行（容量 8，溢出丢弃 + warn）|
| `kill` | 取消旧 ctx，立刻起新 |

任一任务连续 3 次 overlap 会额外打"频率过高 vs 任务耗时"提示，提醒你是 cron 太密还是任务太重。

## 任务日志

每次 tick 生成一个 NDJSON 文件：

```
~/.modu_cron/logs/<task_id>/<RFC3339-timestamp-with-ns>.log
```

里面是 `coding_agent` 完整事件流（session_start, message_update, tool_call, tool_result, message_end, session_end）。

查看历史：

```
modu_cron logs <id>                   # 列出该任务所有 run, 最新在上
modu_cron logs <id> --tail            # 解码最近一次为可读文本
modu_cron logs <id> --tail --json     # 同上但输出原 NDJSON
modu_cron logs <id> --file <name>     # 看指定文件 (从 list 拷文件名)
```

可读视图保留：session 边界、tool call/result（含 ERROR 标识）、assistant 最终文本；过滤掉 `message_update` 的 per-token 增量噪音。

## 目录结构

```
cmd/modu_cron/
├── main.go                 # 入口 + 子命令路由
├── config.example.yaml
├── README.md
└── internal/
    ├── cli/                # 子命令实现
    ├── config/             # YAML 加载/保存 + Task 模型
    ├── provider/           # env-only LLM provider 解析
    ├── runlog/             # 任务日志文件 store
    ├── runner/             # CodingSession 装配的 Runner
    └── scheduler/          # robfig/cron 封装 + 并发策略
```

## 业务开发路线

1. ✅ `Runner` 接 `CodingSession`, prompt 真跑起来，事件流落任务日志
2. ✅ `logs <id>` 子命令: 列出 / tail / 指定文件 / NDJSON 原文
3. ✅ `add` / `rm` 子命令: 交互式编辑 + 写回 YAML（daemon 需重启）
4. ✅ `run <id>` 子命令: 不等到点，立即跑一次
5. daemon 热加载（SIGHUP / fsnotify）
6. agent 工具集 `cron_add` / `cron_list` / `cron_remove`，让 agent 用自然语言管理任务
