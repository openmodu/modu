# TUI

终端用户界面渲染与输入处理库。

## 概述

`tui` 提供了一套用于构建现代终端应用程序的工具，包括屏幕管理、文本渲染和交互式输入。其设计灵感来源于 Claude Code 的 UI 风格。

## 核心组件

### Screen (屏幕)
Screen 使用 ANSI 可滚动区域（scrollable regions）来实现分割终端布局：
- 支持交替屏幕缓冲区 (Alternate screen buffer)
- 可滚动的内容区域
- 静态分隔线和提示栏
- 鼠标事件处理（滚轮滚动、拖动复制）
- 线程安全的写入操作

**布局结构：**
```
第 1 行 .. height-3 : 内容滚动区域（内容在此滚动）
第 height-2 行      : 静态分隔线
第 height-1 行      : 输入提示符（用户在此键入）
第 height 行        : 空白占位行
```

**关键方法：**
```go
NewScreen(out *os.File) *Screen           // 创建并激活 Screen
ContentBottom() int                        // 获取内容区域高度
Write(text string)                         // 向可滚动区域写入文本
Writeln(text string)                       // 写入带换行的行
InitInputLine(prompt string)               // 初始化输入行
RedrawInputContent(prompt, buf, cursor)    // 重绘输入内容
ScrollUp(n int)                            // 向上滚动 n 行
ScrollDown(n int)                          // 向下滚动 n 行
ScrollToBottom()                           // 滚动到历史底部
Close()                                    // 关闭并恢复终端
```

### Renderer (渲染器)
Renderer 负责将 Agent 事件渲染到终端，支持两种模式：
- **Screen 模式**：使用可滚动视口，提供丰富的输出体验
- **Plain 模式**：直接写入输出流，适用于非交互式场景

**关键方法：**
```go
NewRenderer(out io.Writer) *Renderer                    // 创建 Plain 模式渲染器
NewRendererWithScreen(s *Screen) *Renderer              // 创建基于 Screen 的渲染器
PrintUser(msg string)                                   // 打印用户提示行
PrintInfo(msg string)                                   // 打印信息行
PrintError(err error)                                   // 打印错误行
PrintBanner(model, cwd string)                          // 打印启动横幅
PrintSeparator()                                        // 打印分隔线
HandleEvent(event agent.AgentEvent)                     // 处理 Agent 事件
ExpandLastTool()                                        // Ctrl+R: 展开最后一次工具调用的详情
SetActivePrompt(text string)                            // 设置活动提示文本
```

### Input (输入)
Input 提供基于行的编辑功能并支持历史记录，包括原始模式编辑（光标移动、历史导航）。

**关键方法：**
```go
NewInput(in io.Reader, out io.Writer) *Input            // 创建输入处理器
ReadLine(prompt string) (string, error)                 // 读取一行输入
History() []string                                      // 获取历史记录列表
RunScrollLoop(done <-chan struct{}, abortFn func())     // 在 AI 流式传输期间运行滚动循环
```

**键盘快捷键：**
- `ctrl+enter` - 多行输入时换行
- `ctrl+r` - 展开最后一次工具调用的详情
- `ctrl+c` - 中断操作
- `ctrl+d` - 退出（在空行时）
- `shift+drag` - 复制文本（仅限 Screen 模式）

## 使用示例

### 基础 Screen 使用
```go
package main

import (
    "os"
    "github.com/openmodu/modu/pkg/tui"
)

func main() {
    screen := tui.NewScreen(os.Stdout)
    if screen == nil {
        // 退回到普通输出
        return
    }
    defer screen.Close()
    
    screen.Write("你好，世界！")
    screen.Writeln("这是一条多行消息")
    
    // 滚动控制
    screen.ScrollUp(5)      // 向上滚动 5 行
    screen.ScrollDown(3)    // 向下滚动 3 行
}
```

### Renderer 使用
```go
package main

import (
    "os"
    "github.com/openmodu/modu/pkg/tui"
)

func main() {
    renderer := tui.NewRenderer(os.Stdout)
    
    // 打印启动横幅
    renderer.PrintBanner("gpt-4", "/path/to/project")
    
    // 打印用户消息
    renderer.PrintUser("法国的首都是哪里？")
    
    // 处理 Agent 事件（流式）
    renderer.HandleEvent(agentEvent)
    
    // 打印错误
    renderer.PrintError(err)
}
```

### Input 使用
```go
package main

import (
    "os"
    "github.com/openmodu/modu/pkg/tui"
)

func main() {
    input := tui.NewInput(os.Stdin, os.Stdout)
    
    for {
        line, err := input.ReadLine("❯ ")
        if err == tui.ErrInterrupt {
            break // 按下了 Ctrl+C
        }
        if err != nil {
            break // EOF
        }
        
        if line == "exit" {
            break
        }
        
        // 处理输入...
    }
}
```

### 组合使用（完整终端应用）
```go
package main

import (
    "os"
    "github.com/openmodu/modu/pkg/tui"
)

func main() {
    screen := tui.NewScreen(os.Stdout)
    if screen == nil {
        return
    }
    defer screen.Close()
    
    renderer := tui.NewRendererWithScreen(screen)
    input := tui.NewInputWithScreen(os.Stdin, screen)
    
    // 设置事件处理
    renderer.HandleEvent(agentEvent)
    
    // 处理 Ctrl+R 进行工具展开
    input.OnCtrlR = func() {
        renderer.ExpandLastTool()
    }
    
    // 主循环
    line, _ := input.ReadLine("❯ ")
}
```

## 功能特性

### ANSI 样式支持
- 自动颜色检测（非终端输出时禁用）
- 支持的样式：加粗、变暗、斜体、下划线、亮色
- 正确处理 CJK 字符宽度

### 工具调用显示
- 运行状态：`● ToolName(arg)` → `⎿ …`
- 完成状态：`● ToolName(arg) → result (ctrl+r to expand)`
- 支持 Ctrl+R 展开完整的输出详情
- 对长输出自动截断（edit/diff 工具除外）

### 思考内容（Thinking Content）处理
- 自动检测 `<think>...</think>` 标签块
- 以灰色斜体样式显示
- 自动缩进对齐
- 优雅处理尾部换行

### Markdown 渲染
- 支持基础 Markdown 语法
- 代码块高亮与格式化
- 链接格式化

## 线程安全

所有 Screen、Renderer 和 Input 方法都是线程安全的，可以从多个 goroutine 并发调用。
