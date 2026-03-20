## Context

modu_code 基于 `pkg/agent.Agent`（核心 LLM 调用循环）和 `pkg/coding_agent.CodingSession`（工具、技能、会话管理封装）构建。现有 `spawn_agent_tool.go` 通过 Redis mailbox 委托外部 agent，需要额外基础设施，不适合独立 CLI 场景。

skills 系统已有 markdown frontmatter 解析逻辑可以复用。`agent.AgentTool` 接口（Name/Label/Description/Parameters/Execute）是新工具的唯一约束。

## Goals / Non-Goals

**Goals:**
- 用户可在 markdown 文件中定义 subagent（tools、model、system prompt）
- 主 agent 通过工具调用在独立 Go goroutine + `agent.Agent` 实例中运行 subagent
- Subagent 的最终文本输出作为工具结果返回
- 多个 subagent 可并行运行（每次工具调用独立 goroutine）
- 零新外部依赖

**Non-Goals:**
- Subagent 之间互相通信
- Subagent 递归嵌套（subagent 不能再 spawn subagent）
- 持久化 subagent 状态（每次调用是全新 Agent 实例）
- TUI 单独展示 subagent 输出流（结果作为整体返回）

## Decisions

### 决策 1：In-process goroutine，而非外部进程

**选择**：每个 subagent 在同一进程内用独立 `agent.Agent` 实例 + goroutine 运行。

**备选**：外部进程（exec.Command）或基于 mailbox 的委托。

**理由**：无需 Redis/额外基础设施；共享同一 API key 和 provider 配置；goroutine 并行天然支持；进程内调用延迟低。

---

### 决策 2：Subagent 定义复用 skills frontmatter 格式

**选择**：`pkg/coding_agent/subagent/` 新包，复用 skills 的 frontmatter 解析模式（不直接复用 Manager，因 subagent 有不同字段）。

**理由**：用户已熟悉 skills markdown 格式；解析逻辑简单，约 30 行，没必要抽象共享。

**Frontmatter 字段**：
```yaml
name: code-reviewer
description: Expert at reviewing Go code for correctness and style.
tools: read, grep, find, ls     # 逗号分隔，对应 AllTools 名称
model: claude-sonnet-4-6        # 可选，默认继承主 agent model
```
正文 = subagent system prompt。

---

### 决策 3：工具名 `spawn_subagent`，区别于现有 `spawn_agent`

**理由**：现有 `spawn_agent` 用于 mailbox 外部委托，含义不同，避免混淆。

---

### 决策 4：Subagent tools 按名称过滤 AllTools

**选择**：frontmatter `tools` 字段列出工具名，loader 在运行时从 `tools.AllTools(cwd)` 中过滤。

**理由**：不需要 subagent 关心工具构造细节；工具集与主 agent 保持一致。

---

### 决策 5：结果提取——取最后一条 AssistantMessage 的文本

**选择**：subagent 运行完后，从 `agent.GetState().Messages` 中取最后一条 `AssistantMessage` 的文本内容作为结果。

**理由**：简单直接；避免流式传输复杂度；主 agent 只需最终答案。

## Risks / Trade-offs

- **风险：Subagent 运行时间过长** → Mitigation：`Execute` 使用传入的 `ctx`，主 agent 可通过 cancel 中止；可在工具参数中加可选 `timeout_seconds`。
- **风险：Subagent 工具名拼写错误导致空工具集** → Mitigation：loader 在发现阶段 warn 无法识别的工具名，但不阻塞加载（容错）。
- **权衡：Subagent 输出不流式展示** → 用户在 subagent 运行期间看不到中间输出。可通过 `onUpdate` 回调定期推送进度缓解，但 MVP 阶段暂不实现。

## Migration Plan

纯新增功能，无迁移需求。`agents/` 目录不存在时 loader 静默跳过，不影响现有行为。

## Open Questions

- Subagent 是否需要继承主 agent 的对话历史（messages）？当前决策：不继承，每次全新上下文，更安全且简单。
