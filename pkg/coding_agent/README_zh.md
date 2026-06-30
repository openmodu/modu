# coding_agent

`coding_agent` 是基于 `pkg/agent` 核心循环和 `pkg/providers` 多 Provider 抽象构建的上层编码代理系统，提供完整的 AI 辅助编码能力。

## 架构总览

```
┌─────────────────────────────────────────────────────────┐
│                      CodingSession                      │
│                    (coding_agent.go)                     │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌────────────────────┐    │
│  │   配置    │  │   资源    │  │  系统提示词构建器    │    │
│  │(settings) │  │ (Loader) │  │  (system_prompt.go) │    │
│  └──────────┘  └──────────┘  └────────────────────┘    │
│                                                         │
│  ┌──────────────────────────────────────────────────┐   │
│  │              pkg/agent.Agent                      │   │
│  │          (ReAct 循环 + 事件流)                     │   │
│  └──────────────────┬───────────────────────────────┘   │
│                     │                                    │
│  ┌──────────────────▼───────────────────────────────┐   │
│  │                 默认工具                           │   │
│  │              read │ bash │ edit │ write            │   │
│  │          ▲ (如果存在 hook 则使用 WrappedTool)      │   │
│  └──────────┼───────────────────────────────────────┘   │
│             │                                            │
│  ┌──────────┴──────┐  ┌────────────┐  ┌────────────┐   │
│  │      扩展       │  │    会话    │  │    压缩    │   │
│  │ (Hook/Command)  │  │  (JSONL)   │  │ (摘要压缩)  │   │
│  └─────────────────┘  └────────────┘  └────────────┘   │
│                                                         │
│  ┌─────────────────┐  ┌────────────────────────────┐   │
│  │    技能管理器   │  │   斜杠命令                  │   │
│  └─────────────────┘  └────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

## 目录结构

```
pkg/coding_agent/
├── coding_agent.go           # CodingSession 主入口，串联所有子系统
├── config.go                 # 配置管理（全局 + 项目级 settings.json）
├── context_info.go           # 当前 prompt/context 来源摘要
├── doctor_info.go            # 基础运行诊断摘要
├── system_prompt.go          # 系统提示词组装器
├── messages.go               # 自定义消息类型（Bash/Compaction/Branch/Custom）
├── slash_commands.go         # 内置斜杠命令（/model, /compact, /tree 等）
├── tools/                    # 内置编码工具
│   ├── read/read.go          #   文件读取（行号、分页、图片 base64）
│   ├── write/write.go        #   文件写入（自动建目录）
│   ├── edit/edit.go          #   精确替换编辑（歧义检测、replace_all、diff）
│   ├── bash/bash.go          #   Shell 命令执行（超时、进程组 kill）
│   ├── grep/grep.go          #   内容搜索（rg 优先，Go 内置回退）
│   ├── find/find.go          #   文件查找（fd 优先，Go 内置回退）
│   ├── ls/ls.go              #   目录列表（大小写不敏感排序）
│   ├── planning/             #   plan mode 和 todo_write
│   ├── memory/               #   memory 写入工具
│   ├── worktree/             #   worktree 进入/退出工具
│   ├── backend_task/         #   后台任务结果查询工具
│   ├── common/               #   路径解析、输出截断、FileReadState 脏写防护、ToSemanticInt/Bool 辅助函数
│   └── tools.go              #   工具集合工厂（AllTools/CodingTools/ReadOnlyTools）
├── session/                  # 会话持久化
│   ├── entry.go              #   会话条目定义（11 种 EntryType）
│   ├── manager.go            #   JSONL 文件存储 + Fork
│   └── tree.go               #   树形分支导航
├── compaction/               # 上下文压缩
│   ├── compaction.go         #   滑动窗口 + LLM 摘要压缩
│   └── branch_summary.go    #   分支跳转上下文摘要
├── extension/                # 扩展系统
│   ├── types.go              #   Extension/ExtensionAPI 接口、ToolHook
│   ├── runner.go             #   扩展生命周期管理（实现 ExtensionAPI）
│   └── wrapper.go            #   WrappedTool 透明包装（Before/After/Transform）
├── skills/                   # 技能系统
│   └── skills.go             #   技能发现与加载（Markdown + YAML frontmatter）
├── prompts/                  # Prompt template 系统
│   └── prompts.go            #   模板发现与 {{input}} 展开
└── resource/                 # 资源加载
    ├── loader.go             #   上下文文件发现（AGENTS.md 等）+ 目录初始化
    └── package.go            #   本地资源包 manifest 发现
```

## 核心功能

### 1. ReAct Agent 循环

基于 `pkg/agent` 实现的 ReAct（Reasoning + Acting）循环：

```
用户消息 → LLM 推理 → 工具调用 → 工具结果 → LLM 推理 → ... → 最终回复
```

- 每次 LLM 返回 `toolUse` 时自动执行对应工具，将结果回送 LLM 继续推理
- 直到 LLM 返回 `stop` 结束循环
- 支持 Steer（中途注入高优先级消息）和 FollowUp（排队后续消息）

### 2. 默认工具

默认会话只启用 4 个核心工具，对齐上游 coding-agent 的最小工具面：

| 工具 | 功能 | 关键特性 |
|------|------|----------|
| `read` | 文件读取 | 行号格式化、offset/limit 分页、图片自动 base64，兼容 `file_path` alias；通过 `NewTrackedTool` 记录读取状态供 write/edit 防止脏写 |
| `bash` | 命令执行 | 可配置超时（默认 120s）、进程组级 kill、输出尾部截断 |
| `edit` | 精确编辑 | 精确字符串匹配替换、歧义检测、replace_all、CRLF 兼容 |
| `write` | 文件写入 | 自动创建父目录、返回写入字节数 |

搜索和列目录工具保留为显式 opt-in 能力，不进入默认工具集：

| 工具 | 功能 | 关键特性 |
|------|------|----------|
| `grep` | 内容搜索 | 优先使用 ripgrep、Go 内置正则回退、忽略 .git 等目录 |
| `find` | 文件查找 | 优先使用 fd、Go 内置 glob 回退、尊重 .gitignore |
| `ls` | 目录列表 | 大小写不敏感排序、目录后缀 `/`、可限制条数 |

工具集合工厂函数：

```go
tools.CodingTools(cwd)    // 默认核心工具：read, bash, edit, write
tools.ReadOnlyTools(cwd)  // 显式只读工具：read, grep, find, ls
tools.AllTools(cwd)       // 显式全量工具：read, write, edit, bash, grep, find, ls
```

启用 `todoTool` feature gate 后会额外暴露 `todo_write`。该工具采用整体替换语义，参数固定为 `{"todos":[{"content":"...","status":"pending|in_progress|completed"}]}`；根对象和 todo item 都拒绝额外字段，并保持最多一个 `in_progress`。

### 污写防护（FileReadState）

`read`、`write`、`edit` 工具统一通过 `NewTrackedTool(cwd, readState)` 创建，共享一个 `FileReadState` 实例。`read` 每次完整读取文件时会记录文件内容 + 修改时间到 `FileReadState`；`write` 和 `edit` 在写入前会校验目标文件是否已被读取、内容是否匹配，拒绝基于过期内容的脏写操作。

辅助函数 `ToSemanticInt` 和 `ToSemanticBool`（`pkg/coding_agent/tools/common/helpers.go`）支持模型传入的数字字符串和布尔字符串（如 `"10"`、`"true"`），按 Claude Code 兼容语义自动转换为 Go 原生类型。


会话不直接构造具体工具包，而是依赖 `pkg/agent` 定义的工具管理抽象：

```go
type ToolManager interface {
    Tools(ctx types.ToolContext) []types.Tool
    Rebind(tool types.Tool, ctx types.ToolContext) (types.Tool, bool)
}
```

`CodingSessionOptions.ToolProvider` 可替换默认 manager。`pkg/coding_agent/tools.DefaultProvider` 是该接口的具体实现，负责内置工具、feature-gated 工具和 cwd rebind；调用方仍可通过 `Tools` / `CustomTools` 提供基础工具和附加工具。

### 3. Hook 系统（扩展钩子）

通过 Extension 机制注册 `ToolHook`，透明拦截所有工具调用：

```go
type ToolHook struct {
    Before    func(toolName string, args map[string]any) bool              // 返回 false 阻止执行
    After     func(toolName string, args map[string]any, result ToolResult) // 执行后审计
    Transform func(toolName string, result ToolResult) ToolResult      // 修改返回结果
}
```

典型用途：
- **安全防护**：Before hook 拦截危险命令（如 `rm -rf /`）
- **审计日志**：After hook 记录所有工具调用
- **结果变换**：Transform hook 对输出添加水印或脱敏

所有工具通过 `extension.WrapTools()` 统一包装，对 Agent 完全透明。

### 4. Goal 长程任务

`modu_code` 默认加载 `extension/goal`，提供 session-scoped 的 `/goal` 长程任务循环：

- `/goal <objective>` 设置或替换当前目标，并注入隐藏 follow-up 继续执行；TUI 中替换已有目标前会弹出确认。
- `/goal` 查看当前目标；`/goal status` 按 pi-goal 语义会被当作 objective，而不是状态子命令。
- `/goal pause`、`/goal resume`、`/goal clear` 控制生命周期；兼容旧入口 `/goal-pause`、`/goal-resume`、`/goal-cancel`、`/goal-status`。
- 模型可调用 `create_goal`、`get_goal`、`update_goal({status:"complete"})`；`update_goal` 只能用于完成目标，工具 schema 使用普通 string enum 表达 `complete` 状态。

目标状态持久化在当前 session 目录的 `extensions/pi-goal/<session-id>.json`，包含 `active`、`paused`、`budgetLimited`、`complete` 状态，以及 token/time accounting。目标文本最多 4000 个字符，更长的说明应放进文件再用 `/goal follow docs/goal.md` 引用。启动时会校验 goal store schema，坏文件不会被带病加载。达到显式 token budget 后会进入 `budgetLimited`，并注入收尾提示而不是继续做实质工作。Session shutdown 会 flush 最后一段未结算耗时，完成输出采用 pi-goal 的 `Completed at` ISO 时间格式。

Goal 状态也会暴露到 `RuntimeState().Extensions["goal"]`，TUI 底部状态行会显示 `Pursuing goal (...)`、`Goal paused (/goal resume)`、`Goal unmet (...)`、`Goal abandoned` 或 `Goal achieved (...)`。`/goal` 命令输出通过 extension notify 进入 TUI scrollback，print mode 仍会把同样文本写到 stderr。只有 session resume 后发现 paused goal 才会询问是否恢复；普通 startup 和 headless 模式保持 paused，避免无交互自动恢复。交互宿主可设置 `CodingSessionOptions.DeferStartupEvent`，完成 background prompt driver 和 UI wiring 后调用 `EmitStartupEvent()` 与 `EmitExtensionEvent("ui_ready")`，这样启动时的隐藏 goal follow-up 会进入前台 run loop，状态栏能显示 running 并支持中断。

### Subagent 扩展

`modu_code` 默认注册 `extension/subagent`。扩展总会注册 `subagent` 工具，因此即使还没有 Markdown profile，也能通过 `subagent({action:"list"})`、`subagent({action:"status"})` 和 `subagent({action:"doctor"})` 做可见性、runtime 后台任务状态和诊断。发现到 `~/.coding_agent/agents/` 或当前项目 `.coding_agent/agents/` 下的 profile 后，扩展还会注册兼容旧调用面的 `spawn_subagent` alias。`subagent` 支持 `single`、`parallel`、`chain` 三种执行模式，以及 `list`、`status`、`resume`、`interrupt`、`doctor` 管理动作；single 模式可传 `async: true` 启动一次性后台任务，或传 `async: false` 覆盖 profile 的 `background: true`。`spawn_subagent` 保留旧的 `{name, task}` 参数形状。两者都通过 `ExtensionAPI.ForkSession` 复用原有 subagent 执行能力：profile 中的 `tools`、`disallowed_tools`、`skills`、`memory`、`permission_mode`、`max_turns`、`background`、`isolation: worktree` 会传给 forked session。subagent lifecycle 仍会发出 harness `subagent_start` / `subagent_stop` 事件，运行时状态暴露在 `RuntimeState().Extensions["subagent"]`。

`extension/subagent` 的 `max_depth` 现在会在运行时生效。默认 `max_depth: 1` 表示主会话可以启动 child，但 child 不能继续嵌套启动 subagent；设为 `0` 会禁用执行型 subagent 调用，只保留管理/诊断动作。

后台 subagent 任务会写入 `RuntimePaths().BackgroundTasksFile`，并为每个任务维护 `RuntimePaths().AsyncSubagentRunsDir/<task-id>/status.json` 和 `session.jsonl`。因此同一项目 runtime 下重新创建 session 后，`task_output` 和 `subagent({action:"status"})` 仍能读取已完成任务的状态、输出和错误；即使列表文件丢失，也会从每个 run 的 `status.json` 恢复。`subagent({action:"status"})` 会按 `parentId` 缩进展示 follow-up run tree。`subagent({action:"interrupt", id:"..."})` 可取消当前进程内仍在运行的后台任务；`subagent({action:"resume", id:"...", message:"..."})` 会读取原任务的 child `session.jsonl` 作为上下文，重新启动一个后台 follow-up 任务，并把新任务标记为原任务的 child。

执行型 `subagent` 调用支持 `output` 和 `outputMode`。`output` 为绝对路径时直接写入该路径；相对路径会写入 `tool-results/<project>/subagents/` 下。`outputMode:"inline"` 会在正常结果后附加保存路径引用；`outputMode:"file-only"` 只返回 `Output saved to: ...` 的紧凑引用。`parallel` / `chain` 的每个子项也可以单独声明 `output` / `outputMode`。异步任务会在完成时写入目标文件，`task_output`、`subagent status` 和 runtime snapshot 会暴露对应的 `output_file`。

执行型 `subagent` 也支持 `reads`、`progress` 和 `chainDir`。`reads` 为文件列表时会在 child task 前注入 `Read from` 指令，`reads:false` 可关闭 profile 默认读取；`progress:true` 会创建/更新 `progress.md` 指令，默认位置是 `tool-results/<project>/subagents/progress.md`，传 `chainDir` 时使用该目录下的 `progress.md`。profile frontmatter 可配置 `reads` / `default_reads` 和 `progress` / `default_progress` 作为默认行为。

`context:"fork"` 会把当前父会话消息复制到 child 初始上下文，默认或 `context:"fresh"` 则只发送本次 task。profile frontmatter 可用 `default_context: fork` 设置默认上下文；单次调用仍可用 `context:"fresh"` 覆盖。执行型调用还支持 `model` 和 `skill` override；`skill` 可传字符串、字符串数组、`true` 使用 profile skills，或 `false` 禁用 profile skills。

并发执行既支持现有 `mode:"parallel"` + `parallel:[...]`，也支持 pi-style 顶层 `tasks:[...]`。`tasks`/`parallel` 子项可传 `count` 重复同一任务，顶层 `concurrency` 可限制本次并发 child 数。

`chain` 支持混合串行和并行 group：`chain:[{agent:"scout", task:"..."}, {parallel:[{agent:"reviewer-a", task:"review {previous}"}, {agent:"reviewer-b", task:"review {previous}"}]}, {agent:"planner", task:"combine {previous}"}]`。parallel group 内每个 child 会收到上一串行步骤的 `{previous}`，group 聚合输出会作为下一步的 `{previous}`。

执行型调用和 `parallel` / `tasks` / `chain` 子项支持 `cwd`。相对路径基于父 session cwd 解析；child 的环境提示和 file/shell 工具会绑定到该目录。包装过的工具如果实现 `WithCwd(string) types.Tool`，也会随 child cwd 重新绑定。

用户侧命令由同一个扩展注册：`/run <agent> <task>` 运行单个 profile，`/parallel <agent> <task> -> <agent> <task>` 并发运行多个 profile，`/chain <agent> <task> -> <agent> <task>` 串行运行并支持后续步骤中的 `{previous}`，`/subagents-doctor` 输出只读诊断。

### Lua Workflow 编排

`modu_code` 默认注册 `extension/workflow`，提供 Lua 脚本驱动的 `workflow` tool，用来对齐 `pi-dynamic-workflows` 的动态多 agent 编排能力。当前实现支持 `meta` 声明、运行时 `phase`、`log`、`agent`、`workflow`、`parallel`、`pipeline`、`json.encode` / `json.decode`、`json.null`、预算视图、tool update 进度，以及基于 `ExtensionAPI.ForkSession` 的隔离子 agent 执行。

workflow tool 的脚本来源必须在 `script`、`script_path`、`name` 中三选一；它只负责启动 workflow run，不接受 `action`、`status`、`id`、`run_id`、`agent_id` 这类管理参数，查看或控制已有 run 必须用 `/workflows show <run-id>`、`/workflows agent <run-id> <agent-id>`、`/workflows stop <run-id>` 或 exact `/workflows` TUI 面板。`script_path` 可重新运行已落盘脚本，`name` 会从当前 cwd 向上查找 Claude 兼容项目 `.claude/workflows/<name>.lua` 到 git root，最近目录优先，再查兼容旧路径 `.coding_agent/workflows/<name>.lua`，然后查 sibling `~/.claude/workflows/<name>.lua` 和 agent root `workflows/<name>.lua`。启动时会把已存在的 saved workflows 注册成 Claude 风格 `/<name> [json-args]` 命令并后台运行；若名称与内置/扩展命令冲突，则跳过直接命令但仍注册兼容 `/workflow:<name> [json-args]`。项目目录按最近优先覆盖父目录和用户目录同名 workflow；workflow tool 可传 `async:true` 后台启动并立即返回 run id；Lua 内的 `workflow(nameOrRef, args)` 可一层嵌套调用 saved workflow 名称或脚本路径，并共享父 workflow 的 budget、并发默认值、取消信号和 agent 总量上限。

当前 session 目录可用时，inline Lua 脚本会保存到 `extensions/workflow/runs/<run-id>/script.lua`，完成态 `snapshot.json` 和后台 run `status.json` 会保存在同一目录，最终 snapshot/details 暴露 `scriptPath` 和 `runDir`，工具文本也会包含 `Script:` 路径；`/workflows` 可列出当前 session 的 live/persisted runs，展示 running/stopped/failed/completed 状态、workflow 名称、agent/error 计数和结果预览，用 `/workflows show <run-id|latest>` 查看脚本和 metadata，用 `/workflows stop <run-id>` 取消运行中的 workflow，用 `/workflows resume <run-id|latest>` 恢复同 session 内 stopped run 并复用已完成 agent 结果，用 `/workflows restart <run-id|latest>` 将 run 脚本作为新的后台 run 重跑，并用 `/workflows save <run-id|latest> <name> [project|user]` 把 run 脚本保存到项目或用户 workflows 目录供后续 session 复用；project 保存会写入最近的既有项目 `.claude/workflows` 目录，没有既有目录时写入 git root 下的 `.claude/workflows`，user 保存会写入 sibling `~/.claude/workflows`。

可在 `/config` -> `Dynamic workflows`、`extensions.yaml` 的 workflow `config.disabled: true`、`~/.coding_agent/settings.json` 或项目 `.coding_agent/settings.json` 的 `disableWorkflows: true`、环境变量 `MODU_CODE_DISABLE_WORKFLOWS=1` / `CLAUDE_CODE_DISABLE_WORKFLOWS=1` 下关闭 workflow tool 和 workflow slash commands；通过 `/config` 禁用会移除当前 session 的 workflow tool 和 slash commands，重新启用后需要新会话或重启才能重新注册。tool 参数可传 `budget`，Lua 中 `budget.total` 暴露该值，`budget.spent()` 优先按 `subagent_child_usage` 中捕获到的 child usage 计数，用不到时回退到子 agent 返回文本估算，并作为预算视图按 `budget.total` 封顶；真实 per-agent 观测 token 仍保留在 workflow snapshot 中。`budget.remaining()` 返回剩余值；未传 `budget` 时 `budget.total` 和 `budget.remaining()` 为 nil。预算耗尽后后续 `agent()` 不再 fork，单次 workflow 默认最多 fork 1000 个 child，运行时默认并发为 4、并发上限钳制为 16。`agent` / `parallel` task 可传 `label`、`phase`、`model`、`cwd`、`isolation:"worktree"`、`tools`、`disallowed_tools`、`permission_mode`、`max_turns`、`thinking`、`skills`、`memory_scope` 和 `schema`，这些字段会映射到 forked session；`schema` 使用 JSON Schema 子集约束 child final JSON，返回值会被解析和校验为 Lua table，失败时会带校验错误和上一轮输出重试 1 次，仍失败则返回 `json.null` 并记录 log；`memory_scope` 仅接受 `none`、`user` / `global`、`project` / `local`、`both` / `all`。未传 `tools` 时 child 继承当前主 agent 可见 tool allowlist；传入 `tools` 时会从父 session 工具目录中按名筛选，因此 session-connected/custom/MCP 风格工具可显式转发，且 `grep`、`find`、`ls`、`web_search`、`web_fetch` 这类 opt-in 发现/研究工具可被 child 明确请求后补齐；`web_search` 默认使用公开搜索入口，也可用 `MODU_WEB_SEARCH_ENDPOINT` 指向带 `q` 参数或 `{query}` 占位符的搜索 endpoint。`pipeline` 支持每个 item 顺序经过 stage，并按 `concurrency` 调度 item；由于同一个 Lua VM 不能并发执行字节码，stage 函数访问会串行保护，真正的多 agent fan-out 应优先用 `parallel`。

当 workflow tool 处于 active tools 中时，主 agent 的 system prompt 会注入动态工作流编写指南：用户明确说 `workflow`、`dynamic workflow`、`ultracode`，或任务明显需要大规模 fan-out/fan-in 时，模型可以自己写 Lua workflow 脚本并调用 `workflow` tool。`/effort ultracode` 会在支持 xhigh reasoning 的模型上开启当前 session 的 workflow-first 模式，并追加 Ultracode prompt block，让模型对每个实质性任务都优先考虑动态 workflow；`/effort high|medium|low|off` 会退出该模式。当前尚未实现 Claude Code 的输入关键词高亮、`Option/Alt+W` 取消触发和运行前 approval card。

workflow tool、saved `/<name>` / `/workflow:<name>` 命令、`/deep-research` 和 `/workflows restart` 在启动前会通过 host `Select` 展示 workflow 名称、description、推断出的 phase、script path、资源上限和 Lua 脚本预览；用户可以选择 run once、always allow this workflow in this project、view raw script 或 cancel，拒绝时不会 fork 任何 child agent。always-allow 记录写入 agent dir 下的 `workflow_approvals.json`，按 project root、workflow 名称/source 和脚本 hash 匹配。当前尚未实现 Claude Code 的 open-in-editor 动作、permission-mode 差异化跳过和 Desktop approval card 渲染。

workflow snapshot 会记录每个 agent 的 `startedAt`、`endedAt`、`durationMs`、计入预算的 `estimatedTokens`、子事件观测到的 `turnTokens`、provider 已上报的 `cost`、失败 tool-call 数和最近 child tool 名称/参数/结果/错误，并通过 `phaseSummaries` 聚合每个 phase 的 agent 数、token、observed cost 和耗时；`/workflows show` 会展示 phase 聚合与单个 agent 的 token、cost 和耗时。child usage 可用时会进入 workflow runner 的 token 视图和预算检查；`Usage.Cost.Total` 可用时会聚合到 workflow/phase/agent cost 视图，但不会按模型 pricing 自行推导 cost。

Workflow live run 状态也会暴露到 `RuntimeState().Extensions["workflow"]`，包含 running/stopped/completed/failed 计数、最近 run 列表、phase summary、每个 run 的 agent 摘要和状态栏 `indicator`。Agent 摘要包含 label/phase/status、短 prompt 预览、最多 4000 字节的 prompt、token/tool 计数、result/error 预览和最近 tool call 的参数/结果短预览；`/workflows agent <run-id|latest> <agent-id>` 也会展示 prompt，并同样按 4000 字节显示上限截断。TUI 底部状态行会在有后台 workflow 运行时显示 `workflow <name> <done>/<total> running[: phase]` 或 `workflows <n> running`；TUI 中 exact `/workflows` 会打开本地可选择 run 列表，显示 live run 计数和运行 indicator，支持 `j/k` 或方向键选择、`Enter`/右方向键进入所选 run 的 phase progress view，再进入该 phase 的 agent list，再进入 agent detail view，detail 中展示 prompt/result/error/tool-call 预览；`Esc`/左方向键按 agent detail -> agents -> phases -> runs 逐层返回，run 列表中 `Esc`/`q` 关闭。TUI `p` 会 pause/resume 当前 run，`x` 在 agent list/detail 上停止 selected agent、其他层级停止 run，`r` 重启 selected agent，`s` 打开保存命名输入层并可用 `Tab` 切换 project/user scope。完整 child transcript 会写入 workflow snapshot，并可通过 `/workflows transcript <run-id|latest> <agent-id>` 浏览；TUI 内联 transcript 视图仍是后续 polish。

`/workflows agent <run-id|latest> <agent-id>` 可查看单个 workflow agent 的 label、phase、状态、估算 token、turn token、observed cost、失败 tool-call 数、最近 child tool 名称、参数预览、结果预览、错误状态、耗时、错误、结果预览和原始 prompt；`/workflows transcript <run-id|latest> <agent-id>` 可浏览该 agent 捕获到的 child user/assistant/tool-result transcript、tool call 参数和 usage。

`/deep-research <question>` 是内置 bundled workflow，会在后台运行 scope、parallel research、cross-check、synthesis 四个阶段，并复用 `/workflows` 查看/停止/恢复能力。该 workflow 会请求 `web_search` / `web_fetch` 工具；modu 当前还没有内置这两个工具，因此真正的联网引用报告质量取决于宿主是否提供对应工具。

`/workflows resume <run-id|latest>` 可恢复同一进程/session 内被 stop 的后台 workflow：已完成 agent 结果从内存缓存返回，不会再次 `ForkSession`，未完成分支继续 live 执行；退出进程后只能使用 `/workflows restart <run-id|latest>` fresh run。

完整对齐方案、Lua DSL、ForkOptions 映射、安全沙箱要求和真实验收 case 记录在 [docs/lua-workflow-orchestration-plan.md](../../docs/lua-workflow-orchestration-plan.md)。后续工作继续按该文档的 M4-M6 验收规则推进。

### 5. 自动上下文压缩（Auto Compaction）

当累计 token 用量超过模型 context window 的阈值百分比时，自动触发压缩：

```
Agent 完成回复 → 累加 token 用量 → 超过阈值？→ 调用 Compact()
                                                    │
                    ┌───────────────────────────────┘
                    ▼
        旧消息 → LLM 生成摘要 → [摘要消息] + [保留的最近消息]
```

- 默认阈值：context window 的 80%
- 普通 `Prompt()` 和 queued `Continue()` 轮次结束后都会执行同一自动压缩检查
- 默认保留最近 4 条消息
- 无 LLM 可用时回退为直接截断
- 压缩后自动重置 token 计数器
- 可通过 `config.AutoCompaction = false` 关闭
- 支持 `/compact` 手动触发

### 6. 上下文来源摘要

`GetContextInfo()` 返回当前运行时可见的 prompt/context 来源摘要，包括当前模型、工作目录、消息数、系统 prompt 大小、项目上下文文件、memory 是否为空、skills、plan mode 和 worktree 状态。`modu_code` 的 `/context` 命令基于这份只读摘要渲染，便于确认模型为什么会看到某些上下文。

`GetDoctorInfo()` 返回基础运行诊断摘要，包括模型配置路径、当前模型、baseURL 连通性、provider 注册状态、API key 状态、上下文文件数量和问题列表。`modu_code` 的 `/doctor` 命令基于这份只读摘要渲染。

### 7. 计划模式（Plan Mode）

启用 plan mode 后，系统 prompt 会标记当前处于规划状态，要求只做只读调研、先产出方案。执行层同时会阻断 `write`、`edit` 和 `bash` 工具，避免计划阶段直接修改项目文件或通过 shell 绕过。

进入方式：`/plan on`、模型自调 `enter_plan_mode`，或在 `modu_code` TUI 中按 `Shift+Tab` 一键切换。

审批门（对齐 Claude Code）：模型完成调研后调用 `exit_plan_mode`，传入 `plan`（markdown 方案）和 `steps`（有序子任务数组）。该调用**强制**弹出用户审批，绕过 always-allow 缓存与权限规则，不会被静默放行（无交互回调的 headless 场景才自动放行）：

在 `modu_code` TUI 中，plan 会以 markdown 块渲染进对话（glamour 高亮），审批框只保留三选项决策：

- `[Y]es, start coding`（批准）：退出 plan mode，`steps` 转为 todo 列表，模型按子任务逐项执行，编辑仍逐个确认；
- `[A] auto-accept edits`（批准并自动接受编辑）：同上，且本会话后续 `write`/`edit`/`bash` 自动放行，不再逐个弹审批；
- `[N]o, keep planning`（拒绝）：弹出原因输入框——写了原因则回车把反馈**直接喂给模型**，空回车即纯拒绝；两种情况都保持在 plan mode，模型据此修订后再次提交。

实现上，plan 审批不走 agent 工具审批门（`exit_plan_mode` 在 gate 处直接放行），而是由工具内部经独立的带原因回传通道驱动，确保拒绝反馈能完整回到模型；headless / `--no-approve` 场景自动批准。

### 7. 自动重试（Auto Retry）

内置在 Agent 循环中的指数退避重试机制：

```
LLM 调用失败 → 瞬态错误？→ 是 → 等待(指数退避 + 抖动) → 重试（最多 3 次）
                          → 否 → 立即返回错误
```

自动识别的瞬态错误：
- HTTP 429 / 500 / 502 / 503 / 504
- 连接拒绝、连接重置、超时
- 意外 EOF、服务过载

永久错误（401、404、参数错误等）不会重试。

### 7. 会话持久化与分支

基于 JSONL 的树形会话存储：

```
~/.coding_agent/sessions/--path-to-project--/<timestamp>_<session-id>.jsonl
```
- **Flush 方法**：`Flush()` 确保 session header 写入磁盘，即使还没有任何 entry 追加；退出时可保证空 session 也能被恢复

- **消息序列化**：`messagePayload` 通过类型列表正确处理 `UserMessage` / `AssistantMessage` / `ToolResultMessage` 及其指针类型，恢复时识别持久化的 `toolResult` role，不丢失工具结果消息类型信息；resume 后会从已恢复的 assistant usage 重建 session token stats，避免 footer 和 `/session` 的 ctx/tokens 回到 0
每条记录包含 `id` + `parentId`，支持：
- **自动持久化**：消息、模型变更、压缩操作自动记录
- **Fork 分支**：从历史任意点创建分支，探索不同路径
- **树形导航**：`/tree` 可查看并跳转到会话树中的任意条目，跳转后恢复目标路径并注入 branch summary；`/fork <id>` 创建分支
- **会话摘要**：可列出会话文件、最近修改时间、首条消息、display name
- pi-compatible JSONL header：第一行是 `{"type":"session","version":3,...}`
- 9 种条目类型：`message`、`model_change`、`thinking_level_change`、`compaction`、`branch_summary`、`session_info` 等

### 8. 扩展系统

通过 Go 接口注入方式注册扩展：

```go
type Extension interface {
    Name() string
    Init(api ExtensionAPI) error
}
```

扩展能力：
- 注册自定义工具（`RegisterTool`）
- 注册带描述的斜杠命令（`RegisterCommand`）
- 注册工具钩子（`AddHook`）
- 订阅 agent 事件（`On`）
- 注入对话消息（`SendMessage`）
- 控制工具开关（`SetActiveTools`）
- 切换模型（`SetModel`）

### 9. 资源系统

**技能**（Skills）：从 `~/.coding_agent/skills/` 和 `.coding_agent/skills/` 目录发现 Markdown/Text 文件，支持 YAML frontmatter 定义名称、描述、标签。系统提示词只注入技能索引（名称、描述、路径和 base_dir），正文在显式调用技能或 subagent profile 引用时按需加载。

**Subagent profiles**：从 `~/.coding_agent/agents/` 和 `.coding_agent/agents/` 目录发现 Markdown profile。项目 profile 会覆盖同名全局 profile；发现到至少一个 profile 时，`extension/subagent` 会向模型暴露 `subagent` 和兼容 alias `spawn_subagent` 工具。

**Prompt templates**：从 `~/.coding_agent/prompts/` 和 `.coding_agent/prompts/` 目录发现 Markdown 模板。模板文件名或 frontmatter `name` 会注册为斜杠命令。模板里的参数占位符支持 Claude Code 自定义命令风格：`$ARGUMENTS`（全部参数）、`$1` / `$2` ...（按空格切分的位置参数），以及兼容旧版的 `{{input}}` / `{{args}}`。模板里的 `` !`command` `` 会在当前工作目录执行该命令，并把输出内联替换进 prompt（例如 `` 当前分支：!`git branch --show-current` ``）。模板没有任何占位符时，命令参数会追加到末尾。

**本地资源包**：从 `~/.coding_agent/packages/<name>/package.json` 和 `.coding_agent/packages/<name>/package.json` 发现资源包。当前支持 `skills` 和 `prompts` 路径，`enabled: false` 可禁用包：

```json
{
  "name": "team-coding",
  "skills": ["skills/**/SKILL.md"],
  "prompts": ["prompts/*.md"]
}
```

使用示例：

```text
/prompts
/review pkg/coding_agent
/skill-name task
/context
```

### 10. 输出截断

防止超长输出撑爆上下文：

| 函数 | 用途 | 默认限制 |
|------|------|----------|
| `TruncateHead` | 保留前 N 行（read 工具用） | 2000 行 |
| `TruncateTail` | 保留后 N 行（bash 工具用） | 2000 行 |
| `TruncateLine` | 单行字符截断（grep 工具用） | 500 字符 |

### 11. 内置斜杠命令

| 命令 | 功能 |
|------|------|
| `/model <provider> <id>` | 切换模型 |
| `/context` | 显示当前 prompt/context 来源 |
| `/doctor` | 显示基础运行诊断 |
| `/worktree` | 查看或管理当前 isolated worktree |
| `/compact` | 手动触发上下文压缩 |
| `/session` | 显示当前会话 ID、名称、文件、cwd、模型、消息数、tokens、plan/worktree 和资源摘要 |
| `/session name <name>` | 设置当前会话 display name |
| `/session delete <file>` | 删除非当前会话文件 |
| `/sessions [all]` | 列出当前项目或全部项目的已保存会话 |
| `/resume <file>` | 切换到指定会话文件 |
| `/fork-session <file>` | 从指定会话文件复制一份到当前项目 |
| `/tree` | 显示/跳转会话树，TUI 下支持搜索、summary 预览和 branched session 创建 |
| `/fork <id>` | 从指定条目创建分支 |
| `/branch-session <id>` | 抽取当前会话分支路径为新的会话文件 |
| `/export [file]` | 导出当前会话为 HTML |
| `/copy` | 复制最后一条 assistant 回复到系统剪贴板 |
| `/changelog` | 显示当前 git 仓库最近提交 |
| `/settings` | 显示当前配置 |
| `/tools` | 显示当前活跃工具 |
| `/skills` | 显示已发现 skills |
| `/prompts` | 显示已发现 prompt templates |
| `/help` | 显示帮助信息 |

## 快速开始

```go
package main

import (
    "context"

    "github.com/openmodu/modu/pkg/agent"
    coding_agent "github.com/openmodu/modu/pkg/coding_agent"
    "github.com/openmodu/modu/pkg/providers"
    "github.com/openmodu/modu/pkg/types"
)

func main() {
    providers.Register(providers.NewOpenAIChatCompletionsProvider("ollama",
        providers.WithBaseURL("http://localhost:11434/v1"),
    ))

    model := &types.Model{
        ID:            "qwen3-coder-next",
        ProviderID:    "ollama",
        ContextWindow: 32768,
        MaxTokens:     4096,
    }

    session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
        Cwd:   "/your/project",
        Model: model,
        GetAPIKey: func(provider string) (string, error) {
            return "", nil
        },
    })
    if err != nil {
        panic(err)
    }

    // 订阅事件
    session.Subscribe(func(event types.Event) {
        // 处理 agent_start, message_update, tool_execution_start/end, agent_end
    })

    // 发送任务
    err = session.Prompt(context.Background(), "读取 main.go 并解释它的功能")
    if err != nil {
        panic(err)
    }
    session.WaitForIdle()
}
```

## 使用 Hook 扩展

```go
type safetyExtension struct{}

func (e *safetyExtension) Name() string { return "safety" }
func (e *safetyExtension) Init(api extension.ExtensionAPI) error {
    api.AddHook(extension.ToolHook{
        Before: func(toolName string, args map[string]any) bool {
            if toolName == "bash" {
                cmd, _ := args["command"].(string)
                if strings.Contains(cmd, "rm -rf /") {
                    return false // 阻止危险命令
                }
            }
            return true
        },
    })
    return nil
}

// 创建 session 时注入
session, _ := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
    // ...
    Extensions: []extension.Extension{&safetyExtension{}},
})
```

## 配置

配置按优先级加载（后者覆盖前者）：

1. 内置默认值
2. 全局配置：`~/.coding_agent/settings.json`
3. 项目配置：`.coding_agent/settings.json`
4. 构造函数参数

```json
{
  "thinkingLevel": "medium",
  "autoCompaction": true,
  "disableWorkflows": false,
  "compactionSettings": {
    "maxContextPercentage": 80.0,
    "preserveRecentMessages": 4
  },
  "customSystemPrompt": "",
  "appendPrompts": []
}
```

### Harness 配置

`coding_agent` 现在支持把一部分宿主层行为放到 `settings.json` 的 `harness` 段里控制。

默认行为：

- 首次加载会自动生成 `~/.coding_agent/settings.json`
- safe harness outputs 默认自动开启
  - `logFiles`
  - `artifactFiles`
  - `bridgeDirs`
- host actions 默认自动开启
  - 只要配置了 `actions` 就会执行
  - 如需关闭，显式设置 `enableActions: false`

常用能力：

- `features`
  - 统一开关宿主级能力
  - 支持 `memoryTool`、`todoTool`、`taskOutputTool`、`planMode`、`worktreeMode`、`spawnSubagentTool`、`harnessActions`
  - `worktreeMode` 开启后，host 可通过 `EnterWorktree()` 创建 managed worktree：目录在 `<agentDir>/worktrees/<uuid>/<repo>`，分支名为 `modu-code/<repo>-<id>`，便于像 Codex 一样把会话修改隔离在独立 checkout 中。
- `permissions`
  - 统一配置工具权限规则
  - 支持 `allowTools`、`denyTools`、`allowBashPrefixes`、`denyBashPrefixes`
  - 危险 bash 写操作会绕过宽授权并强制走交互审批；交互审批里的 bash “always” 只记忆同一条命令
- `blockTools`
  - 在工具执行前直接阻断指定工具
- `captureHints`
  - 是否剥离并缓存 `<modu-code-hint .../>`
- `persistToolResults`
  - 是否把工具文本结果写到 runtime `tool-results/`
- `logFiles`
  - 追加 JSONL 事件流
- `artifactFiles`
  - 覆盖写最新事件快照
- `bridgeDirs`
  - 每个事件写一个独立 JSON 文件，方便外部 watcher 消费
- `enableActions`
  - 是否允许执行 host action，默认 `true`
- `actions`
  - 目前支持 `type: "exec"` 的宿主命令分发
  - 可通过 `onFailure: "stop"` 在失败后停止后续同类 action
- `actionPolicy`
  - 默认要求 `command` 使用绝对路径
  - 可继续约束 command 前缀、dir 前缀和最大 timeout

示例：

```json
{
  "features": {
    "memoryTool": true,
    "todoTool": true,
    "taskOutputTool": true,
    "planMode": true,
    "worktreeMode": true,
    "spawnSubagentTool": true,
    "harnessActions": true
  },
  "permissions": {
    "denyTools": ["bash"],
    "allowBashPrefixes": ["go test", "git status"]
  },
  "harness": {
    "blockTools": ["bash"],
    "captureHints": true,
    "persistToolResults": true,
    "logFiles": {
      "toolUse": "logs/tool-use.jsonl",
      "compact": "logs/compact.jsonl",
      "subagent": "logs/subagent.jsonl"
    },
    "artifactFiles": {
      "toolUse": "artifacts/tool-use-latest.json",
      "compact": "artifacts/compact-latest.json",
      "subagent": "artifacts/subagent-latest.json"
    },
    "bridgeDirs": {
      "toolUse": "bridge/tool-use",
      "compact": "bridge/compact",
      "subagent": "bridge/subagent"
    },
    "enableActions": true,
    "actions": {
      "toolUse": [
        {
          "type": "exec",
          "command": "/bin/sh",
          "args": [
            "-c",
            "printf '%s:%s' \"$HARNESS_EVENT_TYPE\" \"$HARNESS_TOOL\" > action-marker.txt"
          ],
          "dir": "{{agent_dir}}",
          "timeoutMs": 1000,
          "onFailure": "stop",
          "retry": {
            "maxAttempts": 2,
            "delayMs": 50
          }
        }
      ]
    },
    "actionPolicy": {
      "requireAbsoluteCommand": true,
      "allowCommandPrefixes": ["/bin", "/usr/bin"],
      "allowDirPrefixes": ["/Users/you/.coding_agent"],
      "maxTimeoutMs": 5000
    }
  }
}
```

### Harness Runtime 输出

运行时路径由 harness 统一管理，主要包括：

- `sessions/`
- `tool-results/`
- `runtime/<project>/index.json`
- `runtime/<project>/actions/<category>/latest.json`

除 agent root、配置文件和 session 目录外，运行时目录按需创建：查询 `RuntimePaths()` 只返回路径，不会预创建空的 `tool-results/` 或 `runtime/` 树。

其中：

- `runtime index`
  - 记录 resolved 输出目标和每个 category 的最新事件
- `runtime state`
  - 以 `runtime_state` sidecar entry 写入当前 session JSONL，包括 mode、feature gate、permission rules、todo、background task、tool count 和 runtime paths；它不参与会话分支 leaf
- `plan snapshot`
  - 以 `plan_snapshot` sidecar entry 写入当前 session JSONL，保存最新计划和历史计划；它不参与会话分支 leaf
- `pkg/coding_agent/taskoutput`
  - 复用 background task 的公开类型和 store 接口，供 session runtime 与 task_output tool 共用
- `action status artifact`
  - 记录 host action 的执行状态、`stdout`、`stderr`、合并 `output`、错误、重试次数和 timeout 标记

### Harness Action 模板变量

`command`、`args`、`dir` 里支持这些模板变量：

- `{{agent_dir}}`
- `{{cwd}}`
- `{{runtime_dir}}`
- `{{event_category}}`
- `{{event_type}}`
- `{{tool}}`
- `{{subagent_name}}`
- `{{subagent_task}}`

### Harness Action 环境变量

执行 `exec` action 时，还会注入这些环境变量：

- `HARNESS_EVENT_CATEGORY`
- `HARNESS_EVENT_TYPE`
- `HARNESS_EVENT_JSON`
- `HARNESS_AGENT_DIR`
- `HARNESS_RUNTIME_ROOT`
- `HARNESS_TOOL`
- `HARNESS_SUBAGENT_NAME`

## 请求处理流程

```
Prompt(text)
    │
    ├─ 斜杠命令？ ──→ 执行命令 handler ──→ 返回
    │
    ├─ 技能匹配？ ──→ 注入本轮 skill 指令并提交 task
    │
    ├─ 记录到 session.Manager (JSONL)
    │
    ├─ agent.Prompt(text) ──→ ReAct 循环
    │      │
    │      ├─ LLM 推理 (streamAssistantResponseWithRetry)
    │      │     └─ 瞬态错误? → 指数退避重试
    │      │
    │      ├─ 工具调用 → WrappedTool
    │      │     ├─ Before hook → 允许/阻止
    │      │     ├─ 执行工具
    │      │     ├─ After hook → 审计
    │      │     └─ Transform hook → 变换结果
    │      │
    │      └─ stop → 结束循环
    │
    └─ maybeAutoCompact()
          └─ token 超阈值? → Compact() → 摘要替换旧消息
```
