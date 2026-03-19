# TUI

Terminal user interface rendering and input handling library.

## Overview

`tui` provides a set of tools for building modern terminal applications, including screen management, text rendering, and interactive input. The design is inspired by the Claude Code UI style.

## Core Components

### Screen
Screen uses ANSI scrollable regions to implement split-terminal layouts:
- Alternate screen buffer support
- Scrollable content area
- Static separator lines and prompt bar
- Mouse event handling (scroll wheel, drag-to-copy)
- Thread-safe write operations

**Layout structure:**
```
rows 1 .. height-3 : Content scroll area (content scrolls here)
row  height-2      : Static separator line
row  height-1      : Input prompt (user types here)
row  height        : Empty spacer row
```

**Key methods:**
```go
NewScreen(out *os.File) *Screen           // Create and activate Screen
ContentBottom() int                        // Get content area height
Write(text string)                         // Write text to scrollable area
Writeln(text string)                       // Write line with newline
InitInputLine(prompt string)               // Initialize input line
RedrawInputContent(prompt, buf, cursor)    // Redraw input content
ScrollUp(n int)                            // Scroll up by n lines
ScrollDown(n int)                          // Scroll down by n lines
ScrollToBottom()                           // Scroll to bottom of history
Close()                                    // Close and restore terminal
```

### Renderer
Renderer handles rendering agent events to the terminal, supporting two modes:
- **Screen mode**: Uses scrollable viewport for rich output
- **Plain mode**: Direct write to output stream for non-interactive use

**Key methods:**
```go
NewRenderer(out io.Writer) *Renderer                    // Create plain-mode renderer
NewRendererWithScreen(s *Screen) *Renderer              // Create Screen-backed renderer
PrintUser(msg string)                                   // Print user prompt line
PrintInfo(msg string)                                   // Print info line
PrintError(err error)                                   // Print error line
PrintBanner(model, cwd string)                          // Print startup banner
PrintSeparator()                                        // Print separator line
HandleEvent(event agent.AgentEvent)                     // Handle agent event
ExpandLastTool()                                        // Ctrl+R: Expand last tool call details
SetActivePrompt(text string)                            // Set active prompt text
```

### Input
Input provides line-based editing with history support, including raw mode editing (cursor movement, history navigation).

**Key methods:**
```go
NewInput(in io.Reader, out io.Writer) *Input            // Create input handler
ReadLine(prompt string) (string, error)                 // Read a line of input
History() []string                                      // Get history list
RunScrollLoop(done <-chan struct{}, abortFn func())     // Run scroll loop during AI streaming
```

**Keyboard shortcuts:**
- `ctrl+enter` - Newline in multi-line input
- `ctrl+r` - Expand last tool call details
- `ctrl+c` - Interrupt operation
- `ctrl+d` - Exit (on empty line)
- `shift+drag` - Copy text (Screen mode only)

## Usage Examples

### Basic Screen usage
```go
package main

import (
    "os"
    "github.com/openmodu/modu/pkg/tui"
)

func main() {
    screen := tui.NewScreen(os.Stdout)
    if screen == nil {
        // Fallback to plain output
        return
    }
    defer screen.Close()
    
    screen.Write("Hello, World!")
    screen.Writeln("This is a multi-line message")
    
    // Scroll control
    screen.ScrollUp(5)      // Scroll up 5 lines
    screen.ScrollDown(3)    // Scroll down 3 lines
}
```

### Renderer usage
```go
package main

import (
    "os"
    "github.com/openmodu/modu/pkg/tui"
)

func main() {
    renderer := tui.NewRenderer(os.Stdout)
    
    // Print startup banner
    renderer.PrintBanner("gpt-4", "/path/to/project")
    
    // Print user message
    renderer.PrintUser("What is the capital of France?")
    
    // Handle agent events (streaming)
    renderer.HandleEvent(agentEvent)
    
    // Print error
    renderer.PrintError(err)
}
```

### Input usage
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
            break // Ctrl+C pressed
        }
        if err != nil {
            break // EOF
        }
        
        if line == "exit" {
            break
        }
        
        // Process input...
    }
}
```

### Combined usage (full terminal app)
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
    
    // Setup event handling
    renderer.HandleEvent(agentEvent)
    
    // Handle Ctrl+R for tool expansion
    input.OnCtrlR = func() {
        renderer.ExpandLastTool()
    }
    
    // Main loop
    line, _ := input.ReadLine("❯ ")
}
```

## Features

### ANSI Styling Support
- Automatic color detection (disabled for non-terminal output)
- Supported styles: bold, dim, italic, underline, bright colors
- Correct CJK character width handling

### Tool Call Display
- Running status: `● ToolName(arg)` → `⎿ …`
- Completed status: `● ToolName(arg) → result (ctrl+r to expand)`
- Ctrl+R support for expanding full output details
- Automatic truncation for long outputs (except edit/diff tools)

### Thinking Content Handling
- Detects `<think>...</think>` tag blocks
- Displayed in gray italic style
- Auto-indent alignment
- Graceful handling of trailing newlines

### Markdown Rendering
- Basic Markdown syntax support
- Code block highlighting and formatting
- Link formatting

## Thread Safety

All Screen, Renderer, and Input methods are thread-safe and can be called concurrently from multiple goroutines.
