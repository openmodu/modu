# modu cron (pkg/cron)

Cron-driven agent runner built on modu's CodingAgent. Daemon + CLI dual-form:
the daemon owns the schedule, the CLI manages tasks. 入口收敛在 modu_code 里
(`modu_code cron <subcommand>`),原独立二进制 `cmd/modu_cron` 已移除。

## 安装

```
go build ./cmd/modu_code
```

## 用法

```
modu_code cron [-c <config>] <subcommand>
```

默认配置: `~/.modu_cron/config.yaml`（用 `-c` 覆盖）。示例见 `config.example.yaml`。

### 子命令

| 命令 | 状态 | 说明 |
|------|------|------|
| `init` | ✅ | 初始化 `config.yaml` + `tasks.yaml` |
| `daemon` | ✅ | 跑调度循环, 按 cron 表达式触发 agent, `Ctrl+C` 退出；自动热加载（fsnotify + SIGHUP）|
| `list` | ✅ | 列出当前配置中的所有任务 |
| `logs <id>` | ✅ | 查看任务历史 (`--tail` / `--file <name>` / `--json`) |
| `rm <id>` | ✅ | 删除任务（TTY 下默认问，`--yes` 跳过；非 TTY 必须 `--yes`）|
| `run <id>` | ✅ | 立即触发一次（忽略 `enabled` 和 cron 表达式，用于调试）|

没有 `add` 子命令——用自然语言建任务就直接跟 `modu_code` 说话（见下面「自然语言管理」），不需要单独的 CLI 命令。

## 初始化

```
modu_code cron init
modu_code cron init --workdir /path/to/project
modu_code cron init --non-interactive
```

默认会写：

- `~/.modu_cron/config.yaml`：工作目录、notification channels、`tasks_file`
- `~/.modu_cron/tasks.yaml`：cron 任务列表

默认 `working_dir` 是运行 `init` 时的当前目录；默认 Telegram channel 名为 `telegram-home`，读取 `MODU_CRON_TG_TOKEN` 和 `MODU_CRON_TG_CHAT_ID`。

`init` 默认是交互式的，会逐项询问工作目录、任务文件、Telegram channel；回车接受默认值。Telegram 会先询问真实 bot token / chat id，留空时才继续询问 `MODU_CRON_TG_TOKEN` / `MODU_CRON_TG_CHAT_ID` 这类 env 名。脚本里使用 `--non-interactive` 可直接按 flags/defaults 写文件；不需要 Telegram channel 时使用 `--no-telegram`。

## 模型

cron 没有自己的 model/provider 配置——任务运行时用的是 `modu_code` 当前激活的 model(`modu_code config`,存在 `~/.modu/config.toml`),和交互式会话完全一样。只有一处地方管模型,不会出现"cron 跑的是另一个模型"这种意外。想换模型,`modu_code config` 切一次,daemon 下一次 tick 自动生效。

## 任务管理

```
modu_code cron list                 # 查看现有任务
modu_code cron rm <id>              # 交互确认后删除
modu_code cron rm <id> --yes        # 直接删除（非 TTY 场景必需）
modu_code cron run <id>             # 立即跑一次（调试用：跳过 enabled 和 cron 时间表）
```

`run` 触发一次后即退出，事件流写入 `~/.modu_cron/logs/<id>/...`，跟 daemon 同一目录，所以可以接着 `logs <id> --tail` 看详情。

## 自然语言管理

新建/查询/删除任务不走专门的 CLI 或独立的 Telegram 轮询——直接跟 `modu_code` 说话就行：

- **交互式**：`modu_code`（不带 `cron` 子命令，正常进 TUI）里说"每天早上 8 点跑 git log 看看昨晚有啥提交"，builtin `cron` 扩展给这个 session 注册了 `cron_add` / `cron_list` / `cron_remove` 三个工具，agent 直接调用改 `tasks.yaml`
- **Telegram**：如果配了 `MOMS_TG_TOKEN`（或 `/telegram` 配置）启用了 modu_code 自带的 Telegram bot,同一份工具在那边一样能用——不需要 cron 自己再起一个 bot

daemon 运行中会自动热加载新配置（fsnotify + SIGHUP）；跨进程并发写 `tasks.yaml` 由 `<config>.lock` 上的 flock + 原子写（temp+rename）保护，daemon 和这类 session 同时改也不会写坏文件。

## Agent 工具集

daemon 跑任务时（或 `run <id>`），任务自己的 agent 也会获得这 3 个管理 cron 任务表的工具（用于自我调度类的 prompt,例如"看看有没有该建的任务"）：

| 工具 | 作用 |
|------|------|
| `cron_add` | 加新任务（参数：id / cron / prompt / enabled / on_overlap / channels）|
| `cron_list` | 列出当前所有任务和已配置通知 channel |
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

或者用 `run bootstrap` 触发一次，agent 调工具改 `tasks.yaml`；daemon 运行中会自动热加载新配置。

> `add` / `rm` 会用 `yaml.Marshal` 重写 `tasks.yaml`，**用户在 YAML 里写的任务注释会丢失**。

### daemon 热加载

daemon 跑起来后无需重启就能感知 config/tasks 变化：

- **fsnotify**：监听 `config.yaml` 的父目录，`config.yaml` 或 `tasks.yaml` 写入事件触发 reload（300ms debounce 合并连续事件）
- **SIGHUP**：`kill -HUP <pid>`，作为手动 fallback
- **失败回滚**：reload 时如果新 config 解析失败或包含非法 cron 表达式，**保留旧调度器继续跑**，仅打一条 warning。错配置不会让 daemon 挂
- **in-flight 任务**：旧调度器 Stop 后不再触发，但正在执行的 agent 任务**继续跑完**；新调度器立即接管下一次触发

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
    prompt: "say hello"
    enabled: true
    on_overlap: skip          # skip | queue | kill, 默认 skip
    channels: [ops-webhook]   # 完成后推送到这些 channel
```

老的单文件 `config.yaml` 内联 `tasks:` 仍可读；一旦通过 `add` / `rm` / Telegram 管理任务，新任务表会写入 `tasks_file`。

### 完成通知 `channels`

任务可以配置 `channel: <name>` 或 `channels: [<name>, ...]`。每次运行结束后（成功或失败都会触发），`modu_cron` 会按任务引用的 channel 发送完成通知，内容包含任务 ID、状态、耗时、日志路径，以及日志中最后一段 assistant 文本。

当前支持的出站类型：

| type | 必填配置 | 行为 |
|------|----------|------|
| `webhook` | `url` 或 `url_env` | POST JSON payload 到指定 URL |
| `telegram` | `token`/`token_env` + `chat_id`/`chat_id_env` | 调 Telegram Bot API `sendMessage` |
| `feishu_webhook` | `url` 或 `url_env` | 调飞书/Lark 自定义机器人 webhook |
| `feishu_bot` | `chat_id`/`chat_id_env`;`app_id`+`app_secret`(或 `*_env`)可省略 | 以飞书应用机器人身份发消息;凭据留空时自动复用 `~/.modu/channels/feishu/config.toml` |

`url` / `token` / `chat_id` 支持 `${ENV}` 展开；更推荐用 `*_env` 字段避免把密钥写进 YAML。daemon 热加载任务时也会读取最新 channel 配置；通知失败只记录 warning，不会覆盖本次任务运行结果。`run <id>` 调试时如果任务本身成功但通知失败，会返回通知错误，方便本地验收。

> 这里的 `type: telegram` channel 是**出站**通知（任务跑完推一条消息）。用 Telegram **入站**发自然语言管理任务，见前面「自然语言管理」——直接用的是 modu_code 自己的 Telegram bot,不是 cron 单独起的。

### 三档 cap(断路器)

任务级三档 + 全局日额度，在 loop 第一次自己跑之前装好（Cap Before You Ship）：

```yaml
tasks:
  - id: morning-triage
    cron: "0 0 6 * * *"
    prompt: "/morning-triage"
    timeout: 45m              # per-run 时长上限，默认 30m；超时 cancel，run_end 记 status=timeout
    max_tokens_per_run: 500000 # per-run token 上限（input+output），超线立即 cancel，status=token_cap
    max_retries: 2            # 仅 status=error 时原地重试（指数退避 30s 起，上限 5m）；cap 触发不重试
```

`config.yaml` 全局项：

```yaml
daily_budget_tokens: 3000000  # 所有任务共享的当日 token 总额度（本地时区按天滚动）
```

- 日额度台账落在 `~/.modu_cron/logs/usage.json`，每次 run 结束累加，超额后当天后续 tick 直接拒跑（`run_end` 记 `status=budget_exceeded`，配置了 channel 会收到通知），第二天自动恢复
- `run_end` 现在带 `tokens` 字段（本次 run 消耗），`status` 可为 `ok` / `error` / `timeout` / `token_cap` / `budget_exceeded`
- timeout / token_cap / budget_exceeded 属于断路器：**不会**触发 `max_retries` 重试
- `timeout` / `max_tokens_per_run` / `max_retries` 配置非法时 daemon reload 会失败回滚（与非法 cron 表达式同一套保护）

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
~/.modu_cron/logs/<task_id>/<local-RFC3339-timestamp-with-ns>.log
```

文件名和日志里的 `started_at` / `ended_at` 使用本地时区；文件名里的 `:` 会替换成 `-`。落盘前 `runner` 已经把 `coding_agent` 的完整事件流过滤成**精简版**——只保留 7 种关键事件，每行一个 JSON 对象：

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
modu_code cron logs <id>                   # 列出该任务所有 run, 最新在上
modu_code cron logs <id> --tail            # 解码最近一次为可读文本
modu_code cron logs <id> --tail --json     # 同上但输出原 NDJSON（程序友好）
modu_code cron logs <id> --file <name>     # 看指定文件 (从 list 拷文件名)
```

`--json` 直接 cat 文件，可以喂给 `jq` 之类工具做后续处理。

## Extensions(goal / verifier / workflow)

每个 tick 的 session 会加载 builtin 扩展(读 `~/.modu/extensions.yaml`,与 modu_code 同一份;文件是增量覆盖语义)。这意味着 cron 任务的 prompt 里可以 `create_goal` 长跑,并由 goal verifier(若在 extensions.yaml 开启)对"完成"做独立裁判。extensions.yaml 解析失败时降级为无扩展继续跑(打 warning),三档 cap 仍然生效。

反过来,modu_code 会话里也有 `cron_add` / `cron_list` / `cron_remove` 工具(builtin `cron` 扩展),可以在交互会话里用自然语言管理这里的任务表;daemon 热加载会自动接住改动。跨进程写入由 `<config>.lock` 上的 flock + 原子写(temp+rename)保护。

## 目录结构

```
pkg/cron/                   # 全部实现,入口是 modu_code 的 cron 子命令
├── cli/                    # daemon/init/list/logs/rm/run + root.go 统一 dispatch
├── config/                 # YAML 加载/保存 + Task 模型(原子写)
├── crontools/              # cron_add / cron_list / cron_remove agent 工具(flock)
├── notify/                 # 任务完成后的 channel 推送
├── runlog/                 # 任务日志文件 store + 按天 usage 台账
├── runner/                 # CodingSession 装配的 Runner + 三档 cap + extensions
├── scheduler/              # robfig/cron 封装 + 并发策略
├── config.example.yaml
└── README.md
```

模型/provider 解析没有独立的包——直接用 `pkg/provider`(modu_code 自己的那份),不重复维护。自然语言建任务/查/删也没有独立的 CLI 或 Telegram 轮询实现——直接是 `pkg/coding_agent/plugins/extension/cron` 这个 builtin 扩展,挂进任何 modu_code session。

## 业务开发路线

1. ✅ `Runner` 接 `CodingSession`, prompt 真跑起来，事件流落任务日志
2. ✅ `logs <id>` 子命令: 列出 / tail / 指定文件 / NDJSON 原文
3. ✅ `rm` 子命令: 交互式编辑 + 写回 YAML（daemon 自动热加载）
4. ✅ `run <id>` 子命令: 不等到点，立即跑一次
5. ✅ agent 工具集 `cron_add` / `cron_list` / `cron_remove`，通过 builtin `cron` 扩展挂进任意 modu_code session(交互式或 Telegram),自然语言管理任务
6. ✅ daemon 热加载（SIGHUP + fsnotify，失败回滚保留旧调度器）
