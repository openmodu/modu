# ACP 后续工作与验收基线

ACP 的协议栈和 Gateway 主路径已经进入源码；当前重点不是继续增加模块，而是让端到端验收重新对应 Session / Turn API，并收紧安全边界。

当前架构见 [`ACP 集成架构`](../architecture/acp.md)，HTTP 契约见 [`acp-gateway API`](../reference/acp-gateway-api.md)。本文只记录能够从当前仓库确认的状态和下一步验收条件，不给未经测量的工时。

## 当前状态

| 能力 | 代码位置 | 当前证据 |
|---|---|---|
| JSON-RPC 2.0 消息 | `pkg/acp/jsonrpc` | 单元测试覆盖序列化、判别和错误 |
| ACP 子进程传输 | `pkg/acp/process` | Unix、Windows 实现及进程测试 |
| 请求、通知和反向 RPC | `pkg/acp/client` | 内存 Transport 测试覆盖请求关联、权限和文件请求 |
| ACP 事件翻译 | `pkg/acp/bridge` | `testdata` fixture 与翻译测试 |
| Modu Provider 适配 | `pkg/acp/provider` | mock Agent 测试覆盖初始化、Session、流式输出和取消 |
| Agent 配置与实例管理 | `pkg/acp/manager` | 配置校验、延迟创建、运行时管理测试 |
| HTTP + SSE Gateway | `cmd/acp-gateway` | Handler、鉴权、状态、权限和 TokenKit 测试 |
| 无外部模型的 mock Agent | `examples/acp_e2e/mock_acp_agent` | 可作为端到端测试进程 |

“代码和测试存在”不等于真实 Agent 已验证。Claude Code、Codex 或 Gemini 的 ACP adapter 版本、认证方式和协议兼容性仍需在目标环境手工确认。

## P0：补齐端到端验收

仓库保留了 `examples/acp_e2e/mock_acp_agent`，但没有驱动当前 Project / Session / Turn API 的自动化端到端测试。现有包测试不能替代真实 Gateway 进程、SSE 和反向权限请求的组合验证。

迁移目标：

1. 创建 Project：`POST /api/projects`。
2. 创建 Session：`POST /api/sessions`。
3. 提交 Turn：`POST /api/sessions/{sessionId}/turns`。
4. 从 `/stream` 断言文本增量和最终 `completed` 状态。
5. 从 `permission` SSE 帧取得 `toolCallId`，通过 `/approve` 选择 option。
6. 保留未带 Bearer token 返回 `401` 的用例。

完成标准：新增的自动化测试在 macOS 和 Linux 上退出 0，不需要网络或 LLM API key，并且只调用当前 API 参考中列出的路由。

## P1：定义 system prompt 的写入策略

Gateway Profile 的 system prompt 会在 ACP Provider 第一次启动前写入工作目录：

| Agent id | 文件 |
|---|---|
| `claude` | `CLAUDE.md` |
| `codex` | `AGENTS.md` |
| `gemini` | `GEMINI.md` |

当前使用 `os.WriteFile`，会覆盖同名文件。这个行为对已有项目有数据破坏风险，不能只靠 API 文档提示长期维持。

后续实现前需要先确定一种明确策略：

- 不写文件，把 system prompt 作为用户消息前缀；优点是不改工作区，缺点是语义不等同于 Agent 原生指令文件。
- 写临时或组合后的上下文文件，并在结束时恢复；优点是保留 Agent 原生机制，缺点是崩溃恢复和并发 Session 更复杂。
- 检测同名文件后拒绝启动；优点是不会覆盖数据，缺点是 Profile 在常见仓库中可能无法使用。

完成标准：选定方案和拒绝理由写入架构文档；增加“文件已存在”“两个 Session 同目录”和“进程异常退出”测试；在这些测试通过前，不宣称 Profile 写入是安全的。

## P1：补齐真实 Agent 兼容性记录

Mock Agent 只能证明 Modu 内部的数据流，不能证明第三方 adapter 的当前版本兼容。

每个准备支持的 Agent 至少记录：

- adapter 包名与精确版本；
- 启动命令和必需环境变量；
- `initialize`、`session/new`、`session/prompt` 是否通过；
- 文本流、工具事件、取消和权限请求是否通过；
- 测试日期、操作系统和失败限制。

完成标准：每个 Agent 有一份可重复的手工验证记录。没有验证的 Agent 可以出现在配置示例中，但必须标注“未验证”，不能列为稳定支持。

## P2：明确事件恢复语义

Turn 的最终状态和结果可以写入 SQLite，但 SSE 事件历史只保存在进程内存中，且每个 Turn 最多 256 条。Gateway 重启后，客户端无法恢复旧的流式增量。

在实现持久化事件前，先决定产品语义：

- 客户端只保证拿到最终 Turn，增量允许丢失；或
- 客户端必须按事件序号断点续传，Gateway 持久化并去重事件。

完成标准：API 文档写明选择；若选择断点续传，测试必须覆盖断线重连、Gateway 重启、重复游标和超过保留上限。

## P2：限制文件浏览和动态 Agent 管理

`GET /api/browse` 能列出 Gateway 进程有权读取的任意绝对目录；动态 Agent API 能配置要执行的命令和环境变量。Bearer token 是当前唯一应用层边界。

在把 Gateway 暴露到不完全可信的网络前，至少完成：

- 限制监听地址或部署在可信网络中；
- 为 `/api/browse` 设置允许的根目录；
- 决定是否关闭动态 Agent 增删改；
- 确认日志和 API 响应不会回显 Agent 密钥。

完成标准：增加根目录逃逸和未授权访问测试；部署文档给出可执行配置，而不是只写“注意安全”。

## 不在本计划内

- iOS UI：Gateway 只维护 HTTP 契约。
- 把 `modu_code --acp` 与 `pkg/acp` 合成同一进程模型：两者分别是 ACP server 和 client，职责不同。
- 未经用例驱动的路由或抽象层：先用真实客户端或测试说明问题，再扩展 API。

## 每次变更的最低验证

```bash
go test ./pkg/acp/...
go test ./cmd/acp-gateway
```

改动 HTTP 路由、SSE、Session 隔离或权限流程时，还必须运行修复后的端到端脚本。改动配置或用户可见行为时，同步更新架构和 API 参考。
