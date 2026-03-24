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
│  │                 工具 (7 个)                        │   │
│  │  read │ write │ edit │ bash │ grep │ find │ ls    │   │
│  │          ▲ (如果存在 hook 则使用 WrappedTool)      │   │
│  └──────────┼───────────────────────────────────────┘   │
│             │                                            │
│  ┌──────────┴──────┐  ┌────────────┐  ┌────────────┐   │
│  │      扩展       │  │    会话    │  │    压缩    │   │
│  │ (Hook/Command)  │  │  (JSONL)   │  │ (摘要压缩)  │   │
│  └─────────────────┘  └────────────┘  └────────────┘   │
│                                                         │
│  ┌─────────────────┐  ┌────────────────────────────┐   │
│  │    技能管理器   │  │   斜杠命令 (7 个)           │   │
│  └─────────────────┘  └────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

## 目录结构

```
pkg/coding_agent/
├── coding_agent.go           # CodingSession 主入口，串联所有子系统
├── config.go                 # 配置管理（全局 + 项目级 settings.json）
├── system_prompt.go          # 系统提示词组装器
├── messages.go               # 自定义消息类型（Bash/Compaction/Branch/Custom）
├── slash_commands.go         # 内置斜杠命令（/model, /compact, /tree 等）
├── tools/                    # 7 个内置编码工具
│   ├── read.go               #   文件读取（行号、分页、图片 base64）
│   ├── write.go              #   文件写入（自动建目录）
│   ├── edit.go               #   精确替换编辑（歧义检测、replace_all、diff）
│   ├── bash.go               #   Shell 命令执行（超时、进程组 kill）
│   ├── grep.go               #   内容搜索（rg 优先，Go 内置回退）
│   ├── find.go               #   文件查找（fd 优先，Go 内置回退）
│   ├── ls.go                 #   目录列表（大小写不敏感排序）
│   ├── truncate.go           #   输出截断（Head/Tail/Line）
│   ├── path_utils.go         #   路径解析（~展开、NFD/NFC 兼容）
│   └── tools.go              #   工具集合工厂（AllTools/CodingTools/ReadOnlyTools）
├── session/                  # 会话持久化
│   ├── entry.go              #   会话条目定义（9 种 EntryType）
│   ├── manager.go            #   JSONL 文件存储 + Fork
│   └── tree.go               #   树形分支导航
├── compaction/               # 上下文压缩
│   ├── compaction.go         #   滑动窗口 + LLM 摘要压缩
│   └── branch_summary.go    #   分支跳转上下文摘要
├── extension/                # 扩展系统
│   ├── types.go              #   Extension/ExtensionAPI 接口、ToolHook
│   ├── loader.go             #   扩展注册
│   ├── runner.go             #   扩展生命周期管理（实现 ExtensionAPI）
│   └── wrapper.go            #   WrappedTool 透明包装（Before/After/Transform）
├── skills/                   # 技能系统
│   ├── skills.go             #   技能发现与加载（Markdown + YAML frontmatter）
│   └── prompt_templates.go   #   提示模板展开（{{args}} 替换）
└── resource/                 # 资源加载
    └── loader.go             #   上下文文件发现（AGENTS.md 等）+ 目录初始化
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

### 2. 7 个内置编码工具

| 工具 | 功能 | 关键特性 |
|------|------|----------|
| `read` | 文件读取 | 行号格式化、offset/limit 分页、图片自动 base64 |
| `write` | 文件写入 | 自动创建父目录、返回写入字节数 |
| `edit` | 精确编辑 | 精确字符串匹配替换、歧义检测、replace_all、CRLF 兼容 |
| `bash` | 命令执行 | 可配置超时（默认 120s）、进程组级 kill、输出尾部截断 |
| `grep` | 内容搜索 | 优先使用 ripgrep、Go 内置正则回退、忽略 .git 等目录 |
| `find` | 文件查找 | 优先使用 fd、Go 内置 glob 回退、尊重 .gitignore |
| `ls` | 目录列表 | 大小写不敏感排序、目录后缀 `/`计、可限制条数 |

工具集合工厂函数：

```go
tools.AllTools(cwd)       // 全部 7 个工具
tools.CodingTools(cwd)    // read, bash, edit, write
tools.ReadOnlyTools(cwd)  // read, grep, find, ls
```

### 3. Hook 系统（扩展钩子）

通过 Extension 机制注册 `ToolHook`，透明拦截所有工具调用：

```go
type ToolHook struct {
    Before    func(toolName string, args map[string]any) bool              // 返回 false 阻止执行
    After     func(toolName string, args map[string]any, result AgentToolResult) // 执行后审计
    Transform func(toolName string, result AgentToolResult) AgentToolResult      // 修改返回结果
}
```

典型用途：
- **安全防护**：Before hook 拦截危险命令（如 `rm -rf /`）
- **审计日志**：After hook 记录所有工具调用
- **结果变换**：Transform hook 对输出添加水印或脱敏

所有工具通过 `extension.WrapTools()` 统一包装，对 Agent 完全透明。

### 4. 自动上下文压缩（Auto Compaction）

当累计 token 用量超过模型 context window 的阈值百分比时，自动触发压缩：

```
Agent 完成回复 → 累加 token 用量 → 超过阈值？→ 调用 Compact()
                                                    │
                    ┌───────────────────────────────┘
                    ▼
        旧消息 → LLM 生成摘要 → [摘要消息] + [保留的最近消息]
```

- 默认阈值：context window 的 80%
- 默认保留最近 4 条消息
- 无 LLM 可用时回退为直接截断
- 压缩后自动重置 token 计数器
- 可通过 `config.AutoCompaction = false` 关闭
- 支持 `/compact` 手动触发

### 5. 自动重试（Auto Retry）

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

### 6. 会话持久化与分支

基于 JSONL 的树形会话存储：

```
~/.coding_agent/sessions/<project-hash>/session.jsonl
```

每条记录包含 `id` + `parentId`，支持：
- **自动持久化**：消息、模型变更、压缩操作自动记录
- **Fork 分支**：从历史任意点创建分支，探索不同路径
- **树形导航**：`/tree` 查看分支结构，`/fork <id>` 创建分支
- 9 种条目类型：message、modelChange、compaction、branchSummary、sessionInfo 等

### 7. 扩展系统

通过 Go 接口注入方式注册扩展：

```go
type Extension interface {
    Name() string
    Init(api ExtensionAPI) error
}
```

扩展能力：
- 注册自定义工具（`RegisterTool`）
- 注册斜杠命令（`RegisterCommand`）
- 注册工具钩子（`AddHook`）
- 注入对话消息（`SendMessage`）
- 控制工具开关（`SetActiveTools`）
- 切换模型（`SetModel`）

### 8. 技能与模板系统

**技能**（Skills）：从 `~/.coding_agent/skills/` 和 `.coding_agent/skills/` 目录发现 Markdown/Text 文件，支持 YAML frontmatter 定义名称、描述、标签，注入系统提示词。

**模板**（Templates）：从 `~/.coding_agent/prompts/` 和 `.coding_agent/prompts/` 加载，通过 `/templatename args` 调用，自动展开 `{{args}}` 占位符。

### 9. 输出截断

防止超长输出撑爆上下文：

| 函数 | 用途 | 默认限制 |
|------|------|----------|
| `TruncateHead` | 保留前 N 行（read 工具用） | 2000 行 |
| `TruncateTail` | 保留后 N 行（bash 工具用） | 2000 行 |
| `TruncateLine` | 单行字符截断（grep 工具用） | 500 字符 |

### 10. 内置斜杠命令

| 命令 | 功能 |
|------|------|
| `/model <provider> <id>` | 切换模型 |
| `/compact` | 手动触发上下文压缩 |
| `/tree` | 显示会话分支结构 |
| `/fork <id>` | 从指定条目创建分支 |
| `/settings` | 显示当前配置 |
| `/tools` | 显示当前活跃工具 |
| `/help` | 显示帮助信息 |

## 快速开始

```go
package main

import (
    "context"
    "fmt"

    coding_agent "github.com/openmodu/modu/pkg/coding_agent"
    "github.com/openmodu/modu/pkg/coding_agent/tools"
    "github.com/openmodu/modu/pkg/providers"
    "github.com/openmodu/modu/pkg/types"
    "github.com/openmodu/modu/pkg/agent"
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
        Tools: tools.AllTools("/your/project"),
        GetAPIKey: func(provider string) (string, error) {
            return "", nil
        },
    })
    if err != nil {
        panic(err)
    }

    // 订阅事件
    session.Subscribe(func(event agent.AgentEvent) {
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
    runner := api.(*extension.Runner)
    runner.AddHook(extension.ToolHook{
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
  "compactionSettings": {
    "maxContextPercentage": 80.0,
    "preserveRecentMessages": 4
  },
  "customSystemPrompt": "",
  "appendPrompts": []
}
```

## 请求处理流程

```
Prompt(text)
    │
    ├─ 斜杠命令？ ──→ 执行命令 handler ──→ 返回
    │
    ├─ 模板展开？ ──→ 替换 text
    │
    ├─ 技能匹配？ ──→ 替换 text
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
