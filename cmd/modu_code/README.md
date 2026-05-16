# modu_code

一个基于 `coding_agent` 的终端 AI 编程助手，采用 `github.com/grindlemire/go-tui` 构建的 TUI 界面。

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
  "active": "local-qwen",
  "models": [
    {
      "name": "local-qwen",
      "provider": "lmstudio",
      "model": "qwen/qwen3.6-35b-a3b",
      "baseUrl": "http://127.0.0.1:1234/v1",
      "apiKey": "lm-studio"
    },
    {
      "name": "deepseek",
      "provider": "deepseek",
      "model": "deepseek-chat",
      "baseUrl": "https://api.deepseek.com/v1",
      "apiKey": "..."
    }
  ]
}
```

运行中输入 `/model` 会打开模型选择器，可用方向键选择、`Enter` 确认、`Esc` 取消。也可以用 `/model list` 查看模型，用 `/model <name>` 或 `/model <provider> <modelId>` 快速切换。切换后会写回 `active`，下次启动继续使用该模型；如果实际切换到了另一个模型，会清空旧对话上下文并在状态里明确提示。

配置辅助命令：

```bash
go run ./cmd/modu_code config example
go run ./cmd/modu_code config init
go run ./cmd/modu_code config init --force
go run ./cmd/modu_code config validate
```

---

## 运行检查

输入 `/context` 可以查看当前 prompt/context 来源摘要，包括当前模型、工作目录、会话消息数、系统 prompt 大小、memory 是否为空、计划模式、worktree 状态、项目上下文文件和已发现 skills。

输入 `/doctor` 可以查看基础运行诊断，包括模型配置路径、当前模型、baseURL 连通性、provider 是否注册、API key 状态、上下文文件数量和已发现的问题。

---

## 键盘快捷键

| 按键 | 说明 |
|------|------|
| `Enter` | 提交消息 |
| `ctrl+c` | 中断当前请求 / 退出 |
| `ctrl+d` | 退出（输入框为空时） |
| `ctrl+l` | 清屏 |
| `ctrl+o` | 切换工具调用展开模式 |
| `esc` | 中断当前请求 / 返回输入 |
| `PageUp` / `PageDown` | 滚动对话 |
| `Home` / `End` | 跳到顶部 / 底部 |
| `ctrl+j` | 在输入框插入换行 |

---

## 斜杠命令

| 命令 | 说明 |
|------|------|
| `/model` | 打开模型选择器 |
| `/context` | 查看当前 prompt/context 来源 |
| `/doctor` | 查看基础运行诊断 |
| `/retry` | 重试上一条失败的 prompt |

---

## 状态说明

运行状态显示在聊天输入框上方，当前轮次会显示动画、耗时和中断提示；轮次结束后会保留最近一次完成/中断的耗时摘要。输入框下方保留快捷键、错误和临时状态提示。

| 区域 | 内容 |
|------|------|
| 输入框上方 | 当前运行状态或最近一轮完成/中断耗时，耗时超过 60 秒时显示 `min` |
| 输入框下方 | 快捷键提示、错误提示和临时状态消息；连续相同错误会折叠计数，并提示 `/retry`、切换模型和运行 `/doctor` |
