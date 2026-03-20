## ADDED Requirements

### Requirement: spawn_subagent tool is registered in CodingSession
`spawn_subagent` 工具 SHALL 在 `CodingSession` 初始化时自动注册到主 agent 的工具列表中（当至少发现一个 subagent 定义时）。

#### Scenario: Tool is available when agents directory has definitions
- **WHEN** `agents/` 目录下存在至少一个有效的 subagent 定义文件
- **THEN** 主 agent 的工具列表中包含 `spawn_subagent`

#### Scenario: Tool is omitted when no definitions exist
- **WHEN** `agents/` 目录不存在或为空
- **THEN** 主 agent 工具列表中不包含 `spawn_subagent`（避免干扰模型）

### Requirement: spawn_subagent tool executes named subagent in independent context
调用 `spawn_subagent` 工具 SHALL 在独立 `agent.Agent` 实例和 goroutine 中运行指定 subagent，使用其 system prompt 和工具集。

#### Scenario: Named subagent runs with its own system prompt and tools
- **WHEN** 主 agent 调用 `spawn_subagent`，参数 `name="code-reviewer"`，`task="review pkg/foo"`
- **THEN** 创建一个新 `agent.Agent`，使用 code-reviewer 的 system prompt 和工具集，以 `task` 内容作为用户消息运行

#### Scenario: Unknown subagent name returns error result
- **WHEN** 主 agent 调用 `spawn_subagent`，参数 `name` 不存在于已加载定义中
- **THEN** 工具返回错误内容（不 panic），主 agent 收到错误说明

#### Scenario: Result is the final assistant text output
- **WHEN** subagent 运行完毕
- **THEN** 工具结果为 subagent 最后一条 `AssistantMessage` 的文本内容

### Requirement: Multiple subagents can run in parallel
每次 `spawn_subagent` 工具调用 SHALL 独立运行，互不阻塞，支持主 agent 并行发起多次调用。

#### Scenario: Two concurrent spawn_subagent calls both complete
- **WHEN** 主 agent 在同一轮 tool call 中发起两次 `spawn_subagent`（不同 name 或不同 task）
- **THEN** 两个 subagent 各自在独立 goroutine 中运行，均正常返回结果

### Requirement: Subagent inherits API key and provider from parent session
Subagent SHALL 使用与主 `CodingSession` 相同的 `getAPIKey` 函数和 provider 配置。

#### Scenario: Subagent can call the LLM without separate auth config
- **WHEN** subagent 运行时
- **THEN** 使用 `CodingSession.getAPIKey` 获取 API key，无需用户额外配置

### Requirement: Context cancellation propagates to subagent
`spawn_subagent` 工具 SHALL 将 `Execute` 接收到的 `ctx` 传递给 subagent 的 `Prompt` 调用。

#### Scenario: Cancelling parent context stops subagent
- **WHEN** 主 agent 的 context 被 cancel（如用户按 Ctrl+C）
- **THEN** subagent 的 LLM 请求随之中止
