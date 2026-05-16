# modu_code

一个基于 `coding_agent` 的终端 AI 编程助手，采用 `github.com/grindlemire/go-tui` 构建的 TUI 界面。

---

## 快速开始

```bash
go run ./cmd/modu_code
```

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

## 状态栏说明

状态栏始终显示在底部最后一行：

| 区域 | 内容 |
|------|------|
| 左侧 | 状态消息、错误提示和 `tail` 自动跟随状态 |
