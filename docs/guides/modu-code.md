# modu_code 使用指南

`modu_code` 是终端里的 AI 编程助手，能在当前工作目录中读写文件、搜索代码并执行命令。默认的 Bubble Tea inline TUI 会把已完成的对话保留在终端 scrollback 中，方便选中和复制。

它构建在 `pkg/coding_agent` 引擎之上，除了基本的 REPL 对话，还带长程目标（goal）、子代理（subagent）、动态工作流（workflow）、定时任务（cron）和 MCP 工具接入。本文档只讲命令行和 TUI 这一层；引擎内部机制（ReAct 循环、工具、hook、上下文压缩、MCP 配置格式等）见 [`pkg/coding_agent/README.md`](../../pkg/coding_agent/README.md)。

本文覆盖安装、运行模式、模型配置、TUI 操作和内置扩展。引擎内部机制不在本文展开。

## 文档边界

本文只描述 `cmd/modu_code` 的启动和交互行为。Provider 协议、Agent 循环、工具实现、MCP schema 等内部机制以 [`pkg/coding_agent`](../../pkg/coding_agent/README.md) 及源码为准。

命令和扩展会随配置变化：命令行参数以 `modu_code -h` 为准，TUI 中实际可用的斜杠命令以 `/help` 和 `/` 选择器为准。

---

## 安装与构建

需要 Go（版本见 `go.mod`）。直接从源码跑：

```bash
go run ./cmd/modu_code
```

或编译成单个二进制放进 `PATH`：

```bash
go build -o modu_code ./cmd/modu_code
./modu_code
```

下文命令用 `go run ./cmd/modu_code` 和 `modu_code` 两种写法，等价。

---

## 快速开始：零配置起步

最快的路径是设一个环境变量，不写任何配置文件。`modu_code` 按下面的顺序选 provider，第一个命中的生效：

```bash
ANTHROPIC_API_KEY   # Anthropic（走 OpenAI 兼容端点）
OPENAI_API_KEY      # OpenAI，模型取 $OPENAI_MODEL，默认 gpt-4o
DEEPSEEK_API_KEY    # DeepSeek，模型取 $DEEPSEEK_MODEL，默认 deepseek-chat
OLLAMA_HOST         # Ollama，模型取 $OLLAMA_MODEL（必填）
```

例如：

```bash
export DEEPSEEK_API_KEY=sk-xxx
go run ./cmd/modu_code
```

辅助环境变量：`OPENAI_BASE_URL` 覆盖 OpenAI 兼容源的 base URL，`THINKING_LEVEL` 设推理档位（`off|low|medium|high`，默认 `off`），`MOMS_TG_TOKEN` 启动 Telegram bot。

没有任何 provider 时也能进 TUI：启动会提示用 `/config` 现场配置 provider、API key 和模型。要走配置文件（多模型、role、reasoning 等），见下一节。

默认启动会创建新的 session id，不会自动带入同一路径上一次的对话上下文。需要继续旧 session 时用退出提示里的 id：

```bash
go run ./cmd/modu_code --resume <session-id>
```

交互 TUI 正常退出后会打印当前 session id 和可复制的 `modu_code --resume <session-id>` 命令；即使还没发过消息，退出时也会落盘一个可恢复的空 session。

---

## 运行模式与命令行参数

`modu_code` 有四种运行模式。不带 `-p`/`--rpc`/`--acp` 时是默认的交互 TUI，其余三种是非交互模式，供脚本或编辑器集成使用。

| 参数 | 说明 |
|------|------|
| （无） | 交互 TUI（默认） |
| `-p "<prompt>"` | print 模式：发送一条 prompt，把结果输出到 stdout 后退出 |
| `--json` | 配合 `-p`：输出 NDJSON 事件流而非纯文本 |
| `--rpc` | RPC 模式：stdin/stdout 上的 JSON-line 协议 |
| `--acp` | ACP stdio server：JSON-RPC 2.0 LDJSON（供 Zed 等 ACP 客户端接入） |
| `--no-approve` | 跳过工具执行的人工确认，自动放行全部工具（print/rpc/acp 常用） |
| `--resume <id>` | 恢复已保存的 session（完整 id 或唯一前缀均可） |
| `--worktree` | 在隔离的 git worktree 中启动（见下） |

print 模式的典型用法：

```bash
go run ./cmd/modu_code -p "总结 cmd/modu_code 的职责" --no-approve
```

非交互模式在没有配置 provider 时会直接报错退出，而不是进入配置引导——脚本场景需要先配好模型。

### worktree 隔离

默认启动使用当前 checkout。需要隔离修改时，显式创建并切入 managed worktree，路径形如 `~/.modu/worktrees/<uuid>/<repo>`，分支名形如 `modu-code/<repo>-<id>`：

```bash
go run ./cmd/modu_code --worktree
```

不在 git 仓库里，或 worktree 模式被禁用时，`--worktree` 会被安全忽略、退回当前目录。运行中也可以用 `/worktree` 查看状态、diff、列表和 cleanup。

---

## 模型配置

`modu_code` 优先读取 `~/.modu/config.toml` 中的模型配置。支持配置多个模型，通过 `active` 指定默认模型：

```toml
version = 2
active = "local-qwen"
scopedModels = ["local-qwen", "deepseek"]

[roles]
summary = "local-qwen"
dispatcher = "deepseek"

[reasoning]
level = "off"

[providers.lmstudio]
type = "openai-compatible"
baseUrl = "http://127.0.0.1:1234/v1"
apiKey = "lm-studio"

[providers.deepseek]
type = "openai-compatible"
baseUrl = "https://api.deepseek.com/v1"
apiKeyEnv = "DEEPSEEK_API_KEY"

[[models]]
name = "local-qwen"
description = "local coding model"
provider = "lmstudio"
model = "qwen/qwen3.6-35b-a3b"
capabilities = ["tools"]
contextWindow = 262144

[[models]]
name = "deepseek"
description = "remote fallback model"
provider = "deepseek"
model = "deepseek-chat"
capabilities = ["tools"]
contextWindow = 1000000
```

`providers` 只描述连接方式，`models` 只描述可选模型；`active` 是默认模型，`scopedModels` 是模型循环范围，`roles` 预留给 summary/dispatcher 等专用模型。`contextWindow` 可显式覆盖模型上下文窗口；未配置时，内置厂商会按当前厂商最大窗口补默认值。

运行中输入 `/model` 打开模型选择器：方向键选择、`Enter` 确认、`Esc`
取消。`/model list` 列出模型，`/model <name>` 或
`/model <provider> <model-id>` 也可以直接切换。切换后写回 `active`，下次
启动继续使用；如果实际切到了另一个模型，会清空旧对话上下文并明确提示。

配置辅助命令（非交互）：

```bash
go run ./cmd/modu_code config              # 显示当前配置
go run ./cmd/modu_code config init         # 生成示例配置
go run ./cmd/modu_code config init --force # 覆盖已有配置
```

TUI 中输入 `/config` 打开 provider 配置流程，可选 DeepSeek、LMStudio、
Ollama 或 `Custom OpenAI-Compatible`，再统一填写密钥方式和 base URL；
密钥输入不会进入 transcript。查看或切换模型使用 `/model`。这些流程与
`/channel` 共用同一种 choice/text Flow 数据，不再各自维护输入状态机。

---

## 能力概览

除了基础对话，`modu_code` 默认注册了下面几个引擎扩展。这里只给入口，机制细节见 [`pkg/coding_agent/README.md`](../../pkg/coding_agent/README.md)。

- **长程目标（goal）**：`/goal` 设置/查看/暂停/恢复/清除一个跨多轮持续的目标，带 token 预算；子代理消耗的 token 会计入当前 goal 预算。
- **子代理（subagent）**：`/run <agent> [task]` 跑单个子代理，`/parallel` 并发、`/chain` 串行编排多个子代理，`/subagents-doctor` 查看配置诊断。子代理定义放在 `~/.modu/agents/`。
- **动态工作流（workflow）**：模型可以写 JavaScript 脚本调用 `workflow` 工具做 fan-out / fan-in 编排。`/workflows` 是管理面板（list/show/pause/stop/resume/restart/save 等），保存过的工作流以 `/workflow:<name>` 暴露，`/deep-research <question>` 是内置的多阶段联网研究工作流。可用 `MODU_CODE_DISABLE_WORKFLOWS=1` 或 `/config` 关闭。
- **定时任务（cron）**：调度器内嵌在 TUI 进程里运行，无独立 daemon。`/cron add <request>` / `/cron list` / `/cron rm <uuid>` / `/cron update <request>` 管理任务，配置在 `~/.modu/cron/`，日志写 `~/.modu/cron/daemon.log`。
- **MCP 工具**：配置的 stdio 和 Streamable HTTP MCP server 会接入每个新 session，工具对主代理可用，`required` server 启动失败会阻止 session 创建。配置格式（`~/.modu/config.toml` 的 `[mcp_servers.*]` 与项目级 `.coding_agent/settings.json` 的 `mcpServers`）见引擎文档。

哪些扩展启用由 `~/.modu/extensions.yaml` 决定；文件不存在时默认启用全部内置扩展。

---

## 运行检查

- `/context`：查看当前 prompt/context 来源摘要——当前模型、工作目录、会话消息数、系统 prompt 大小、memory 是否为空、计划模式、worktree 状态、项目上下文文件、已发现 skills、prompt templates 和本地资源包。
- `/doctor`：查看基础运行诊断——模型配置路径、当前模型、baseURL 连通性、provider 是否注册、API key 状态、上下文文件数量、发现的问题，以及接入的 MCP server 数。

两个命令都只读，不改动 session 状态。

---

## 配置与数据目录

`modu_code` 的运行时根目录是 `~/.modu/`：

- `config.toml` — 模型/provider 配置，根级 `[mcp_servers.*]` MCP 配置，`[settings]`
- `extensions.yaml` — 启用哪些内置扩展
- `settings.json` — 全局 harness 设置
- `sessions/<project>/` — 按项目路径分目录的会话历史
- `agents/` — 子代理定义
- `skills/` `prompts/` `packages/` — 资源系统
- `memory/` — 持久 memory
- `worktrees/` — managed worktree
- `cron/` — cron 配置（`config.yaml`、`tasks.yaml`）与日志（`daemon.log`、`logs/`）
- `channels/telegram/`、`channels/feishu/` — 渠道凭据与调试日志

项目级配置放在仓库内的 `.coding_agent/settings.json`，按名称覆盖全局配置。

---

## 键盘快捷键

| 按键 | 说明 |
|------|------|
| `Enter` | 提交消息；任务运行中提交为 follow-up 队列 |
| `Shift+Enter` | 任务运行中 steer 当前任务，打断当前轮并切到新指令 |
| `Ctrl+V` | 从系统剪贴板附加图片 |
| `ctrl+c` | 输入框有内容时清空输入；空输入时中断当前请求 / 退出 |
| `ctrl+d` | 退出（输入框为空时） |
| `ctrl+l` | 清屏 |
| `ctrl+o` | 切换工具调用展开模式 |
| `esc` | 中断当前请求 / 返回输入 |
| `Home` / `End` | 输入行首 / 行尾 |
| `ctrl+w` | 删除光标前一个词 |
| `ctrl+j` | 在输入框插入换行 |

输入 `/` 打开轻量命令选择器：方向键选择，`Tab` 补全，`Enter` 执行。

### 图片输入

按 `Ctrl+V` 读取系统剪贴板中的图片。不要用 `Command+V`：终端会把它当作普通文本粘贴。也可以把图片文件拖进终端，或粘贴一个只包含图片路径的字符串。输入框用 `[Image #1]`、`[Image #2]` 表示附件，不在终端内渲染缩略图。

图片附件和文字共用光标编辑。将光标移到附件标记后按 `Backspace`，或移到标记前按 `Delete`，即可移除对应图片。允许只提交图片，也允许在任务运行时把图片作为 follow-up 或 steer 消息提交。

支持 PNG、JPEG、GIF、WebP，单张上限 5 MB。Linux 的剪贴板读取依赖 `wl-paste` 或 `xclip`；图片路径拖入不需要这两个程序。当前模型显式声明只接受文本，或配置启用了 `blockImages` 时，提交会返回错误。

图片内容以 base64 写入 session，因此恢复会话时不要求原始图片文件仍然存在。OpenAI-compatible、Anthropic 和 Gemini provider 会分别转换为各自的多模态请求格式。

任务运行中继续输入普通消息并按 Enter，会把消息加入 follow-up 队列，在当前任务结束后自动执行。运行中按 Shift+Enter，或输入 `/steer <message>` / `/s <message>`，把消息加入 steer 队列并中断当前轮，随后按新方向继续。也可以 `/followup <message>` / `/f <message>` 显式排队下一条 follow-up。`/queue` 查看等待队列。

输入 `!cmd` 在当前工作目录执行 shell 命令，把输出显示在 TUI 中并作为下一条用户消息发送给模型；`!!cmd` 只执行并显示，不发送给模型。

输入 `/tool-output <call-id>` 从当前 session 的工具结果 artifact 读取完整本地输出；没有 artifact 时回退显示模型看到的 preview。

### SSH 与移动端

SSH 环境默认保留终端 mouse reporting，滚轮和拖拽选择可直接用。若 JuiceSSH 等移动端客户端在触摸滚动或键盘尺寸变化时产生大量 mouse 事件导致界面卡住，可显式关闭：

```bash
MODU_TUI_MOUSE=off modu_code
```

SSH 下拖选复制会发 OSC52；在 tmux/screen 中走 passthrough 更新本机剪贴板。OSC52 写入没有回执，被终端或 multiplexer 拦掉时（tmux 未开 `allow-passthrough`、终端不支持/未授权 OSC52）复制会静默丢失，所以远程复制后状态栏会附带兜底提示：按住 Shift 拖选可绕过 mouse reporting 走终端原生选择（macOS Terminal.app 用 Fn、iTerm2 用 Option），选中后用终端自己的复制快捷键。也可 `MODU_TUI_MOUSE=off` 彻底交还鼠标。

SSH 兼容模式下，输入框为空且没有输入历史可选时，Up/Down 滚动对话内容，适配移动端把滑动手势转成方向键的行为；有输入历史时 Up/Down 优先切换历史输入。

---

## 渠道：Telegram 与 Feishu

在 TUI 输入 `/channel`，选 Telegram 或 Feishu 后按提示输入凭据。Token 和 App Secret 用隐藏输入，不进入会话历史。配置保存后重启 `modu_code` 生效。渠道和 TUI 共用同一个 steer / follow-up 队列。

三处输入语义一致：

| 输入 | 任务空闲时 | 任务运行中 |
|------|------|------|
| 普通消息 | 作为新 prompt 执行 | 加入 follow-up 队列 |
| `/followup <msg>` / `/f <msg>` | 提示当前没有 active task | 加入 follow-up 队列 |
| `/steer <msg>` / `/s <msg>` | 提示当前没有 active task | 加入 steer 队列并中断当前轮 |

### Feishu

配置 `MODU_FEISHU_APP_ID` / `MODU_FEISHU_APP_SECRET`，或写入 `~/.modu/channels/feishu/config.toml`：

```toml
appID = "cli_xxx"
appSecret = "xxx"
chatIDs = ["oc_xxx"] # 可选；为空时接收所有已授权会话
```

等价的环境变量：

```bash
MODU_FEISHU_APP_ID=cli_xxx
MODU_FEISHU_APP_SECRET=xxx
MODU_FEISHU_CHAT_IDS=oc_xxx,oc_yyy
```

飞书回复以富文本（`post`）渲染 Markdown，接收到的每条消息会先回一个“灵光一闪”表情作为回执。运行诊断写入 `~/.modu/channels/feishu/debug.log`，不进入 TUI。

### Telegram

配置 `MOMS_TG_TOKEN` 或 `~/.modu/channels/telegram/config.toml`：

```toml
token = "123456:bot-token"
```

---

## 斜杠命令

引擎内置命令（`pkg/coding_agent`）和扩展/TUI 命令合并后可用。常用清单：

| 命令 | 说明 |
|------|------|
| `/model [query]` | 打开带搜索的模型选择器 |
| `/scoped-models [list\|set\|add\|remove\|clear\|edit]` | 配置模型循环范围 |
| `/config` | 打开模型/provider 配置页面 |
| `/effort [off\|low\|medium\|high\|ultracode]` | 设置推理/努力档位；`ultracode` 需 workflow 启用且模型支持 xhigh |
| `/channel` | 交互配置 Telegram 或 Feishu |
| `/context` | 查看当前 prompt/context 来源 |
| `/doctor` | 查看基础运行诊断 |
| `/tools` | 列出当前生效的工具 |
| `/plan` | 计划模式相关 |
| `/compact` | 手动触发上下文压缩 |
| `/tokens` | 查看 token 用量 |
| `/worktree` | 查看 worktree 状态、diff、列表和 cleanup |
| `/retry` | 重试上一条失败的 prompt |
| `/steer <msg>` / `/s <msg>` | 打断当前轮并按新消息继续（`/s` 供无法识别 Shift+Enter 时用） |
| `/followup <msg>` / `/f <msg>` | 把消息排到当前任务之后执行 |
| `/queue [clear [steer\|followup]\|drop]` | 查看/清空/删除等待队列 |
| `/goal` | 设置/查看/暂停/恢复/清除持久目标 |
| `/run <agent> [task]` | 运行单个子代理 |
| `/parallel <agent> <task> -> ...` | 并发运行多个子代理 |
| `/chain <agent> <task> -> ...` | 串行运行多个子代理 |
| `/subagents-doctor` | 子代理配置诊断 |
| `/workflows [...]` | 工作流管理面板 |
| `/workflow:<name> [json-args]` | 运行已保存的工作流 |
| `/deep-research <question>` | 运行内置多阶段研究工作流 |
| `/cron [add\|list\|rm\|update]` | 管理定时任务 |
| `/hotkeys` | 查看快捷键 |
| `/reload` | 重新加载 skills、prompts、context（keybindings 除外） |
| `/new` / `/clear` | 清空当前会话上下文 |
| `/session` | 查看会话摘要（id、名称、cwd、模型、消息数、tokens、plan/worktree、资源） |
| `/name <name>` | 设置会话名称 |
| `/sessions [all]` | 列出当前项目或全部项目的会话 |
| `/resume <file>` | 切换到指定会话文件 |
| `/session delete <file>` | 删除非当前会话文件 |
| `/fork-session <file>` | 从已有会话复制一份到当前项目 |
| `/fork <entry-id>` | 从历史位置 fork |
| `/clone` | 从当前 session leaf 克隆一份会话 |
| `/tree` | 显示 session tree 摘要 |
| `/export [file]` | 导出当前 session 为 HTML |
| `/copy` | 复制最后一条 assistant 回复到系统剪贴板 |
| `/changelog` | 显示当前 git 仓库最近提交 |
| `/skills` | 列出已发现 skills |
| `/prompts` | 列出已发现 prompt templates |
| `/settings` | 显示 Bubble Tea 迁移状态 |

命令列表随启用的扩展变化，以 `/help` 或 `/` 选择器实际显示为准。

---

## 状态说明

运行状态显示在聊天输入框上方：当前轮次只显示简短状态；轮次结束保留最近一轮完成耗时，超过 60 秒时显示 `min`。输入框下方保留简短上下文用量、模型和工作区路径。任务进行中时，输入框上方还会显示本轮收到的活跃 todo 卡片；空闲、已完成、空列表、全完成或仅有上一轮遗留 todo 时隐藏。

自动或手动 context compaction 完成时，TUI 会在 transcript 中持久插入 `------------- context compact ------------------` 分隔线；恢复 session 时也会按已保存的 compaction entry 回放该分隔线。

| 区域 | 内容 |
|------|------|
| 输入框上方 | 当前运行状态或最近一轮完成耗时 |
| 输入框与状态之间 | 活跃 todo 卡片（运行中且本轮收到未完成 todo 更新时渲染） |
| 输入框下方 | `ctx used/window · model · …/workspace` |

---

## 工具渲染

TUI 中工具调用按类型采用不同的展开/折叠样式：

- **Read**：折叠为 `Read(path · lines x-y)`，展开显示带行号的文件内容
- **Write**：默认展开显示 `Write path` 或 `Update path`（目标已存在时），展开显示带行号的代码块 + 写入/变更行数；写已有文件时预览为上下文 diff 并渲染 `diff` 语法高亮
- **Edit**：默认展开显示 `Edit path` 或 `Update path`，展开显示语法高亮的上下文 diff 代码块（含邻近上下文行）
- **Bash**：折叠为 `Ran N shell command(s)`，展开显示完整命令输出
- **Grep/Find/Ls**：紧凑单行摘要，展开显示完整搜索结果

Bubble Tea 全屏 TUI 保留为实验路径；默认交互路径用 Bubble Tea inline runtime，优先保证 scrollback 和终端文本选择体验。

## 验收

修改命令行行为或本文参数后，至少运行：

```bash
go run ./cmd/modu_code -h
go test ./cmd/modu_code
```

TUI、审批、会话恢复、worktree 或渠道行为无法只靠帮助输出验收。对应变更还需要运行相关单元测试，并在终端中手工走通本文描述的路径。
