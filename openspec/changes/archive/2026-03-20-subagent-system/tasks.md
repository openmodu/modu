## 1. Subagent 定义包（pkg/coding_agent/subagent/）

- [x] 1.1 创建 `pkg/coding_agent/subagent/definition.go`：定义 `SubagentDefinition` 结构体（Name、Description、Tools []string、Model string、SystemPrompt string）和 `ParseDefinition(path string) (*SubagentDefinition, error)` 函数，复用 skills frontmatter 解析逻辑
- [x] 1.2 创建 `pkg/coding_agent/subagent/loader.go`：实现 `Loader` 结构体，`Discover(agentDir, cwd string)` 从全局和项目 agents/ 目录加载所有 `.md` 文件；项目定义覆盖全局；目录不存在时静默跳过
- [x] 1.3 创建 `pkg/coding_agent/subagent/runner.go`：实现 `Run(ctx, def, task, allTools, model, getAPIKey)` 函数，构造 `agent.Agent`，按 def.Tools 过滤工具集，以 task 为用户消息调用 `agent.Prompt`，返回最后一条 AssistantMessage 的文本；未知工具名 warn 并跳过

## 2. spawn_subagent 工具

- [x] 2.1 创建 `pkg/coding_agent/tools/spawn_subagent.go`：实现 `SpawnSubagentTool` 结构体（持有 `*subagent.Loader`、`allTools []agent.AgentTool`、`model *types.Model`、`getAPIKey func`）
- [x] 2.2 实现 `AgentTool` 接口（Name/Label/Description/Parameters/Execute）；Parameters 包含 `name`（string，必填）和 `task`（string，必填）两个字段
- [x] 2.3 在 `Execute` 中：按 name 查找 subagent 定义 → 不存在则返回错误内容；存在则调用 `subagent.Run`，将 ctx 传递下去；运行期间通过 `onUpdate` 推送 `"Running subagent <name>…"` 进度；完成后返回结果文本

## 3. 集成到 CodingSession

- [x] 3.1 在 `pkg/coding_agent/coding_agent.go` 的 `NewCodingSession` 中：初始化 `subagent.Loader`，调用 `Discover(agentDir, cwd)`
- [x] 3.2 若 loader 发现了至少一个定义，创建 `SpawnSubagentTool` 并追加到 `activeTools`；将已加载的 subagent 列表注入 system prompt（XML 格式，类似 skills）

## 4. 示例 subagent 定义文件

- [x] 4.1 创建 `examples/modu_code/agents/code-reviewer.md`：示例 subagent，tools: read, grep, find, ls，附带代码审查 system prompt

## 5. 验证

- [x] 5.1 `go build ./...` 通过，无编译错误
- [x] 5.2 手动测试：在 modu_code 中调用 `spawn_subagent`，验证 subagent 运行并返回结果
- [x] 5.3 测试并行：在同一轮提示中让主 agent 并行调用两次 `spawn_subagent`，验证均正常返回
