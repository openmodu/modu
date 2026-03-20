## ADDED Requirements

### Requirement: Subagent definition file format
Subagent 定义文件 SHALL 是 markdown 文件，包含 YAML frontmatter（name、description、tools、可选 model）和正文（system prompt）。

#### Scenario: Valid definition file is parsed
- **WHEN** loader 读取一个包含完整 frontmatter 的 `.md` 文件
- **THEN** 解析出 `SubagentDefinition`，包含 name、description、tools 列表、可选 model 字段和 system prompt 正文

#### Scenario: Missing name falls back to filename
- **WHEN** frontmatter 中没有 `name` 字段
- **THEN** 使用文件名（去掉 `.md` 扩展名）作为 name

#### Scenario: Missing tools defaults to empty (no tools)
- **WHEN** frontmatter 中没有 `tools` 字段
- **THEN** subagent 以空工具集运行（只能生成文本，不能调用工具）

### Requirement: Subagent discovery from global and project directories
Loader SHALL 从 `{agentDir}/agents/` 和 `{cwd}/.coding_agent/agents/` 两个目录发现 subagent 定义文件。

#### Scenario: Global agents directory is loaded
- **WHEN** `~/.coding_agent/agents/` 存在并包含 `.md` 文件
- **THEN** 所有 `.md` 文件被加载为 subagent 定义

#### Scenario: Project agents override global
- **WHEN** 同名 subagent 在全局和项目目录均存在
- **THEN** 项目目录的定义覆盖全局定义

#### Scenario: Missing directory is silently skipped
- **WHEN** `agents/` 目录不存在
- **THEN** loader 不报错，返回空列表

### Requirement: Tool name resolution
Loader SHALL 根据 frontmatter `tools` 字段（逗号分隔工具名）从 `tools.AllTools(cwd)` 中过滤出对应工具。

#### Scenario: Valid tool names are resolved
- **WHEN** frontmatter 中 `tools: read, grep, ls`
- **THEN** subagent 获得对应的 3 个工具实例

#### Scenario: Unknown tool name is warned and skipped
- **WHEN** frontmatter 中包含不存在的工具名（如 `tools: read, nonexistent`）
- **THEN** 已知工具正常加载，未知工具被跳过（不阻塞加载）
