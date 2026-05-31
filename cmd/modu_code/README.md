# modu_code

一个基于 `coding_agent` 的终端 AI 编程助手，默认采用 Bubble Tea inline TUI：完成的对话会进入终端 scrollback，便于像 Claude Code / Codex 一样选择和复制；启动和模型切换时会把当前模型、目录、模式和 channel 信息打印为非常驻多行 header，底部输入和选择器由 Bubble Tea 渲染。

---

## 快速开始

```bash
go run ./cmd/modu_code
```

---

## 模型配置

`modu_code` 优先读取 `~/.coding_agent/config.json` 中的模型配置。支持配置多个模型，并通过 `active` 指定默认使用的模型：

```json
{
  "version": 2,
  "active": "local-qwen",
  "roles": {
    "summary": "local-qwen",
    "dispatcher": "deepseek"
  },
  "scopedModels": ["local-qwen", "deepseek"],
  "reasoning": {
    "level": "off"
  },
  "providers": {
    "lmstudio": {
      "type": "openai-compatible",
      "baseUrl": "http://127.0.0.1:1234/v1",
      "apiKey": "lm-studio"
    },
    "deepseek": {
      "type": "openai-compatible",
      "baseUrl": "https://api.deepseek.com/v1",
      "apiKeyEnv": "DEEPSEEK_API_KEY"
    }
  },
  "models": [
    {
      "name": "local-qwen",
      "description": "local coding model",
      "provider": "lmstudio",
      "model": "qwen/qwen3.6-35b-a3b",
      "capabilities": ["tools"]
    },
    {
      "name": "deepseek",
      "description": "remote fallback model",
      "provider": "deepseek",
      "model": "deepseek-chat",
      "capabilities": ["tools"]
    }
  ]
}
```

`providers` 只描述连接方式，`models` 只描述可选模型；`active` 是默认模型，`scopedModels` 是模型循环范围，`roles` 预留给 summary/dispatcher 等专用模型。

运行中输入 `/model` 会打开模型选择器，可用方向键选择、`Enter` 确认、`Esc` 取消。也可以用 `/model <query>` 带初始搜索打开选择器。切换后会写回 `active`，下次启动继续使用该模型；如果实际切换到了另一个模型，会清空旧对话上下文并在状态里明确提示。

配置辅助命令：

```bash
go run ./cmd/modu_code config
go run ./cmd/modu_code config init
go run ./cmd/modu_code config init --force
```

TUI 中输入 `/config` 会打开配置页面，当前只提供 `Active Model` 和 `Provider` 两个入口。进入二级页面后可用 `Esc` 返回上一层。`Provider` 会先打开和模型选择器一致的可搜索列表，可选择已有或预设 provider 进入设置，也可选择 `Custom OpenAI-compatible` 配置自定义 OpenAI 风格源；保存 provider 后会自动请求 `<baseUrl>/models`，把返回的模型写入 `models` 配置。

---

## 运行检查

输入 `/context` 可以查看当前 prompt/context 来源摘要，包括当前模型、工作目录、会话消息数、系统 prompt 大小、memory 是否为空、计划模式、worktree 状态、项目上下文文件、已发现 skills、prompt templates 和本地资源包。

输入 `/doctor` 可以查看基础运行诊断，包括模型配置路径、当前模型、baseURL 连通性、provider 是否注册、API key 状态、上下文文件数量和已发现的问题。

---

## 键盘快捷键

| 按键 | 说明 |
|------|------|
| `Enter` | 提交消息；任务运行中提交为 follow-up 队列 |
| `Shift+Enter` | 任务运行中 steer 当前任务，打断当前轮并切到新指令 |
| `ctrl+c` | 中断当前请求 / 退出 |
| `ctrl+d` | 退出（输入框为空时） |
| `ctrl+l` | 清屏 |
| `ctrl+o` | 切换工具调用展开模式 |
| `esc` | 中断当前请求 / 返回输入 |
| `Home` / `End` | 输入行首 / 行尾 |
| `ctrl+j` | 在输入框插入换行 |

输入 `/` 会打开轻量命令选择器，可用方向键选择，`Tab` 补全，`Enter` 执行选中命令。`/scoped-models` 可直接用 slash 参数配置模型循环范围：`list` 查看，`set <model...>` 设置，`add <model...>` 添加，`remove <model...>` 移除，`clear` 恢复全部模型，`edit` 打开选择器。

输入 `!cmd` 会在当前工作目录执行 shell 命令，把输出显示在 TUI 中，并作为下一条用户消息发送给模型。输入 `!!cmd` 只执行并显示输出，不发送给模型。

任务运行中继续输入普通消息并按 Enter，会把消息加入 follow-up 队列，在当前任务结束后自动继续执行。任务运行中按 Shift+Enter，或输入 `/steer <message>` / `/s <message>`，会把消息加入 steer 队列并中断当前轮，随后按新方向继续。也可以输入 `/followup <message>` / `/f <message>` 显式排队下一条 follow-up。

输入 `/queue` 可以查看当前等待执行的 steer / follow-up 队列；`/queue clear` 清空全部队列，`/queue clear steer` 或 `/queue clear followup` 按类型清空，`/queue drop` 删除最后一条等待消息。

Bubble Tea 的全屏 TUI 保留为实验路径；默认交互路径使用 Bubble Tea inline runtime，优先保证 scrollback 和终端文本选择体验。

---

## Telegram

配置 `MOMS_TG_TOKEN` 或 `~/.coding_agent/channels/telegram/config.json` 后，`modu_code` 会启动共享当前 session 的 Telegram bot。Telegram 和 TUI 共用同一个 steer / follow-up 队列：

| Telegram 输入 | 任务空闲时 | 任务运行中 |
|------|------|------|
| 普通消息 | 作为新 prompt 执行 | 加入 follow-up 队列 |
| `/followup <message>` / `/f <message>` | 提示当前没有 active task | 加入 follow-up 队列 |
| `/steer <message>` / `/s <message>` | 提示当前没有 active task | 加入 steer 队列并中断当前轮 |

---

## 斜杠命令

| 命令 | 说明 |
|------|------|
| `/settings` | 显示 Bubble Tea 迁移状态 |
| `/model [query]` | 打开带搜索的模型选择器 |
| `/scoped-models [list\|set\|add\|remove\|clear\|edit]` | 配置模型循环范围 |
| `/config` | 打开模型配置页面 |
| `/context` | 查看当前 prompt/context 来源 |
| `/doctor` | 查看基础运行诊断 |
| `/retry` | 重试上一条失败的 prompt |
| `/steer <message>` | 任务运行中打断当前轮，并按新消息继续 |
| `/s <message>` | `/steer` 的短别名；用于终端无法识别 Shift+Enter 时 |
| `/followup <message>` | 任务运行中把消息排到当前任务之后执行 |
| `/f <message>` | `/followup` 的短别名 |
| `/queue` | 查看当前等待执行的 steer / follow-up 队列 |
| `/queue clear [steer\|followup]` | 清空全部队列，或按类型清空 |
| `/queue drop` | 删除最后一条等待消息 |
| `/hotkeys` | 查看快捷键 |
| `/reload` | 重新加载 keybindings 之外的动态资源：skills、prompts、context |
| `/new` | 清空当前会话上下文 |
| `/session` | 查看当前会话 id、名称、文件、cwd、模型、消息数、tokens、plan/worktree 和资源摘要 |
| `/name <name>` | 设置当前会话名称 |
| `/session delete <file>` | 删除非当前会话文件 |
| `/sessions [all]` | 列出当前项目或全部项目的会话 |
| `/resume <file>` | 切换到指定会话文件 |
| `/fork-session <file>` | 从已有会话复制一份到当前项目 |
| `/fork <entry-id>` | 从历史位置 fork |
| `/clone` | 从当前 session leaf 克隆一份会话 |
| `/tree` | 显示 session tree 摘要 |
| `/export [file]` | 导出当前 session 为 HTML；相对路径按当前工作目录解析 |
| `/copy` | 复制最后一条 assistant 回复到系统剪贴板 |
| `/changelog` | 显示当前 git 仓库最近提交 |
| `/skills` | 列出已发现 skills |
| `/prompts` | 列出已发现 prompt templates |

---

## 状态说明

运行状态显示在聊天输入框上方，当前轮次会显示动画、耗时和中断提示；轮次结束后会保留最近一次完成/中断的耗时摘要。输入框下方保留快捷键、错误和临时状态提示。

| 区域 | 内容 |
|------|------|
| 输入框上方 | 当前运行状态或最近一轮完成/中断耗时，耗时超过 60 秒时显示 `min` |
| 输入框下方 | 快捷键提示、错误提示和临时状态消息；连续相同错误会折叠计数，并提示 `/retry`、切换模型和运行 `/doctor` |
