# Mailbox

[English](README.md) | [中文](README_zh.md)

Mailbox 负责 Agent 注册、独立收件箱、任务与项目状态、能力队列、结果验证、流水线和对话记录。你可以在同一进程内直接使用 `Hub`，也可以通过 Redis 兼容命令服务器把同一套状态机提供给其他进程。

Mailbox 只解决协作问题，不负责运行 Agent。它不会调用 LLM，不会替手动分配的任务选择 Worker，默认存储也不提供重启后的数据恢复。

## 最短示例

```go
package main

import (
	"fmt"

	"github.com/openmodu/modu/pkg/mailbox"
)

func main() {
	hub := mailbox.NewHub()
	hub.Register("director")
	hub.Register("writer")

	msg, err := mailbox.NewTaskAssignMessage("director", "task-1", "编写产品文案")
	if err != nil {
		panic(err)
	}
	if err := hub.Send("writer", msg); err != nil {
		panic(err)
	}

	raw, ok := hub.Recv("writer")
	if !ok {
		return
	}
	fmt.Println(raw)
}
```

`Recv` 不会阻塞。目标不存在或收件箱已满时，`Send` 会返回错误；调用方需要明确选择重试、丢弃还是施加背压。`NewHub()` 默认使用不落盘的 `noopStore`；如果任务、项目、角色和对话需要跨重启恢复，请接入 `mailbox/sqlitestore`。

## 文档

- [English reference](../../docs/reference/mailbox.md)
- [中文参考](../../docs/reference/mailbox.zh-CN.md)
- [系统架构](../../docs/architecture/mailbox-agent-system.md)：进程边界、状态流转、持久化、事件和失败场景。
