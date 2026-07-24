# 如何在面试中讲清 Modu

这是一份讲解提纲，不是实现规格。Modu 仍在迭代，模块、默认值和代码位置会变化；面试前必须用当前源码和测试重新核对。不要背诵旧数字，也不要把没有亲自验证的能力说成自己的贡献。

## 先用一句话说明定位

Modu 是用于构建 Agent 应用的 Go 工具库，提供 Agent 循环、LLM Provider 适配、工具执行、多 Agent 协作、消息通道、定时调度和终端 UI 组件。应用仍需自行定义 Prompt、工具、持久化策略和部署方式。

这句话刻意没有把 Modu 说成“完整平台”：仓库提供的是可组合模块和几个可运行应用，不负责替使用方决定业务流程。

## 用一张图讲清层次

```text
应用与入口
  cmd/modu_code · cmd/modu_eval · cmd/acp-gateway · examples
        │
Agent 能力
  pkg/coding_agent · pkg/agent · pkg/runtime
        │
协作与调度
  pkg/mailbox · pkg/cron · pkg/channels
        │
模型与协议
  pkg/providers · pkg/provider · pkg/acp
        │
公共契约和界面
  pkg/types · pkg/stream · pkg/modu-tui
```

这不是编译依赖图，而是讲解顺序。真正的依赖关系以 `go list -deps` 和各包 import 为准。

## 四个值得展开的设计

### Agent 循环：把状态门面和执行循环分开

`pkg/agent.Agent` 对外提供 `Prompt`、状态访问、事件订阅、`Steer`、`FollowUp` 和中断恢复等能力。循环所需的模型调用、工具执行、事件发送和运行时回调通过 `types.LoopInput` 传入。

面试时不要只说“用了 ReAct”。更有信息量的讲法是：

> Agent 持有会话状态，但循环通过输入结构接收依赖，因此工具执行、模型调用和中断策略都能在测试中替换。外部界面订阅事件，不需要把 TUI 或日志逻辑写进循环。

可以继续解释两个队列的语义：

- `Steer` 用于改变当前任务方向，优先于普通后续消息。
- `FollowUp` 等当前任务结束后再处理，不应被误解成中断。

判据很简单：消息要求当前工作立即转向时用 Steer；只是补充下一件事时用 FollowUp。

### Provider：在边界处吸收厂商差异

`pkg/providers.Provider` 接收统一的 Chat 请求，返回统一的响应或事件流。OpenAI、Anthropic、DeepSeek、Gemini 及 OpenAI 兼容服务的协议差异由各自适配器处理。

值得讲清的不是“支持很多模型”，而是 ID 如何连起来：注册 Provider 时使用的 id，必须与 `types.Model.ProviderID` 一致。Agent 根据这个 id 找到实现，上层不需要判断厂商。

代价也要说：统一接口只能覆盖共同语义。厂商独有能力如果没有进入公共类型，就需要扩展契约或留在具体 Provider 层，不能假装已经无损抽象。

### Mailbox：把协作落到消息和任务状态

`pkg/mailbox` 负责 Agent 注册、消息收发、Project、Task、事件订阅和结果验证。它支持进程内 Hub，也提供 Redis 协议相关的跨进程组件；持久化通过 Store 接口接入 SQLite 等实现。

可以用两种协作模式解释为什么需要 Task 状态：

- Agent Teams：协调者分派任务并汇总结果，适合角色和依赖明确的工作。
- Adversarial Validation：Worker 提交结果，Validator 独立判定通过、重试或失败。

不要再把已经删除的 `pkg/swarm` 说成当前模块。队列认领和验证逻辑现在位于 `pkg/mailbox`；具体行为应从 `hub_swarm.go`、`hub_validation.go` 及其测试核对。

### Coding Agent：在通用 Agent 上组装产品能力

`pkg/coding_agent` 组合会话、system prompt、上下文管理、压缩、审批、重试、MCP、memory、计划、todo、worktree 和编程工具。`cmd/modu_code` 再提供交互 TUI、print、RPC 和 ACP server 等入口。

这里最容易说过头。不要背“固定有几个工具”之类的数字：工具会随版本和配置变化。更稳妥的讲法是列出职责类别，并让面试官看目录：

```text
pkg/coding_agent/tools/      文件、搜索、Shell、规划、memory、worktree 等工具
pkg/coding_agent/services/   session、compaction、approval、MCP 等服务
pkg/coding_agent/plugins/    extension、prompt 和 subagent 扩展点
```

## 常见追问

### 为什么选 Go？

短回答：Agent 主要处理流式网络 IO、工具子进程和并发事件，Go 的 goroutine、context 和接口适合表达这些边界；单二进制也降低了 CLI 分发成本。

这不是说 Python 做不了。Python 的模型生态和实验速度更有优势；Modu 选择 Go，是把并发控制、类型约束和部署形态放在更高优先级。

### 如何避免事件订阅拖慢主循环？

回答前先区分模块。`pkg/agent`、`pkg/mailbox` 和 `cmd/acp-gateway` 有各自的订阅与缓冲策略，不能用一个结论概括全部实现。

以 Gateway 为例，Turn 最多缓存 256 条 SSE 事件；向慢订阅者发送时不会阻塞执行线程，因此慢客户端可能漏掉中间事件。最终 Turn 状态和结果仍单独保存。这个取舍优先保证 Agent 执行不被 UI 拖住。

### 如何处理模型的瞬态错误？

`pkg/agent/llm.go` 区分可重试错误和永久错误，并在重试之间加入退避与抖动。具体次数和最大延迟来自配置，不要口述未经核对的默认值。

验证方法比背默认值更可靠：看 `completeWithRetry` 的实现，再看 `loop_test.go` 中瞬态错误、永久错误和 context 取消的测试。

### 上下文太长怎么办？

`pkg/coding_agent/services/compaction` 负责压缩服务，工具结果还有独立的截断和 artifact 机制。面试时应说明两者解决不同问题：

- 工具结果截断控制单次工具输出进入模型的大小。
- 对话压缩减少长期会话历史占用。

不要承诺“永远不会超限”。模型窗口、估算误差和一次请求的突发内容都可能让请求失败，调用方仍要处理错误。

### ACP Gateway 如何隔离会话？

Gateway 的每个 Session 有独立 key。`pkg/acp/manager.ProviderKeyed` 用 `agentID + gatewaySessionID` 缓存 Provider，所以两个 Session 即使指向同一工作目录，也不会共享 ACP 对话。

代价是进程数可能随活跃 Session 增长。部署时要结合 `-workers`、Session 生命周期和 Agent 资源占用设置边界。

### 你贡献了什么？

只回答你能拿出 diff、测试或设计记录的部分。一个可信回答至少包含：问题、你做的选择、被否决的方案、验证方式和已知限制。

如果只是阅读或使用了项目，就明确说“我分析了”或“我集成了”，不要说“我设计并实现了”。

## 面试前的核对清单

先运行不依赖外部 LLM 的测试：

```bash
go test ./pkg/agent/...
go test ./pkg/mailbox/...
go test ./pkg/acp/...
go test ./cmd/acp-gateway
```

再亲自运行至少一个示例：

```bash
go run ./examples/agent_demo
go run ./examples/agent_teams
```

需要模型服务的示例可能因本地配置失败，这也是必须提前验证的边界。演示前确认 API key、base URL、模型 id 和工作目录，不要在面试现场第一次运行。

最后检查这些来源：

| 要讲的内容 | 先看哪里 |
|---|---|
| Agent 主循环和重试 | `pkg/agent/loop.go`、`pkg/agent/llm.go` 及测试 |
| Steer / FollowUp | `pkg/agent/queue.go` |
| Provider 契约 | `pkg/providers/provider.go` 与各 Provider README |
| Mailbox 任务和验证 | `pkg/mailbox/hub_task.go`、`hub_validation.go` 及测试 |
| Coding Agent 组成 | `pkg/coding_agent/README_zh.md` 与 `services/`、`tools/` |
| ACP Gateway | [`ACP 架构`](../architecture/acp.md) 与 [`API 参考`](../reference/acp-gateway-api.md) |

能从源码和测试推导出的结论才适合放进面试回答。数字、默认值和“支持某能力”的说法如果现场无法指出证据，就删掉。
