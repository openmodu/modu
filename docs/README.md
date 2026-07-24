# Modu 文档

这里保存跨模块的使用指南、接口参考、架构说明、实施方案和技术文章。根目录及模块目录中的 README 只负责说明入口、最短用法和模块边界；需要连续阅读的长文统一从本页进入。

## 从哪里开始

- 想安装并运行项目：先读仓库根目录的 [中文 README](../README_zh.md) 或 [English README](../README.md)。
- 想使用终端编码 Agent：读 [modu_code 使用指南](guides/modu-code.md)。
- 想编写和运行评测：读 [modu_eval 评测指南](guides/modu-eval.md)。
- 想接入 HTTP Gateway：读 [ACP Gateway API](reference/acp-gateway-api.md) 和 [ACP 集成架构](architecture/acp.md)。
- 想扩展编码运行时：读 [Coding Agent 参考](reference/coding-agent.md) 和 [Coding Agent 架构](architecture/coding-agent.md)。
- 想使用多 Agent 通信：读 [Mailbox 中文参考](reference/mailbox.zh-CN.md)；英文版见 [Mailbox reference](reference/mailbox.md)。

## 使用指南

- [modu_code 使用指南](guides/modu-code.md)：启动方式、模型配置、TUI 操作、渠道和运行状态。
- [modu_eval 评测指南](guides/modu-eval.md)：任务格式、运行器、结果界面和评测编写方法。

## 接口参考

- [ACP Gateway API](reference/acp-gateway-api.md)：项目、会话、turn、事件流和运行时 Agent API。
- [Coding Agent 参考](reference/coding-agent.md)：会话装配、工具、hook、上下文、workflow 和配置。
- [Mailbox 中文参考](reference/mailbox.zh-CN.md) / [English](reference/mailbox.md)：Hub、任务、协作模式和存储接口。
- [Subagent 对齐清单](reference/subagent-parity.md)：与 pi-subagents 的能力差异和验收状态。

## 架构

- [ACP 集成架构](architecture/acp.md)：Gateway、项目、会话和 ACP 适配层的职责边界。
- [Coding Agent 架构](architecture/coding-agent.md)：包结构、依赖方向和请求执行路径。
- [modu TUI 封装与业务拆分](architecture/modu-tui.md)：UI 内核、Intent/Client 边界、状态拆分和 `modu_code` 迁移顺序。
- [Mailbox Agent System](architecture/mailbox-agent-system.md)：协议、Hub、持久化和跨进程协作设计。

## 方案与验收记录

- [ACP 开发规划](plans/acp-roadmap.md)：实现顺序、里程碑和验收条件的历史基线。
- [Loop Engineering](plans/loop-engineering.md)：定时执行、隔离、独立验收、持久化和真机记录。
- [Lua Workflow 编排](plans/lua-workflow-orchestration.md)：Lua 接口、资源限制、兼容状态和后续清单。
- [工具输出与并行执行](plans/tool-output-and-parallel-execution.md)：大输出落盘、模型预览和并发顺序约束。

## 技术文章

- [modu code 运行时设计](articles/coding-agent-runtime-sharing.md)：从用户输入到可恢复会话的完整执行链路。
- [TUI 交互改动记录](articles/modu-code-tui-optimizations-2026-05-18-20.md)：2026-05-18 至 2026-05-20 的界面能力与测试覆盖。
- [TUI resize 重影](articles/modu-code-tui-resize-rendering.md)：终端 reflow、diff 渲染和真实终端验证方法。
- [如何在面试中讲清 Modu](articles/modu-interview-notes.md)：按架构边界组织项目讲解，并标明需要现场核对的事实。

## 文档放置规则

- README 与源码放在一起，但控制在模块入口所需的长度；详细说明链接到 `docs/`。
- 跨模块机制放进 `architecture/`，操作步骤放进 `guides/`，稳定接口放进 `reference/`。
- 尚在推进或需要保留验收过程的内容放进 `plans/`；按时间记录的复盘和分享放进 `articles/`。
- `PROGRESS.md` 留在对应模块目录，它记录开发状态，不作为用户文档入口。
- 修改 `pkg/` 下的公开行为时，同时更新模块 README 和对应的 `docs/reference/` 文档。
