# Modu 定时任务

`pkg/cron` 在 `modu_code` 的交互式 TUI 进程内定时运行 Coding Agent 任务。它没有独立二进制、子命令或常驻服务：关闭 `modu_code` 后，调度停止；`modu_code -p`、`-rpc` 和 `-acp` 模式也不会启动调度器。

要让任务在无人值守时继续运行，需要在一台持续在线的机器上保持 `modu_code` 交互会话，例如放进 tmux 或 screen。

## 启动与模型

正常启动 `modu_code` 即可。默认配置路径是 `~/.modu/cron/config.yaml`，任务表默认是同目录的 `tasks.yaml`。两个文件都可以不存在：调度器会以零任务启动，并把进程启动时的当前目录作为 `working_dir`。

Cron 不单独配置模型。每次任务运行时读取 `modu_code config` 当前激活的模型，即 `~/.modu/config.toml` 中的配置。切换模型后，下一个 Tick 自动使用新配置。

## 管理任务

在 `modu_code` 中使用 `/cron`：

| 命令 | 行为 |
|---|---|
| `/cron add <自然语言需求>` | 让 Agent 补齐信息并调用 `cron_add` |
| `/cron list` | 直接读取任务表，显示 UUID、名称、表达式、启用状态和 Prompt |
| `/cron rm <UUID>` | 按 UUID 删除任务 |
| `/cron update <自然语言需求>` | 让 Agent 调用 `cron_update` 修改任务 |

也可以直接说“每天早上 8 点运行 git log，汇总昨晚的提交”。Cron 扩展向当前会话注册 `cron_add`、`cron_list`、`cron_remove` 和 `cron_update`，Agent 会据此修改 `tasks.yaml`。启用 `modu_code` 的 Telegram Bot 后，同一组工具也能从 Telegram 入站消息调用；Cron 不会另起机器人。

`uuid` 是任务的唯一身份，用于调度去重、更新、删除、日志目录和通知中的 `task_id`；`name` 只用于展示，允许重复。旧格式的 `id` 会在加载时迁移为 `name` 并补充 UUID。

任务工具也会注入定时运行的 Agent，因此任务可以按 Prompt 自行查询或修改任务表：

| 工具 | 作用 |
|---|---|
| `cron_add` | 新建任务，UUID 自动生成 |
| `cron_list` | 列出任务和通知 Channel |
| `cron_remove` | 按 UUID 删除任务 |
| `cron_update` | 按 UUID 修改任务 |

`cron_add`、`cron_remove` 和 `cron_update` 使用 `yaml.Marshal` 重写 `tasks.yaml`，原文件中的任务注释会丢失。需要保留的说明应写进文档，不要依赖 YAML 注释。

## 配置

`config.yaml` 保存运行目录、任务文件、日额度和通知 Channel：

```yaml
working_dir: /path/to/project
tasks_file: tasks.yaml
daily_budget_tokens: 3000000

channels:
  ops-webhook:
    type: webhook
    url_env: MODU_CRON_OPS_WEBHOOK_URL
  telegram-home:
    type: telegram
    token_env: MODU_CRON_TG_TOKEN
    chat_id_env: MODU_CRON_TG_CHAT_ID
  feishu-alerts:
    type: feishu_webhook
    url_env: MODU_CRON_FEISHU_WEBHOOK_URL
```

`tasks.yaml` 保存任务。Cron 表达式使用 6 个字段，顺序为 `sec min hour dom mon dow`：

```yaml
tasks:
  - uuid: 11111111-1111-1111-1111-111111111111
    name: heartbeat
    cron: "*/10 * * * * *"
    timezone: Asia/Shanghai
    prompt: "say hello"
    goal: "Confirm the heartbeat task can run to completion"
    enabled: true
    on_overlap: skip
    channels: [ops-webhook]
    timeout: 45m
    max_tokens_per_run: 500000
    max_retries: 2
```

`timezone` 使用 IANA 时区名称；为空时使用进程本地时区。不要在表达式内写 `CRON_TZ=` 或 `TZ=`。需要计算下一次触发时间时，调用 `pkg/cron/scheduler.Next(task, from)`，不要另写 Cron 解析逻辑。

旧版单文件配置中的内联 `tasks:` 仍可读取。通过 Cron 工具再次保存后，任务会写入 `tasks_file`。

## 停止条件与断路器

无人值守任务至少要配置时长上限；成本敏感的任务还应配置单次和每日 Token 上限：

| 配置 | 判据 |
|---|---|
| `timeout` | 单次运行时长；默认 `30m`，超时后取消并记录 `status=timeout` |
| `max_tokens_per_run` | 单次输入与输出 Token 总量；`0` 表示不限制 |
| `max_retries` | 只对 `status=error` 重试；退避从 30 秒开始，最长 5 分钟 |
| `daily_budget_tokens` | 每个任务的每日 Token 上限；`0` 表示不限制 |

每日用量账本位于 `~/.modu/cron/logs/usage.json`。一个任务超过日额度后，只拒绝该任务当天后续 Tick，第二天恢复。`timeout`、`token_cap`、`budget_exceeded`、`goal_unavailable`、`goal_paused` 和 `goal_budget_limited` 都是断路器状态，不触发 `max_retries`。

`timeout`、`max_tokens_per_run`、`max_retries` 或 Cron 表达式非法时，配置重载失败，旧调度器继续运行。

### `goal` 与 Verifier

`goal` 是可验证的停止条件，`prompt` 是本轮执行入口。任务声明 `goal` 后，Runner 会在 Tick 开始时创建 Session Goal，再发送 Prompt。Agent 调用 `update_goal(status=complete)` 后，已启用的 Verifier 会独立检查；拒绝会触发隐藏续跑，直到通过、暂停、用尽 Goal 预算，或被时长/Token 断路器终止。

每次 Tick 都创建新 Session。跨轮记忆应写入仓库文件，例如 `state/*.md`，或写入项目 Memory；不要假设 Goal 会跨 Tick 延续。任务也可以不声明 `goal`，而在 Prompt 中显式调用 `create_goal`。

Session 使用与 `modu_code` 相同的 `~/.modu/extensions.yaml`。文件解析失败时，任务降级为无扩展运行；如果任务声明了 `goal`，但 Goal 扩展不可用，本次运行以 `status=goal_unavailable` 结束。

## 重叠策略

上一次运行未结束时，`on_overlap` 决定如何处理新 Tick：

| 策略 | 行为 |
|---|---|
| `skip` | 丢弃新 Tick并记录警告；默认值 |
| `queue` | 最多排队 8 次，超出后丢弃并记录警告 |
| `kill` | 取消旧 Context，等旧运行退出后启动新运行 |

同一任务连续发生 3 次重叠时，日志会提示检查执行频率和任务耗时。

## 完成通知

任务通过 `channel: <name>` 或 `channels: [<name>, ...]` 引用 `config.yaml` 中的出站 Channel。成功和失败都会通知；内容包含任务 UUID、名称、状态、耗时、日志路径、最后一段 Assistant 文本、本轮新增的 `inbox/` 文件和日志中发现的 PR 链接。

| `type` | 必填配置 | 行为 |
|---|---|---|
| `webhook` | `url` 或 `url_env` | POST JSON |
| `telegram` | `token`/`token_env` 和 `chat_id`/`chat_id_env` | 调用 Telegram Bot API `sendMessage` |
| `feishu_webhook` | `url` 或 `url_env` | 调用飞书/Lark 自定义机器人 Webhook |
| `feishu_bot` | `chat_id`/`chat_id_env` | 使用飞书应用机器人；凭据为空时复用 `~/.modu/channels/feishu/config.toml` |

`url`、`token` 和 `chat_id` 支持 `${ENV}` 展开；优先使用 `*_env`，避免把密钥写入 YAML。通知失败只记录警告，不会覆盖任务运行结果。这里的 Telegram Channel 只负责出站通知，不能替代 `modu_code` 的 Telegram 入站机器人。

## 运行记录

每次 Tick 生成两份记录：

- `~/.modu/cron/logs/<task_uuid>/<timestamp>.log`：筛选后的 NDJSON 事件。
- `~/.modu/sessions/` 中名为 `cron:<task_name>:<uuid-prefix>` 的 Session：完整消息、Thinking 和工具调用。

调度器自己的启动、重载、重试和通知错误写入 `~/.modu/cron/daemon.log`，避免 stderr 破坏 Bubble Tea 界面。Cron 没有 `logs` 子命令，直接查看文件：

```bash
ls ~/.modu/cron/logs/<task_uuid>/
tail -1 ~/.modu/cron/logs/<task_uuid>/*.log
jq . ~/.modu/cron/logs/<task_uuid>/<file>.log
```

文件名和 `started_at`、`ended_at` 使用本地时区；文件名中的 `:` 会替换成 `-`。精简日志只保留 7 类事件：

| `type` | 关键字段 | 含义 |
|---|---|---|
| `run_start` | `task_id`, `task_name`, `prompt`, `trigger`, `timezone`, `has_goal`, `goal`, `goal_verifier`, `started_at` | 本次 Tick 的任务和停止条件 |
| `session_start` | `session_id`, `model` | Session 元数据 |
| `user` | `text` | 原始 Prompt |
| `tool_call` | `name`, `args` | 工具调用 |
| `tool_result` | `name`, `ok`, `snippet` | 工具结果；Snippet 最多保留前 5 行 |
| `assistant` | `text` | Assistant 最终文本 |
| `run_end` | `status`, `goal_status`, `tokens`, `duration_ms`, `ended_at`, `error` | 终态、用量和耗时 |

即使任务失败，`run_end` 也会写入。Envelope、逐 Token 增量、Thinking、Interrupt 和只有 Tool Call 的 Assistant Turn 不进入精简日志。

## 热加载与并发写

调度器监听 `config.yaml` 所在目录。`config.yaml` 或 `tasks.yaml` 写入后，以 300 ms Debounce 重载：

- 新配置解析失败或包含非法 Cron 表达式时，保留旧调度器。
- 旧调度器停止触发新任务；已经运行的任务继续结束，新调度器接管后续 Tick。
- fsnotify 初始化失败时禁用热加载并记录日志，不使用 SIGHUP 兜底。
- 多个 `modu_code` 进程同时修改任务表时，通过 `<config>.lock` 的 `flock` 和 `temp + rename` 原子写保护文件。

## 目录

```text
pkg/cron/
├── daemon.go            # 启动、重载和生命周期
├── config/              # YAML、Task 和原子写
├── crontools/           # cron_add/list/remove/update
├── notify/              # 完成通知
├── runlog/              # NDJSON 日志和用量账本
├── runner/              # CodingSession、断路器和扩展装配
├── scheduler/           # Cron 与重叠策略
├── config.example.yaml
└── README.md
```

`RunScheduler(ctx, cfgPath)` 由 `cmd/modu_code/main.go` 在进入 TUI 前启动。取消 `ctx` 即停止调度；本包不提供独立 CLI。
