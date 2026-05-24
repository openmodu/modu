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
| `daemon` | ✅ | 跑调度循环, 按 cron 表达式触发 agent, `Ctrl+C` 退出；自动热加载（fsnotify + SIGHUP）|
| `list` | ✅ | 列出当前配置中的所有任务 |
| `logs <id>` | ✅ | 查看任务历史 (`--tail` / `--file <name>` / `--json`) |
| `add "<desc>"` | ✅ | 用一句自然语言描述，agent 解析后调 `cron_add` 落盘 |
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

**没设任何 env 时的兜底**：自动落到 `http://192.168.5.149:1234/v1` + 模型 `qwen/qwen3.6-35b-a3b`。该 LM Studio 主机可达 + 模型已加载就能直接用，无需 export 任何 env。本地跑可以 `export LMSTUDIO_BASE_URL=http://localhost:1234/v1` 覆盖。启动时 stderr 会打一行提示让你确认。

## 任务管理

```
modu_cron add "<desc>"         # 自然语言描述, agent 解析出 id/cron/prompt 后落盘
modu_cron list                 # 查看现有任务
modu_cron rm <id>              # 交互确认后删除
modu_cron rm <id> --yes        # 直接删除（非 TTY 场景必需）
modu_cron run <id>             # 立即跑一次（调试用：跳过 enabled 和 cron 时间表）
```

### add 工作流

```
$ modu_cron add "每天早上 8 点跑 git log 看看昨晚有啥提交"
Scheduled task "daily-git-log" to run every day at 08:00, listing yesterday's
commits. Restart the daemon (modu_cron daemon) for the schedule to take effect.
```

底层：起一个临时 `CodingSession`，工具集**只暴露** `cron_add` + `cron_list`（屏蔽 Read/Write/Bash 等默认工具，避免 agent 跑偏）。一段 framed prompt 告诉它怎么从描述里推断 id / cron 表达式 / overlap 策略，然后回一句确认。要求 provider env 已配；没配直接报错。

`run` 触发一次后即退出，事件流写入 `~/.modu_cron/logs/<id>/...`，跟 daemon 同一目录，所以可以接着 `logs <id> --tail` 看详情。`run` 必须配置 provider env，dry mode 对它无意义。

## Agent 工具集

daemon 跑任务时（或 `run <id>`），agent 自动获得 3 个管理 cron 任务表的工具：

| 工具 | 作用 |
|------|------|
| `cron_add` | 加新任务（参数：id / cron / prompt / enabled / on_overlap）|
| `cron_list` | 列出当前所有任务 |
| `cron_remove` | 按 id 删任务 |

用法上 prompt 里说人话即可。例如把 prompt 写成：

```yaml
- id: bootstrap
  cron: "0 0 9 * * *"
  prompt: |
    Use cron_list to show me what's scheduled, then if there isn't
    already a task that watches GitHub releases, use cron_add to
    create one that runs daily at 18:00.
```

或者用 `run bootstrap` 触发一次，agent 调工具改 config.yaml，然后告诉用户**重启 daemon** 生效（daemon 暂不热加载）。

多任务并发改 config 时，工具内部用包级 mutex 串行化文件读写，单进程内不会丢任务。

> `add` / `rm` 会用 `yaml.Marshal` 重写整个文件，**用户在 YAML 里写的注释会丢失**。

### daemon 热加载

daemon 跑起来后无需重启就能感知 config 变化：

- **fsnotify**：监听 `config.yaml` 的父目录，写入事件触发 reload（300ms debounce 合并连续事件）
- **SIGHUP**：`kill -HUP <pid>`，作为手动 fallback
- **失败回滚**：reload 时如果新 config 解析失败或包含非法 cron 表达式，**保留旧调度器继续跑**，仅打一条 warning。错配置不会让 daemon 挂
- **in-flight 任务**：旧调度器 Stop 后不再触发，但正在执行的 agent 任务**继续跑完**；新调度器立即接管下一次触发

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

落盘前 `runner` 已经把 `coding_agent` 的完整事件流过滤成**精简版**——只保留 7 种关键事件，每行一个 JSON 对象：

| `type` | 字段 | 含义 |
|--------|------|------|
| `run_start` | `task_id` / `prompt` / `started_at` | 本次 tick 起始 |
| `session_start` | `session_id` / `model` | session 元信息 |
| `user` | `text` | 用户 prompt 原文 |
| `tool_call` | `name` / `args` | agent 调用工具 |
| `tool_result` | `name` / `ok` / `snippet` | 工具执行结果（snippet 是输出前 5 行 + `+N more lines`，无输出则省略）|
| `assistant` | `text` | assistant 最终回答（thinking / 纯 toolCall turn 已被丢弃）|
| `run_end` | `status` / `duration_ms` / `ended_at` / `error` | 本次 tick 结束；`status` 为 `ok` 或 `error`，失败带 `error` 字段 |

`run_end` 即使失败也一定会写——它是可靠的"tick 已结束"标记，便于程序消费时判断是否完成。

被丢弃的事件：`agent_start`/`turn_start`/`message_start`/`message_update` 等 envelope 与 per-token 增量、`interrupt`、`thinking` 块、只含 tool call 没真文本的 assistant turn、`toolResult` role 的 message（信息已在 `tool_result` 里）。典型 100+ 行原始事件压到 < 10 行。

> 该格式是新版本，**与老的完整事件流日志不兼容**。已有旧日志解码会落到 `· <type>` unknown-event 行——重跑一次即可换上新格式。

查看历史：

```
modu_cron logs <id>                   # 列出该任务所有 run, 最新在上
modu_cron logs <id> --tail            # 解码最近一次为可读文本
modu_cron logs <id> --tail --json     # 同上但输出原 NDJSON（程序友好）
modu_cron logs <id> --file <name>     # 看指定文件 (从 list 拷文件名)
```

`--json` 直接 cat 文件，可以喂给 `jq` 之类工具做后续处理。

## 目录结构

```
cmd/modu_cron/
├── main.go                 # 入口 + 子命令路由
├── config.example.yaml
├── README.md
└── internal/
    ├── cli/                # 子命令实现
    ├── config/             # YAML 加载/保存 + Task 模型
    ├── crontools/          # cron_add / cron_list / cron_remove agent 工具
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
5. ✅ agent 工具集 `cron_add` / `cron_list` / `cron_remove`，让 agent 用自然语言管理任务
6. ✅ daemon 热加载（SIGHUP + fsnotify，失败回滚保留旧调度器）
