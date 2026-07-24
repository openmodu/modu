// Command tuipoc2 demonstrates the reusable full-screen TUI viewport from
// pkg/modu-tui on Bubble Tea v2.
//
// Run:  go run ./cmd/tuipoc2
// Keys:
//   - Enter = send · wheel/PgUp/PgDn = scroll · drag = select+copy
//   - click ▸ = fold · ctrl+End = jump to bottom · Ctrl+C = quit
package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

const cannedReply = `这是流式渲染进 **viewport** 的回复(v2 版)—— 底部输入框始终钉在底部。

最近几个提交(表格在 viewport 内渲染,不进终端原生 scrollback):

| # | Hash | Message |
|---|---------|--------------------------------------------------|
| 1 | 331d879 | fix(tui): render streaming markdown tables as placeholders |
| 2 | 6d1739d | fix(tui): let markdown tables use full available width |
| 3 | c76b15b | feat(tui): add notify block kind |
| 4 | 7adf6f8 | feat(workflow): migrate Lua engine to JavaScript |

剪贴板走 v2 原生 tea.SetClipboard(OSC 52),SSH 下也不会把鼠标/查询序列漏进输入框。`

func main() {
	opts := modutui.Options{
		Width:       120,
		Height:      35,
		StreamReply: cannedReply,
		InitialEntries: []modutui.Entry{
			{
				Role: modutui.RoleAssistant,
				Nodes: []modutui.Node{modutui.MarkdownNode{
					Text: "POC v2: 全屏 viewport 架构,跑在 bubbletea v2(charm.land)。自研滚动区 + 输入框,不依赖 bubbles。Enter 发送会模拟含表格的流式回复。",
				}},
			},
			{
				Role: modutui.RoleAssistant,
				Nodes: []modutui.Node{modutui.ToolNode{Call: modutui.ToolCall{
					ID:      "poc-command",
					Name:    "bash",
					Summary: "Ran 1 shell command",
					Detail:  "$ go test ./cmd/tuipoc2/\nok  github.com/openmodu/modu/cmd/tuipoc2  0.4s\n\n点这一行可展开/折叠。",
					Done:    true,
				}}},
			},
		},
	}
	final, err := tea.NewProgram(modutui.NewModel(opts), tea.WithWindowSize(opts.Width, opts.Height)).Run()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// Run has returned, so the alt-screen is already torn down and the normal
	// buffer restored. Printing the transcript now lands it in the main screen.
	if m, ok := final.(modutui.Model); ok {
		if lines := m.Lines(); len(lines) > 0 {
			fmt.Println(strings.Join(lines, "\n"))
		}
	}
}
