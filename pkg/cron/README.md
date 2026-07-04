# modu cron (pkg/cron)

Cron-driven agent runner built on modu's CodingAgent. No separate binary, no
subcommand, no daemon you have to remember to start: the scheduler runs
embedded inside `modu_code`'s normal interactive TUI process. Launch
`modu_code` like you always do — if `~/.modu/cron/tasks.yaml` has tasks, they
run on schedule in the background; if it doesn't, the scheduler just sits
idle. Quit `modu_code` and scheduling stops until you launch it again.

## 用法

没有用法——`modu_code` 正常启动就带着调度器。默认配置：`~/.modu/cron/config.yaml`（不存在也没关系，见下）。示例见 `config.example.yaml`。

配置文件缺失完全没问题：调度器会以零任务、`working_dir` = `modu_code` 启动时的当前目录起步，之后靠聊天里说话建任务（见下面）。

**只在交互式 TUI 里跑**——`modu_code -p`（单次 print 模式）、`-rpc`、`-acp` 不启动调度器,因为这些模式要么一次性退出、要么是被外部客户端驱动的短生命周期进程,把定时调度绑在这上面没有意义,也避免在不预期的场合悄悄花 token。想让 cron 任务真正"人不在也能跑",就得让某台机器上一直开着一个 `modu_code` 交互会话(比如 tmux/screen 里挂着)。

## 模型

cron 没有自己的 model/provider 配置——任务运行时用的是 `modu_code` 当前激活的 model(`modu_code config`,存在 `~/.modu/config.toml`),和交互式会话完全一样。只有一处地方管模型,不会出现"cron 跑的是另一个模型"这种意外。想换模型,`modu_code config` 切一次,调度器下一次 tick 自动生效。

## 任务管理:跟 modu_code 说话

新建/查询/删除任务没有 CLI 命令——直接跟 `modu_code` 说话就行：

- **交互式**：在同一个正在跑调度器的 `modu_code` 会话里说"每天早上 8 点跑 git log 看看昨晚有啥提交"，builtin `cron` 扩展给这个 session 注册了 `cron_add` / `cron_list` / `cron_remove` 三个工具，agent 直接调用改 `tasks.yaml`
- **Telegram**：如果配了 `MOMS_TG_TOKEN`（或 `/telegram` 配置）启用了 modu_code 自带的 Telegram bot,同一份工具在那边一样能用——不需要 cron 自己再起一个 bot

调度器运行中会自动热加载新配置（fsnotify）；跨进程并发写 `tasks.yaml`（比如你同时开了两个 `modu_code` 会话）由 `<config>.lock` 上的 flock + 原子写（temp+rename）保护。

## Agent 工具集

调度器跑任务时,任务自己的 agent 也会获得这 3 个管理 cron 任务表的工具（用于自我调度类的 prompt,例如"看看有没有该建的任务"）：

| 工具 | 作用 |
|------|------|
| `cron_add` | 加新任务（参数：id / cron / prompt / goal / timezone / enabled / on_overlap / channels）|
| `cron_list` | 列出当前所有任务和已配置通知 channel |
| `cron_remove` | 按 id 删任务 |

用法上 prompt 里说人话即可。例如把 prompt 写成：

```yaml
- id: bootstrap
  cron: "0 0 9 * * *"
  timezone: Asia/Shanghai
  prompt: |
    Use cron_list to show me what's scheduled, then if there isn't
    already a task that watches GitHub releases, use cron_add to
    create one that runs daily at 18:00.
```

> `cron_add` / `cron_remove` 会用 `yaml.Marshal` 重写 `tasks.yaml`，**用户在 YAML 里写的任务注释会丢失**。

### 热加载

调度器跑起来后无需重启就能感知 config/tasks 变化：

- **fsnotify**：监听 `config.yaml` 的父目录，`config.yaml` 或 `tasks.yaml` 写入事件触发 reload（300ms debounce 合并连续事件）
- **失败回滚**：reload 时如果新 config 解析失败或包含非法 cron 表达式，**保留旧调度器继续跑**，仅打一条 warning。错配置不会让调度器挂
- **in-flight 任务**：旧调度器 Stop 后不再触发，但正在执行的 agent 任务**继续跑完**；新调度器立即接管下一次触发
- fsnotify 初始化失败时热加载整体禁用(打日志说明),不再有 SIGHUP fallback——调度器现在跑在 `modu_code` 进程里,SIGHUP 另有用途(SSH 断线时优雅退出 TUI)

## 运行记录

每次 tick 会产出两份记录,分别回答"发生了什么"和"agent 具体怎么想的":

- **精简运行日志**——`~/.modu/cron/logs/<task_id>/<timestamp>.log`,NDJSON,见下面「任务日志」
- **完整 session 记录**——像交互式会话一样存进 `~/.modu/sessions/`,session 名固定是 `cron:<task_id>`,所以能直接在 session 列表/`--resume` 里按任务 id 找到某次 cron 运行的完整对话(工具调用参数、thinking、原始事件都在,不是精简版)

调度器自身的操作日志(启动/reload/重试/通知失败)写在 `~/.modu/cron/daemon.log`——不会打进 `modu_code` 的终端,因为直接写 stderr 会把 bubbletea 的界面搞花。

### 任务日志

没有 `logs` 子命令——每次 tick 生成一个 NDJSON 文件，直接用文件工具看：

```
~/.modu/cron/logs/<task_id>/<local-RFC3339-timestamp-with-ns>.log
```

```
ls ~/.modu/cron/logs/<task_id>/                 # 列出该任务所有 run,文件名即时间戳
tail -1 ~/.modu/cron/logs/<task_id>/*.log        # 看最新一次 run 的最后一行(通常是 run_end)
jq . ~/.modu/cron/logs/<task_id>/<file>.log      # 格式化看某一次完整事件流
```

文件名和日志里的 `started_at` / `ended_at` 使用本地时区；文件名里的 `:` 会替换成 `-`。落盘前 `runner` 已经把 `coding_agent` 的完整事件流过滤成**精简版**——只保留 7 种关键事件，每行一个 JSON 对象：

| `type` | 字段 | 含义 |
|--------|------|------|
| `run_start` | `task_id` / `prompt` / `trigger` / `timezone` / `has_goal` / `goal` / `goal_verifier` / `started_at` | 本次 tick 起始；`trigger=scheduler` 是自然调度器触发,`manual` 是手动 harness/bootstrap；配置了 `timezone:` 的任务会记录该 IANA timezone；`has_goal=true` 表示本 tick 由任务级 `goal:` 驱动；声明 `goal:` 的任务会记录去空白后的 `goal` 文本和 `goal_verifier=true/false`,用于证明 completion 是否有 maker-checker verifier 门 |
| `session_start` | `session_id` / `model` | session 元信息 |
| `user` | `text` | 用户 prompt 原文 |
| `tool_call` | `name` / `args` | agent 调用工具 |
| `tool_result` | `name` / `ok` / `snippet` | 工具执行结果（snippet 是输出前 5 行 + `+N more lines`，无输出则省略）|
| `assistant` | `text` | assistant 最终回答（thinking / 纯 toolCall turn 已被丢弃）|
| `run_end` | `status` / `goal_status` / `tokens` / `duration_ms` / `ended_at` / `error` | 本次 tick 结束；`status` 见下面三档 cap 一节,声明 `goal:` 的任务会在能读取 goal 状态时记录 `goal_status` |

`run_end` 即使失败也一定会写——它是可靠的"tick 已结束"标记，便于程序消费时判断是否完成。

被丢弃的事件：`agent_start`/`turn_start`/`message_start`/`message_update` 等 envelope 与 per-token 增量、`interrupt`、`thinking` 块、只含 tool call 没真文本的 assistant turn、`toolResult` role 的 message（信息已在 `tool_result` 里）。典型 100+ 行原始事件压到 < 10 行。这份精简版对应完整版就是上一节说的 `~/.modu/sessions/` 里那份 `cron:<task_id>` session。

需要核对某个任务下一次自然触发时间时,复用
`pkg/cron/scheduler.Next(task, from)` 的 task timezone 逻辑,不要手写另一套
cron parser。

## 配置文件

`config.yaml` 放运行配置：

```yaml
working_dir: /path/to/project

tasks_file: tasks.yaml

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

`tasks.yaml` 放 cron 任务：

```yaml
tasks:
  - id: heartbeat
    cron: "*/10 * * * * *"   # 6 字段格式: sec min hour dom mon dow
    timezone: Asia/Shanghai   # 可选 IANA timezone;为空则使用进程本地时区
    prompt: "say hello"
    goal: "Confirm the heartbeat task can run to completion" # 可选: tick 触发即创建 /goal
    enabled: true
    on_overlap: skip          # skip | queue | kill, 默认 skip
    channels: [ops-webhook]   # 完成后推送到这些 channel
```

老的单文件 `config.yaml` 内联 `tasks:` 仍可读；一旦通过 `cron_add` / `cron_remove` 管理任务，新任务表会写入 `tasks_file`。

### 完成通知 `channels`

任务可以配置 `channel: <name>` 或 `channels: [<name>, ...]`。每次运行结束后（成功或失败都会触发），调度器会按任务引用的 channel 发送完成通知，内容包含任务 ID、状态、耗时、日志路径，以及日志中最后一段 assistant 文本。

当前支持的出站类型：

| type | 必填配置 | 行为 |
|------|----------|------|
| `webhook` | `url` 或 `url_env` | POST JSON payload 到指定 URL |
| `telegram` | `token`/`token_env` + `chat_id`/`chat_id_env` | 调 Telegram Bot API `sendMessage` |
| `feishu_webhook` | `url` 或 `url_env` | 调飞书/Lark 自定义机器人 webhook |
| `feishu_bot` | `chat_id`/`chat_id_env`;`app_id`+`app_secret`(或 `*_env`)可省略 | 以飞书应用机器人身份发消息;凭据留空时自动复用 `~/.modu/channels/feishu/config.toml` |

`url` / `token` / `chat_id` 支持 `${ENV}` 展开；更推荐用 `*_env` 字段避免把密钥写进 YAML。调度器热加载任务时也会读取最新 channel 配置；通知失败只记录 warning，不会覆盖本次任务运行结果。

> 这里的 `type: telegram` channel 是**出站**通知（任务跑完推一条消息）。用 Telegram **入站**发自然语言管理任务，见前面「任务管理」——直接用的是 modu_code 自己的 Telegram bot,不是 cron 单独起的。

### 三档 cap(断路器)

任务级三档 + 全局日额度，在 loop 第一次自己跑之前装好（Cap Before You Ship）：

```yaml
tasks:
  - id: morning-triage
    cron: "0 0 6 * * *"
    timezone: Asia/Shanghai
    prompt: "/morning-triage"
    timeout: 45m              # per-run 时长上限，默认 30m；超时 cancel，run_end 记 status=timeout
    max_tokens_per_run: 500000 # per-run token 上限（input+output），超线立即 cancel，status=token_cap
    max_retries: 2            # 仅 status=error 时原地重试（指数退避 30s 起，上限 5m）；cap 触发不重试
```

`config.yaml` 全局项：

```yaml
daily_budget_tokens: 3000000  # 所有任务共享的当日 token 总额度（本地时区按天滚动）
```

- 日额度台账落在 `~/.modu/cron/logs/usage.json`，每次 run 结束累加，超额后当天后续 tick 直接拒跑（`run_end` 记 `status=budget_exceeded`，配置了 channel 会收到通知），第二天自动恢复
- `run_end` 带 `tokens` 字段（本次 run 消耗），`status` 可为 `ok` / `error` / `timeout` / `token_cap` / `budget_exceeded` / `goal_unavailable` / `goal_paused` / `goal_budget_limited`
- timeout / token_cap / budget_exceeded / goal_unavailable / goal_paused / goal_budget_limited 属于断路器：**不会**触发 `max_retries` 重试
- `timeout` / `max_tokens_per_run` / `max_retries` 配置非法时调度器 reload 会失败回滚（与非法 cron 表达式同一套保护）

### 并发策略 `on_overlap`

任务上一次执行未结束，下一次 tick 又到了的处理方式：

| 策略 | 行为 |
|------|------|
| `skip`（默认） | 丢弃新 tick，打 warning |
| `queue` | 排队执行（容量 8，溢出丢弃 + warn）|
| `kill` | 取消旧 ctx，立刻起新 |

任一任务连续 3 次 overlap 会额外打"频率过高 vs 任务耗时"提示，提醒你是 cron 太密还是任务太重。

## Extensions(goal / verifier / workflow)

每个 tick 的 session 会加载 builtin 扩展(读 `~/.modu/extensions.yaml`,与 modu_code 同一份;文件是增量覆盖语义)。这意味着 cron 任务可以用两种方式接 goal:

- 在 `tasks.yaml` 写 `goal:`:runner 会在 tick 开始时直接创建当前 session 的 goal,再发送 `prompt`;agent 完成后必须调用 `update_goal(status=complete)`,若 verifier 驳回则继续 hidden continuation,直到 verifier PASS、goal paused/budgetLimited,或 timeout/token cap 打断。goal 扩展不可用会记为 `status=goal_unavailable`;goal paused / budgetLimited 会分别记为 `status=goal_paused` / `status=goal_budget_limited`;这些都不会被 `max_retries` 当成普通错误重跑。
- 在 prompt 里显式 `create_goal`:仍然支持,适合 prompt 自己决定目标的自调度任务。

`goal` 和 `prompt` 的分工: `goal` 是可验证停止条件,`prompt` 是当轮执行入口。cron tick 仍然是 fresh session;跨天/跨轮记忆应该写进仓库文件(如 `state/*.md`)或 project memory,而不是依赖同一个 goal 跨 tick 续命。extensions.yaml 解析失败时降级为无扩展继续跑(打 warning);若任务声明了 `goal:` 但禁用了 goal 扩展,本次 run 会以 `status=goal_unavailable` 结束,因为停止条件无法被 harness 管理。

## 目录结构

```
pkg/cron/                   # 全部实现
├── daemon.go                # RunScheduler:加载 config、起 scheduler、监听 fsnotify 热加载
├── config/                 # YAML 加载/保存 + Task 模型(原子写)
├── crontools/              # cron_add / cron_list / cron_remove agent 工具(flock)
├── notify/                 # 任务完成后的 channel 推送
├── runlog/                 # 任务日志文件 store + 按天 usage 台账
├── runner/                 # CodingSession 装配的 Runner + 三档 cap + extensions + session 命名
├── scheduler/              # robfig/cron 封装 + 并发策略
├── config.example.yaml
└── README.md
```

`RunScheduler(ctx, cfgPath)` 由 `cmd/modu_code/main.go` 在进入交互式 TUI 前起一个 goroutine直接调用,`ctx` 取消(比如用户退出 `modu_code`)它就停——没有独立进程,没有 CLI 包。模型/provider 解析也没有独立的包,直接用 `pkg/provider`(modu_code 自己的那份)。任务的增删查同样没有独立实现,直接是 `pkg/coding_agent/plugins/extension/cron` 这个 builtin 扩展,挂进任何 modu_code session。
