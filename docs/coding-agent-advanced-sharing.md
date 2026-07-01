# modu code 深度分享：一个 Coding Agent 如何从命令行变成工程化运行时

这份分享不是介绍“怎么调一个模型”，也不是罗列 modu code 有哪些模块。真正要讲清楚的是：

> modu code 为什么不是一个带工具的聊天程序，而是一个面向真实代码修改的 agent runtime。

理解 modu code，只需要抓住一条主线：

```text
用户输入
  -> TUI/CLI 宿主判断这是什么操作
  -> CodingSession 准备上下文、工具、权限和会话状态
  -> pkg/agent 执行 ReAct loop
  -> 工具调用被审批、执行、渲染、持久化
  -> 结果回到 TUI，并写入可恢复 session
```

这条链路讲通了，`modu_code` 的设计基本就讲通了。

---

## 1. 分享开场：先纠正一个误解

很多人理解 coding agent，会先想到这样的实现：

```text
读一行用户输入
拼一个 system prompt
调用大模型
如果模型要工具，就执行工具
把结果打印出来
```

这个实现能做 demo，但不能长期写代码。真实使用时马上会遇到问题：

- 模型改文件前是否读过最新版？
- 用户中途发现方向错了，能不能纠偏？
- bash 命令危险时谁来批准？
- 上下文爆了怎么办？
- 会话退出后怎么恢复？
- 一次长任务的状态怎么展示？
- 修改是否能放进隔离 worktree？
- 扩展能力是不是每次都要改主循环？

modu code 的答案是：把 coding agent 做成一个运行时，而不是一段调用模型的脚本。

可以用三层结构概括：

```text
┌──────────────────────────────────────────────┐
│ cmd/modu_code                                │
│ CLI / TUI / 配置 / 快捷键 / 用户审批 / 展示    │
└───────────────────────┬──────────────────────┘
                        │
┌───────────────────────▼──────────────────────┐
│ pkg/coding_agent                             │
│ session / context / tools / approval /        │
│ memory / worktree / extensions / runtime      │
└───────────────────────┬──────────────────────┘
                        │
┌───────────────────────▼──────────────────────┐
│ pkg/agent                                    │
│ 最小 ReAct loop：LLM -> tool -> result -> LLM │
└──────────────────────────────────────────────┘
```

分享时先把这三层讲清楚，后面所有细节都能挂上去。

---

## 2. 第一段主线：`main.go` 只负责把产品拉起来

源码入口：[cmd/modu_code/main.go](../cmd/modu_code/main.go)

`main.go` 的职责不是跑 agent 逻辑，而是完成宿主初始化：

1. 解析命令行模式。
2. 解析模型、provider、API key。
3. 加载内置扩展。
4. 构造 `CodingSessionOptions`。
5. 创建 `CodingSession`。
6. 根据模式进入 print、RPC 或 TUI。
7. 交互退出后打印 resume 命令。

核心链路可以这样讲：

```text
provider.Resolve()
  -> extension.LoadEnabled()
  -> coding_agent.NewCodingSession(...)
  -> runModuTUI(...)
```

这里有两个值得强调的设计点。

第一，模型配置属于宿主层。

`main.go` 通过 `cmd/modu_code/internal/provider` 得到当前模型和 `getAPIKey` 回调，然后把它们传给 `CodingSessionOptions`。这样 `pkg/coding_agent` 不需要知道配置文件命令、初始化 wizard、环境变量兜底这些产品细节。

第二，TUI 模式会设置 `DeferStartupEvent`。

这是一个很工程化的细节：扩展可能在 session 启动时注入隐藏 follow-up，例如 goal 自动继续。如果 TUI 还没完成订阅和前台任务驱动，这些任务会在后台跑，用户看不到状态，也不好中断。所以交互模式要延迟 startup event，等 TUI wiring 完成后再发。

这一段的分享目标是让听众明白：

> `cmd/modu_code` 是宿主，不是 agent 内核。它负责把用户、终端、配置和 session engine 接起来。

---

## 3. 第二段主线：TUI 不是壳，它是前台任务调度器

源码入口：[cmd/modu_code/modu_tui_runner.go](../cmd/modu_code/modu_tui_runner.go)

如果只是做一个 REPL，TUI 只需要：

```text
read line -> session.Prompt(line) -> print result
```

但 modu code 的 TUI 复杂得多，因为真实 agent 运行中需要被控制。

`runModuTUI` 维护了几类状态：

- 当前 prompt 的 `cancel`。
- 当前 prompt id。
- 前台运行计数。
- cancel 后是否继续处理 queued message。
- workflow panel 状态。
- Bubble Tea program 的 send 函数。

核心执行器是 `runAgentLoop`：

```text
mark foreground running
set TUI busy/running
create cancellable context
run session.Prompt / session.Continue
if queued messages remain, continue
render completed / interrupted / error
clear busy when foreground runs done
```

它解决的不是显示问题，而是控制问题：

- 用户按 `Esc`，能取消当前 context。
- bash 正在跑，能 `AbortBash`。
- 用户运行中继续输入，消息不会丢。
- steer 可以改变当前任务方向。
- follow-up 可以排到当前任务之后继续。
- hidden extension turn 可以走前台 loop，状态栏可见，也能中断。

这就是为什么 modu code 的 TUI 不是“外壳”。它其实是前台任务调度器。

可以在分享里用这个对比：

```text
普通 REPL：
  用户输入 -> 阻塞等待 -> 输出

modu TUI：
  用户输入 -> 分类调度 -> 可中断运行 -> 事件流渲染 -> 队列续跑
```

---

## 4. 第三段主线：`CodingSession` 是整个系统的装配中心

源码入口：[pkg/coding_agent/coding_agent.go](../pkg/coding_agent/coding_agent.go)

`NewCodingSession` 是理解 modu code 最关键的函数。它不是简单 new 一个 agent，而是把一整个 coding runtime 组装出来。

它大致做这些事：

```text
校验 cwd 和 model
确定 agentDir
加载配置
初始化 resource loader
初始化 memory store
创建 tool provider 和 active tools
创建 session manager
发现 skills / prompt templates / context files
构造 system prompt builder
创建底层 agent.Agent
创建 context manager
创建 approval manager
创建 plan / worktree / todo / bgtask 等服务
初始化 extension runner
注册命令和扩展能力
写 runtime state
```

这里的关键不是“功能多”，而是分层边界：

- `engine`：持有 session 的真实运行状态。
- `CodingSession`：对宿主暴露的 facade。
- services：上下文、审批、worktree、memory、session 等独立状态能力。
- tools：模型真正能调用的行动面。
- extensions：运行时可插拔能力。

`pkg/coding_agent/ARCHITECTURE.md` 里把它定义成 L0-L5 分层。分享时不用逐层背，但要讲出一个核心原则：

> 会让 engine 跑一轮 agent 的东西，属于 runtime；只是为了 CLI/TUI/RPC 访问 runtime 的东西，属于 host API。

这能解释为什么 `CodingSession` 看起来很厚：它不是一个简单 SDK client，而是一个编码任务运行现场。

---

## 5. 第四段主线：`pkg/agent` 故意做得很薄

源码入口：

- [pkg/agent/loop.go](../pkg/agent/loop.go)
- [pkg/agent/agent.go](../pkg/agent/agent.go)

`pkg/agent` 是底层 ReAct 内核。它只做一件事：让模型和工具循环起来。

`Loop.Run` 的逻辑可以压缩成：

```text
append user prompt
while true:
  apply steering messages
  call LLM
  append assistant message
  if no tool call:
    run follow-up if any
    otherwise finish
  execute tool calls
  append tool result messages
```

它不知道什么是：

- TUI
- session JSONL
- worktree
- memory
- slash command
- dangerous bash
- context compaction
- tool diff rendering

这些都在上层。

这是非常重要的架构取舍。很多 agent 项目会把 loop 写成巨大的业务状态机，最后所有能力都往里塞。modu code 反过来做：loop 只暴露 hook 和 event。

关键接口包括：

- `types.LLM`
- `types.Tools`
- `types.RuntimeHooks`
- `types.EventSink`

所以 tool approval、steering、follow-up、max-step resume 都能通过 hook 接入，而不是污染 loop。

这一段可以讲成一个原则：

> agent loop 越薄，上层产品能力越容易演进。

---

## 6. 第五段主线：工具不是函数，是受控行动面

源码入口：

- [pkg/coding_agent/tools](../pkg/coding_agent/tools)
- [pkg/coding_agent/services/approval](../pkg/coding_agent/services/approval)
- [pkg/coding_agent/tool_gate.go](../pkg/coding_agent/tool_gate.go)

coding agent 和聊天机器人最大的区别是：它会行动。

modu code 默认把行动面收敛到几个核心工具：

- `read`：读文件。
- `edit`：精确局部替换。
- `write`：创建或完整写入。
- `bash`：运行测试、构建、git、脚本。
- `get_context_remaining`：查询上下文预算。

搜索和列目录能力可以存在，但默认面不必无限扩张。工具面越大，越需要权限、上下文和展示治理。

这部分最值得讲三个保护机制。

### 6.1 ToolManager：工具必须能随 session 状态重绑

`CodingSessionOptions.ToolProvider` 允许替换工具来源。默认 provider 会根据：

- cwd
- feature gate
- memory store
- todo store
- plan/worktree 状态
- context remaining provider

构造工具。

为什么这很重要？因为 cwd 会变。进入 worktree 后，read/write/bash 必须绑定到隔离目录，而不是继续操作原目录。

### 6.2 FileReadState：防止模型脏写

文件修改必须建立在“模型读过当前版本”的基础上。

`FileReadState` 记录完整读取时的内容和修改时间。写入或编辑前，如果文件已经变化，就可以拒绝基于旧内容的修改。

这不是 UX 细节，而是 coding agent 的安全底线：

> 模型没有读过当前文件，就不该自信地改当前文件。

### 6.3 Approval：危险动作必须经过执行层

权限不能只靠 prompt 约束。`approval.Manager` 支持：

- allow / deny
- allow always / deny always
- allowTools / denyTools
- bash prefix 规则
- dangerous bash 识别
- observer 通知
- interactive callback

一个很好的细节是：交互式批准 bash 的 always decision 按具体命令缓存。批准 `go test ./...` 不等于批准所有 bash。

这说明 modu code 的工具哲学是：

> 让模型行动，但每一步行动都要可审计、可审批、可中断。

---

## 7. 第六段主线：上下文系统决定模型“为什么这么做”

源码入口：

- [pkg/coding_agent/services/systemprompt](../pkg/coding_agent/services/systemprompt)
- [pkg/coding_agent/services/contextmgr](../pkg/coding_agent/services/contextmgr)
- [pkg/coding_agent/services/compaction](../pkg/coding_agent/services/compaction)
- [pkg/coding_agent/context_info.go](../pkg/coding_agent/context_info.go)

很多 agent 项目把上下文理解成“多塞点资料”。modu code 里，上下文是一个受管理的系统。

`systemprompt.Builder` 负责把这些来源组装起来：

- 默认 coding agent prompt。
- active tools 描述。
- context files，例如项目里的 AGENTS.md。
- skills。
- memory。
- 当前工作目录、环境、模型信息。
- plan/worktree/workflow 等模式块。

它还设置预算，例如 context file 总大小、单文件大小、memory 大小。这样不会因为一个上下文文件过大，把系统 prompt 撑爆。

运行中由 `contextmgr.Manager` 处理窗口问题：

- 统计 token usage。
- 计算距离自动压缩还有多少 token。
- 清理 transient message。
- 触发 compaction。

compaction 也不是简单删历史。它会：

- 保留最近消息。
- 总结旧消息。
- 保留近期用户消息锚点。
- 提取读过和修改过的文件。
- 把 compaction entry 写入 session。

这解释了 `/context` 的价值。它不是调试命令，而是回答一个核心问题：

> 模型现在看到了什么？这些上下文从哪里来？为什么它会这样判断？

分享时可以把上下文系统总结为：

```text
systemprompt 决定初始可见世界
contextmgr 决定运行中窗口策略
compaction 决定长任务如何续命
/context 决定人能否理解模型视野
```

---

## 8. 第七段主线：session 把聊天变成工程现场

源码入口：

- [pkg/coding_agent/services/session](../pkg/coding_agent/services/session)
- [pkg/coding_agent/session_api.go](../pkg/coding_agent/session_api.go)
- [pkg/coding_agent/export_html.go](../pkg/coding_agent/export_html.go)

普通聊天记录只关心消息列表。modu code 的 session 关心的是工程现场。

session 用 append-only JSONL 保存：

- header
- message entry
- compaction entry
- runtime sidecar entry
- branch summary entry
- session info entry

几个关键概念：

- `leafID`：当前对话分支位置。
- `Append`：追加对话 entry，并移动 leaf。
- `AppendSidecar`：追加运行态 entry，但不移动 leaf。
- `Fork`：把当前 leaf 移到历史 entry。
- `Tree`：从 JSONL 还原可导航会话树。

这带来一组产品能力：

- `--resume`
- `/session`
- `/sessions`
- `/fork`
- `/clone`
- `/tree`
- `/export`

这部分可以讲一个对比：

```text
chat history:
  保存一串消息，方便继续聊天

modu session:
  保存一个可恢复、可分支、可回放、带运行态的工程现场
```

这也是为什么退出时打印 resume 命令很重要。它告诉用户：这次工作不是临时的，现场已经被保存。

---

## 9. 第八段主线：人类介入是核心能力，不是失败兜底

源码入口：

- [cmd/modu_code/modu_tui_runner.go](../cmd/modu_code/modu_tui_runner.go)
- [pkg/agent/agent.go](../pkg/agent/agent.go)
- [pkg/agent/loop.go](../pkg/agent/loop.go)

很多人会把 agent 的理想形态理解成“完全自动跑完”。真实编码任务不是这样。用户需要随时介入：

- 发现方向错了，马上纠偏。
- 看到危险命令，拒绝执行。
- 想追加约束，但不想打断当前工作。
- 任务太久，直接中断。

modu code 有三类介入机制：

```text
interrupt:
  取消当前 context，abort agent 和 bash

steer:
  运行中插入高优先级消息，改变当前方向

follow-up:
  当前任务结束后继续执行下一条用户消息
```

这三者分别解决不同问题：

- interrupt 是刹车。
- steer 是抢方向盘。
- follow-up 是排队追加任务。

`pkg/agent/loop.go` 在工具执行前后检查 steering，在无 tool call 结束时检查 follow-up。TUI 把按键和输入映射到这些队列。

这一段的结论是：

> 好的 coding agent 不应该把用户排除在运行过程之外，而应该让用户低成本介入。

---

## 10. 第九段主线：slash command 是确定性控制面

源码入口：[pkg/coding_agent/slash_commands.go](../pkg/coding_agent/slash_commands.go)

自然语言适合表达意图，但不适合所有操作。比如：

- 切模型。
- 手动压缩。
- 查看当前 session。
- 从历史 entry fork。
- 查看工具列表。
- 调整 thinking level。

这些操作需要确定性，不应该交给模型猜。

所以 modu code 有 slash command：

- `/model`
- `/compact`
- `/session`
- `/tree`
- `/fork`
- `/tools`
- `/thinking`
- `/effort`
- `/retry`

TUI 层还有更多产品命令，例如 `/context`、`/doctor`、`/worktree`、`/queue`、`/config`。

可以这样讲：

```text
自然语言：交给模型理解任务
slash command：直接控制宿主系统
```

这个边界很关键。一个成熟 agent 产品不能所有事情都让模型解释。

---

## 11. 第十段主线：扩展系统让主循环不膨胀

源码入口：[pkg/coding_agent/plugins/extension](../pkg/coding_agent/plugins/extension)

modu code 有 goal、subagent、workflow 等能力。如果这些能力都直接写进 agent loop，系统会迅速失控。

所以它们通过 extension API 接入。

扩展可以注册：

- tool
- slash command
- tool hook
- event handler

扩展也可以调用宿主能力：

- `SendMessage`
- `SendFollowUpMessage`
- `Notify`
- `Confirm`
- `Select`
- `BackgroundTasks`
- `InterruptBackgroundTask`
- `ForkSession`

这让几类长任务能力成为插件式能力：

- goal：长程目标推进。
- subagent：fork 子 session 处理任务。
- workflow：脚本化 fan-out/fan-in。

分享时不要把重点讲成“多 agent 很酷”。更准确的重点是：

> 扩展系统保护了主循环，让复杂能力长在 session 外围，而不是塞进 ReAct loop。

---

## 12. 第十一段主线：worktree 是 agent 修改代码的隔离边界

源码入口：[pkg/coding_agent/services/worktree](../pkg/coding_agent/services/worktree)

coding agent 直接改当前 checkout 有风险。modu code 提供 managed worktree：

```text
~/.modu/worktrees/<uuid>/<repo>
branch: modu-code/<repo>-<id>
```

进入 worktree 不是简单 `cd`。它要做一系列 session 级动作：

1. 创建 worktree 和分支。
2. 保存原始 cwd。
3. 调用 host 的 `SwitchCwd`。
4. 重新绑定工具 cwd。
5. 刷新 system prompt。
6. 发 session event。
7. 写 runtime state。

这说明 worktree 是 runtime 状态，不只是一个工具命令。因为模型看到的工作目录、工具执行目录、UI 显示目录和 session 状态都要一起变。

---

## 13. 第十二段主线：runtime state 让长任务可观测

源码入口：[pkg/coding_agent/runtime_paths.go](../pkg/coding_agent/runtime_paths.go)

coding agent 一旦有后台任务、workflow、subagent、worktree，只靠当前聊天消息是不够的。用户需要知道系统状态。

`RuntimePaths()` 定义了运行态目录：

- runtime dir
- runtime index file
- background tasks file
- async subagent runs dir
- sessions dir
- worktrees dir
- tool results dir
- memory dir

runtime state 的价值是：

- TUI 能展示当前状态。
- 后台任务有地方记录。
- workflow/subagent 可以暴露进度。
- worktree/git 状态可以被查询。
- session 退出或重启后仍有可见痕迹。

这部分要和产品体验一起讲：

> 用户信任 agent，不是因为模型说“我在处理”，而是因为系统能展示它正在处理什么。

---

## 14. 一条完整执行链路

这张图可以作为分享里的核心页：

```text
用户输入
  |
  v
cmd/modu_code TUI
  - 判断 slash / prompt / steer / follow-up / inline bash
  - 持有 cancel
  - 渲染状态和事件
  |
  v
CodingSession
  - 刷新 system prompt
  - 准备 active tools
  - 接入 approval/context/session/runtime
  |
  v
pkg/agent Loop
  - 调 LLM
  - 解析 tool calls
  - 执行 tools
  - 处理 steering/follow-up
  |
  v
Tool / Approval / Gate
  - read/edit/write/bash
  - 防脏写
  - 危险命令审批
  - plan mode 阻断写操作
  |
  v
Events + Persistence
  - agent event 给 TUI
  - session entry 写 JSONL
  - runtime state 更新
  |
  v
用户看到可解释、可中断、可恢复的 agent 运行过程
```

这条链路比模块清单更适合分享，因为它回答的是：一次真实请求到底怎么穿过系统。

---

## 15. 可以现场演示的 Demo 脚本

建议演示一个小任务，而不是展示所有功能。

### Demo 目标

让 agent 修改一个小功能，并展示 modu code 的运行时能力。

### 演示步骤

1. 启动：

   ```bash
   go run ./cmd/modu_code
   ```

2. 输入一个小任务，例如：

   ```text
   找到 slash command 的实现，解释 /compact 是怎么工作的
   ```

3. 展示 TUI 如何渲染 read/search/tool 调用。

4. 输入：

   ```text
   /context
   ```

   讲模型当前看到哪些上下文。

5. 运行中追加 follow-up：

   ```text
   顺便指出它最终调用到了哪个服务
   ```

6. 用 `/session` 或 `/tree` 展示 session 状态。

7. 退出后展示：

   ```text
   Session saved: ...
   Resume with: modu_code --resume ...
   ```

这个 demo 的重点不是模型答得多好，而是让听众看到：modu code 的每一步都有状态、有工具、有上下文、有持久化。

---

## 16. 40 分钟分享结构

| 时间 | 内容 | 目的 |
|---|---|---|
| 5 分钟 | 开场：为什么 coding agent 不是聊天脚本 | 建立问题意识 |
| 5 分钟 | 三层架构：cmd / coding_agent / agent | 建立全局地图 |
| 6 分钟 | `main.go` 和 TUI：宿主如何驱动运行 | 讲清产品入口 |
| 8 分钟 | `CodingSession`：runtime 如何组装 | 讲核心设计 |
| 5 分钟 | `pkg/agent`：薄 ReAct loop | 讲内核边界 |
| 5 分钟 | tools + approval + context + session | 讲可靠性来源 |
| 3 分钟 | extensions + worktree + runtime state | 讲可扩展和可观测 |
| 3 分钟 | 完整链路和总结 | 帮听众收束 |

---

## 17. 结尾总结

modu code 的核心价值不是“接了某个模型”，而是把 coding agent 的工程问题拆开了：

1. `cmd/modu_code` 负责宿主体验：配置、TUI、输入、中断、展示。
2. `pkg/coding_agent` 负责编码运行时：session、context、tools、approval、extensions。
3. `pkg/agent` 负责最小 ReAct loop：模型和工具循环。
4. 工具调用必须可审计、可审批、可中断。
5. 上下文必须可解释、可压缩、可恢复。
6. 会话必须能 resume、fork、export。
7. 长任务必须有 runtime state。
8. 扩展能力不能污染主循环。

最后可以用一句话收束：

> modu code 不是把 LLM 放进终端，而是把 LLM 放进一个可控、可恢复、可观察的编码运行时。
