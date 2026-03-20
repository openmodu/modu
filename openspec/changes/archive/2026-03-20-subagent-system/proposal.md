## Why

modu_code 目前只支持单一 agent 运行，无法将复杂任务分解给专门化的子 agent 并行处理。当任务涉及多个独立子问题（如同时搜索代码、运行测试、分析文档）时，只能串行执行，效率低且主 agent 上下文容易膨胀。

## What Changes

- 新增 subagent 定义文件格式：markdown + YAML frontmatter，支持自定义 name、description、tools、model 和 system prompt
- 新增 subagent 加载器：从 `~/.coding_agent/agents/` 和 `.coding_agent/agents/` 发现并加载定义
- 新增 `spawn_subagent` 工具：主 agent 可通过该工具在独立 goroutine + 独立 `agent.Agent` 实例中启动子 agent（in-process，不依赖 Redis/mailbox）
- Subagent 执行完成后将最终文本结果返回给主 agent 作为工具结果
- 支持多个 subagent 并行运行（每次工具调用独立 goroutine）
- 集成到 `CodingSession`：自动发现并注册 `spawn_subagent` 工具

## Capabilities

### New Capabilities

- `subagent-definition`: Markdown 文件格式，定义 subagent 的元数据（name、description、tools、model）和 system prompt 正文，支持从全局和项目目录加载
- `spawn-subagent-tool`: `AgentTool` 实现，主 agent 调用后在独立上下文中运行指定 subagent，等待结果并返回；支持并行调用

### Modified Capabilities

（无现有 spec 级别变更）

## Impact

- **新增包**：`pkg/coding_agent/subagent/`（definition.go、loader.go、runner.go）
- **新增文件**：`pkg/coding_agent/tools/spawn_subagent.go`
- **修改文件**：`pkg/coding_agent/coding_agent.go`（集成 subagent loader，注册工具）
- **无新外部依赖**：复用现有 `agent.Agent`、`AgentTool` 接口和 skills 的 frontmatter 解析逻辑
- **无破坏性变更**：新功能为可选增量，现有行为不受影响
