# modu TUI 封装与业务拆分

`pkg/modu-tui` 是唯一的终端 UI 内核；`modu_code` 只负责把 CodingSession 的状态和用户动作映射到这个内核。实施顺序固定为：先收口 UI 内核的状态与副作用，再拆 `modu_code` 业务，最后删除不再使用的 `pkg/tui`。

## 当前判断

`pkg/modu-tui` 已经具备可复用的渲染组件、标准 ViewModel 和宿主边界。
`modu_code` 的 Runtime、产品命令注册和实时 EventPresenter 已经拆出，
历史 transcript 也已统一到 EventPresenter，ToolPresenter 已独立成业务模块，
WorkflowView 和 typed snapshot decoder 也已独立。`runModuTUI` 只剩 226 行
启动装配；workflow 生命周期、事件订阅、channel、prompter、duration 和
history/footer 都不再由 runner 实现。

已经成立的边界：

- 不依赖 `coding_agent`、provider、workflow 或 `cmd`。
- `Block`、`InputBlock`、Markdown、Code、Tool、Todo、Card 等渲染能力可以独立测试。
- 宿主通过消息更新 transcript、status、footer、todo 和 panel。
- 业务可以用 `Entry + Node` 描述文本、Markdown、代码、表格、列表、键值和进度，
  用稳定 ID 增量更新，不需要理解 Block 或 Bubble Tea。
- Panel 行和快捷键可以携带稳定 Action ID 与 typed payload。
- 现有单元测试覆盖输入、滚动、审批、panel、图片、工具块和选择复制。

根 `Model.Update` 仍负责把消息路由到 transcript、composer、overlay 和
chrome，这是 UI 内核的组件协调职责，不回流到 `modu_code` 业务层。

## 目标依赖方向

```text
cmd/modu_code
  ├─ 参数、配置和进程启动
  ├─ ToolPresenter / WorkflowView
  ├─ WorkflowController / EventBindings
  ├─ channel、prompter、history/footer 等宿主适配
  └─ internal/tui
       ├─ Runtime          Prompt/Continue/FollowUp/Steer/Cancel
       ├─ CommandRegistry  命令定义、补全和执行
       ├─ EventPresenter   Agent/Session Event -> UI ViewModel
       └─ DialogFlow       config/channel/model 业务流程
             |
             v
pkg/modu-tui
  ├─ Model                焦点和消息路由
  ├─ transcriptModel      消息、滚动、选择、Block
  ├─ composerModel        输入、历史、IME、图片、slash
  ├─ overlayModel         approval/choice/text/panel
  ├─ chromeModel          status/footer/todo
  ├─ Entry / Node         标准展示数据
  ├─ Update               Append/Upsert/Remove/Replace
  ├─ Client               宿主更新 UI 的语义接口
  └─ Intent / Action      UI 输出给宿主的 typed action
```

依赖只能向下。`pkg/modu-tui` 不得导入 `coding_agent`，业务 Presenter 不得进入 UI 内核。

## UI 内核接口

### Intent：用户动作向外输出

UI 不在 `Update` 中同步调用业务代码。它生成 typed intent，并通过 `tea.Cmd` 在事件循环外投递：

```go
type Intent interface {
	isIntent()
}

type SubmitIntent struct {
	Text   string
	Images []ImageAttachment
	Kind   SubmitKind
}

type SlashCommandIntent struct {
	Line string
}

type PanelActionIntent struct {
	Action PanelAction
}

type InterruptIntent struct{}
```

输入历史变化、panel 关闭和审批决定也属于 intent。宿主只通过
`IntentHandler` 接收这些动作；Render 不访问宿主回调。

### Client：宿主向内更新

业务代码不直接构造 `tea.Msg`，也不直接调用 `tea.Program.Send`。`Client` 提供语义方法：

```go
client.AppendEntry(entry)
client.UpsertEntry(entry)
client.RemoveEntry(entryID)
client.ReplaceEntries(entries)
client.ClearTranscript()
client.SetStatus(status, ttl)
client.SetBusy(true)
client.SetTodos(todos)
client.OpenPanel(panel)
client.RefreshPanel(panel)
client.ClosePanel(panelID)
client.AskChoice(ctx, request)
client.AskText(ctx, request)
```

所有语义方法都进入 `Client.Apply(Update)`，经过防御性复制后只向 Bubble Tea
发送一种 `UpdateMsg` 运输信封。`Client` 负责消息投递、Dialog 响应和 context
取消，业务流程不管理 response channel。`UpdateMsg` 只允许出现在 UI 适配层，
业务代码不能直接构造。

### Entry、Node 和 Update：标准展示数据

业务 Presenter 输出 `Entry`，不为每一种业务结果增加互斥布尔字段：

```go
entry := modutui.Entry{
	ID:   "workflow:run-42",
	Role: modutui.RoleAssistant,
	Nodes: []modutui.Node{
		modutui.MarkdownNode{Text: "## Workflow"},
		modutui.KeyValueNode{Items: []modutui.KeyValue{
			{Key: "status", Value: "running"},
		}},
		modutui.ProgressNode{Current: 2, Total: 5},
	},
}
client.UpsertEntry(entry)
```

首批标准 Node 只有九种：Text、Markdown、Code、Thinking、Tool、Table、List、
KeyValue 和 Progress。
这个集合不是万能布局 DSL。新增 Node 的判据是至少出现两个真实业务调用方；只有一个
调用方的特殊视觉结构继续使用 `BlockFactory`，不能先把业务类型塞进 UI 内核。

Append 可以省略 Entry ID；空 ID 的 Upsert 按 Append 处理，空 ID 的 Remove
不执行任何操作。需要后续更新的业务必须提供稳定 ID。`Update` 是宿主侧统一状态
协议，覆盖 Entry、Todo、Panel、Status、Busy 和 Footer：

```go
client.Apply(modutui.UpsertEntryUpdate{Entry: entry})
client.Apply(modutui.SetTodoListUpdate{Items: todos})
client.Apply(modutui.ShowPanelUpdate{Panel: panel})
```

### Action：结构化交互

Panel 不需要把所有交互编码进命令字符串：

```go
Action{
	ID:      "workflow.control",
	Payload: WorkflowAction{Verb: "pause", RunID: "run-42"},
}
```

内核只回传 ID 和 Payload，不解析业务字段。`modu_code` 的 IntentRouter 把 Action
交给业务 handler。旧 `Command` 字段在迁移期保留，结构化 Action 优先。

### IO Port：外部资源异步返回

剪贴板、粘贴图片和 tool artifact 都是外部 IO：

```text
UI action
  -> tea.Cmd 调用 port
  -> result message 返回 Update
  -> Update 只更新状态
```

`Update`、`Render` 和 `rebuild` 不允许直接读取文件、访问网络或执行宿主回调。

## 内部状态拆分

不引入万能 `Component` 接口。四个具体子模型各自维护状态，根 Model 只决定消息交给谁。

### transcriptModel

负责：

- Entry 归一化和 ToolCall.ID 合并。
- Block 构造和 transcript 行缓存。
- viewport、follow、unseen、jump-to-bottom。
- 鼠标选择、gutter 和复制范围。

不负责输入、Dialog、业务回调或 artifact 读取。

### composerModel

负责：

- `InputBlock`、光标、IME、粘贴和图片 token。
- 输入历史。
- slash command 匹配与补全。
- 生成 Submit/Slash Intent。

不判断 CodingSession 是否真的在运行；宿主通过 busy 状态决定 submit kind。

### overlayModel

同一时间只允许一个 overlay：

```text
none | panel | approval | choice | text
```

打开新 overlay、关闭、取消和响应只有一套状态迁移，不再分别清空多个指针。

### chromeModel

负责：

- busy/streaming/status/transient status。
- footer 和 todo。
- 固定区高度预算。

它不读取 session；footer、todo 和状态文本都由宿主传入。

## Entry 内核边界

transcript 内部直接存储 `Entry`。Tool 合并、审批状态、artifact 加载和折叠状态
直接更新 `ToolNode`；Thinking 折叠状态直接更新 `ThinkingNode`。仓库中不存在
第二套 transcript 数据结构或旧的具体 Bubble Tea 更新消息。

## modu_code 业务拆分

只有 UI 内核验收通过后才进入这一阶段。

### Runtime

从 `runModuTUI` 提取前台运行状态：

- 当前 prompt context/cancel/id。
- foreground run 计数。
- queued follow-up 和 steer。
- Prompt/Continue 循环。
- Abort 和 AbortBash。
- running/completed/interrupted/error 状态。

Runtime 只依赖 `CodingSession` 和 `modutui.Client`，不拼接 panel 或工具展示文本。

### CommandRegistry

命令名称、别名、描述和执行函数只注册一次。注册表生成 slash suggestions，
并负责产品命令的解析和路由，避免提示列表与执行分支分离。兼容期内，
`pkg/slash` 的命令由注册表登记名称后转交旧 handler；待这些命令逐个迁移后，
`/help` 也直接由注册表生成。

### Presenter

Presenter 是纯函数：

```text
Agent Event       -> []modutui.Entry
Session Event     -> modutui.Entry
Tool Call/Result  -> modutui.ToolNode
Workflow Snapshot -> modutui.Panel
```

workflow runtime state 只解码一次。Cockpit、Feed、Map、Agent 等页面消费 typed snapshot，不重复解析 `map[string]any`。

实时 Agent/Session 事件已经由 `internal/tui.EventPresenter` 输出 `Entry`。
文本、思考和工具分别映射为 MarkdownNode、ThinkingNode 和 ToolNode；ToolNode
的摘要、diff、artifact 等字段由 ToolNodePresenter 接口提供。这样事件顺序和
工具展示可以分开测试，ToolPresenter 迁移不再改动订阅代码。

启动恢复和 `/resume` 会话切换也调用同一个 EventPresenter。历史树中的 message
节点经过 `AgentMessage`，compaction 节点经过 `ContextCompactEntry`；初始数据
使用 `InitialEntries`，会话切换使用 `ReplaceEntriesUpdate`。`modu_code` 不再维护一套
单独的 Message transcript 转换。

具体 ToolPresenter 位于 `cmd/modu_code/tui_tool_presenter.go`。它接收实时
ToolExecution、Assistant ToolCall 和历史 ToolResult，统一生成 ToolNode；
工具摘要由 presenter 负责，write/edit diff 和文件预览位于
`tui_tool_diff.go`，artifact、代码语言和参数解码位于 `tui_tool_data.go`。
`internal/tui` 只保留 ToolNodePresenter 接口，因为这些文件系统和 workflow
规则属于 modu_code 业务，不应进入 `pkg/modu-tui`。

Workflow 业务按职责分布在 `tui_workflow_view.go`、`tui_workflow_panels.go`、
`tui_workflow_insights.go`、`tui_workflow_details.go` 和
`tui_workflow_state.go`。Runtime 的
`map[string]any` 只在 `decodeModuTUIWorkflowSnapshot` 入口解码为 typed
snapshot、run、phase、agent 和 tool call；Cockpit、Feed、Map、Agent、
快捷键、默认选中项和 fingerprint 都消费同一份 typed 结构。

`tui_workflow_controller.go` 管理 panel 引用、刷新和 action 路由；
`tui_event_bindings.go` 只负责订阅 Agent/Session 事件。runner 不保存
workflow mutex、fingerprint 或刷新闭包。

所有 built-in slash definition 同时提供名称、别名、描述和执行操作。
执行结果通过 `moduTUISlashPrinter.Entry` 直接生成 Text/List 等标准 Node，
不再经过 `[]string -> strings.Join -> Presenter.Text` 的旧路径。

### DialogFlow

`/config`、`/channel`、`/model` 使用同一个 Dialog API。业务流程只写步骤、校验和 Hook 调用，不再重复实现 requestChoice、requestText、post 和 setStatus。

## 遗留 `pkg/tui`

`pkg/tui` 已于 2026-07-23 删除。删除前确认仓库内没有生产 import，
并确认项目不再为这套未使用实现保留外部兼容性。历史文章和进度记录里的
`pkg/tui` 路径描述的是当时实现，不代表当前包仍然存在。

后续不得重新建立第二套 Model、renderer 或 workflow UI；新增通用能力进入
`pkg/modu-tui`，产品业务进入 `cmd/modu_code/internal/tui`。

## 2026-07-23 落地状态

本次先完成能独立验收的纵向切片：

- `pkg/modu-tui` 增加 typed Intent、异步 Services 和宿主 Client。
- Model 状态按 transcript、composer、overlay、chrome 明确所有权；
  overlay 的互斥打开、按 ID 关闭和刷新选中项由一个组件处理。
- Render、Update 和 rebuild 不同步调用宿主 I/O；IntentHandler 在 `tea.Cmd`
  中执行。
- `modu_code` 增加 `internal/tui.Dialog`、标准 `Flow` 和 `IntentRouter`；
  config/channel/model 都提交相同的 choice/text step 数据，并消费统一的
  `FlowResult`，prompter、slash executor 和 channel printer 也都通过
  Client 访问 UI。
- `send func(tea.Msg)` 只保留在 `tui_client.go` 的运行时适配边界。
- 删除未使用的 `pkg/tui`。
- 增加 `Entry + Node` 标准展示数据、稳定 ID 的 Upsert/Remove/Replace 和统一
  `Update` 协议。
- Tool 和 Thinking 也可以通过 Node 输入，并复用原有生命周期与 renderer。
- Panel 支持结构化 Action；workflow 控制和导航已经用 typed payload 落地，
  同时保留旧 Command 兼容。
- `internal/tui.Presenter` 已接管 Dialog、slash、model selector 和 channel
  的普通文本输出。
- `internal/tui.CommandRegistry` 已统一全部静态 slash 命令的名称、别名、
  描述、补全、帮助和 handler 路由；运行时扩展、skill 和 prompt template
  也通过同一个动态解析入口执行，不再存在 nil-handler 兼容项、
  `executeLegacy`、runner fallback 或另一套 help 清单。
- `internal/tui.Runtime` 已接管 Prompt/Continue 循环、follow-up、steer、
  中断、前台计数和 busy/status 生命周期，runner 只提供 CodingSession 适配。
- `internal/tui.EventPresenter` 已接管实时 Agent/Session 事件顺序和标准
  Entry 映射；runner 的事件订阅只追加 Entry。
- 启动恢复和 `/resume` 已与实时事件共用 EventPresenter。
- ToolPresenter 已独立，三种工具输入直接生成 ToolNode；
  runner 中的工具摘要、artifact、代码/diff 和文件预览函数已迁出。
- Workflow panel、action 和 runtime decoder 已迁入 WorkflowView；runner
  从约 5400 行降到 226 行，workflow 页面不再直接解析 `state["runs"]`。
- runner 的 workflow controller、event bindings、channel、prompter、
  duration、shell state、tool-output 和 content helper 已拆出。
- workflow 视图按路由、面板、洞察、详情和状态解码拆分；tool presenter 按
  presenter、diff 和 data 拆分。
- slash 执行结果直接输出标准 `Entry/Node`，旧文本拼接层已删除。

阶段 4 已完成。Runtime、CommandRegistry、Presenter、DialogFlow、
ToolPresenter 和 WorkflowView 各自有明确入口和测试。

## 实施阶段

### 阶段 1：副作用边界

- 增加 typed Intent 和异步 dispatcher。
- IntentHandler 在 `tea.Cmd` 中执行。
- Dialog 响应统一通过 Cmd/Future，不在 Update 中启动不可控 goroutine。
- clipboard、paste resolver、artifact loader 异步化。
- RenderContext 不持有宿主回调。

验收：

- `Update` 和 `Render` 不直接执行宿主回调或文件 IO。
- 原有行为测试通过。
- `go test -race ./pkg/modu-tui` 通过。

### 阶段 2：根 Model 拆分

- 引入 transcript/composer/overlay/chrome 四个具体子模型。
- 迁移状态和处理函数。
- 保持最终 View 行为一致。

验收：

- 根 Model 只包含子模型和焦点路由状态。
- overlay 只有一套打开/关闭迁移。
- 各子模型有独立单元测试。

### 阶段 3：宿主 Facade

- 增加 Client。
- `modu_code` 的 wizard、model selector 和 runtime 改用 Client。
- 业务代码不再出现 `send func(tea.Msg)`。

### 阶段 4：业务拆分

- 提取 Runtime、CommandRegistry、EventPresenter、ToolPresenter、WorkflowView、DialogFlow。
- 每次只迁移一类职责，并保留原功能测试。

当前：阶段 4 已完成。runner 只负责启动装配，Runtime、CommandRegistry、
EventPresenter、ToolPresenter、WorkflowController、WorkflowView 和标准
Flow 都有独立职责与测试入口。

### 阶段 5：清理

- 删除遗留 `pkg/tui`。
- 删除旧 Message、Hooks、旧具体更新消息和对应测试入口。
- 更新 `pkg/modu-tui/README.md`、`modu_code` 指南和进度记录。

阶段 5 已完成。

## 完成判据

- `pkg/modu-tui` 没有业务依赖。
- `Update`、Render 和 rebuild 是无外部副作用的状态计算。
- `modu_code` 业务代码不依赖 Bubble Tea 消息类型。
- slash 命令定义只有一份。
- workflow runtime state 只有一个 decoder。
- `runModuTUI` 只负责装配，运行、命令和 Presenter 分属独立模块。
- 仓库只剩 `pkg/modu-tui` 一套输入、状态和渲染实现。
- `go test ./pkg/modu-tui ./cmd/modu_code`、race 测试和相关集成测试通过。
- 使用 PTY 验证启动、输入、流式输出、退出和终端恢复；IME、图片、审批、
  resize、鼠标选择、SSH/OSC52、follow-up、steer 和 workflow panel 由对应
  Model、Runtime、Intent、Panel 测试覆盖。

## 非目标

- 本轮不增加主题市场、布局 DSL 或通用组件注册系统；没有第二个需求证明这些抽象值得存在。
- 本轮不改变用户可见快捷键和 TUI 样式。
- 本轮不把 CodingSession 或 workflow 类型放进 `pkg/modu-tui`。
- 不用机械拆文件数量代替职责拆分；状态所有权和依赖方向必须真实改变。
