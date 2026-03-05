# pkg/moms — Telegram 版 mom 机器人

基于 `pkg/agent` 和 `pkg/providers` 构建的 Telegram 智能机器人，是Slack 机器人的 Go/Telegram 移植版本。

机器人可以**执行 bash 命令、读写文件**，拥有持久化工作空间、技能系统（skills）、定时事件（events）和跨会话记忆（MEMORY.md），可以自主安装工具、配置环境，完成开发场景中的各类任务。

## 快速开始

参见 [`examples/moms/main.go`](../../examples/moms/main.go) 的完整示例。

```bash
mkdir -p /tmp/moms-data

export MOMS_TG_TOKEN="<BotFather 颁发的 token>"
export ANTHROPIC_API_KEY="<Anthropic Key>"
# 可选：覆盖默认模型
export MOMS_MODEL="claude-sonnet-4-5"

go run github.com/crosszan/modu/examples/moms --sandbox=host /tmp/moms-data
```

## 环境变量

| 变量 | 必填 | 说明 |
|------|------|------|
| `MOMS_TG_TOKEN` | ✅ | Telegram Bot token（@BotFather） |
| `ANTHROPIC_API_KEY` | ✅ | Anthropic API 密钥 |
| `MOMS_MODEL` | ❌ | 模型 ID（默认 `claude-sonnet-4-5`） |

## CLI 参数

```
go run .../examples/moms [--sandbox=host|docker:<container>] <working-dir>
```

| 参数 | 默认 | 说明 |
|------|------|------|
| `--sandbox` | `host` | 执行环境：`host` 直接在宿主机运行，`docker:<name>` 在容器内运行 |
| `<working-dir>` | 必填 | 机器人工作目录根路径 |

## 工作目录结构

```
<working-dir>/
├── MEMORY.md              # 全局记忆（所有会话共享）
├── settings.json          # 全局设置
├── skills/                # 全局自定义 CLI 工具（技能）
├── events/                # 定时事件（JSON 文件）
└── <chatID>/              # 每个 Telegram 会话独立目录
    ├── MEMORY.md          # 会话专属记忆
    ├── log.jsonl          # 完整消息历史
    ├── attachments/       # 用户上传的文件
    ├── scratch/           # 机器人工作目录
    └── skills/            # 会话专属技能（覆盖全局同名技能）
```

## 功能特性

### 工具
机器人内置以下工具：

| 工具 | 说明 |
|------|------|
| `bash` | 执行 shell 命令（支持 host / Docker 沙箱） |
| `read` | 读取文件 |
| `write` | 创建或覆盖文件 |
| `edit` | 精准文本替换编辑文件 |
| `attach` | 向 Telegram 会话发送文件 |

### 技能（Skills）
在 `skills/<name>/SKILL.md` 中创建 CLI 工具，系统提示词会自动加载。

SKILL.md 格式：
```markdown
---
name: my-tool
description: 做某件事的工具
---

# 使用说明
...
```

### 定时事件（Events）
在 `events/` 目录投放 JSON 文件即可触发机器人执行任务：

```json
// 立即触发
{"type":"immediate","chatId":12345678,"text":"检查服务器状态"}

// 指定时间触发一次
{"type":"one-shot","chatId":12345678,"text":"发送周报提醒","at":"2026-03-01T09:00:00+08:00"}

// Cron 周期触发
{"type":"periodic","chatId":12345678,"text":"早安，检查一下邮件","schedule":"0 9 * * 1-5","timezone":"Asia/Shanghai"}
```

### 记忆（Memory）
- 全局记忆：`<working-dir>/MEMORY.md`
- 会话记忆：`<working-dir>/<chatID>/MEMORY.md`

机器人可以自主更新这些文件，实现跨会话持久化。

### Telegram 命令
| 输入 | 效果 |
|------|------|
| 任意消息 | 发给机器人，自动触发 agent 响应 |
| `stop` | 中断当前正在运行的 agent |

## 包结构

| 文件 | 说明 |
|------|------|
| `telegram.go` | Telegram Bot 接入层：消息队列、更新循环、文件上传 |
| `runner.go` | 每个 chat 的 Agent 实例管理 |
| `events.go` | events/ 目录监听与调度 |
| `store.go` | log.jsonl 消息持久化 |
| `context.go` | log.jsonl → Agent 上下文同步 |
| `system_prompt.go` | 系统提示词构建（含记忆、技能、事件文档） |
| `tools.go` | bash、read、write、attach 工具实现 |
| `sandbox.go` | Host / Docker 执行沙箱 |
