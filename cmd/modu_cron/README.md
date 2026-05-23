# modu_cron

Cron-driven agent runner built on modu's CodingAgent. Daemon + CLI dual-form:
the daemon owns the schedule, the CLI manages tasks.

## 状态

骨架阶段 (feat/modu_cron)。`daemon` / `list` 已可跑；`add` / `run` / `rm` / `logs` 占位待业务开发。

## 安装

```
go build ./cmd/modu_cron
```

## 用法

```
modu_cron [-c <config>] <subcommand>
```

默认配置: `~/.modu_cron/config.yaml`，可用 `-c` 覆盖。示例见 `config.example.yaml`。

### 子命令

| 命令 | 状态 | 说明 |
|------|------|------|
| `daemon` | ✅ | 跑调度循环, 按 cron 表达式触发任务, `Ctrl+C` 退出 |
| `list` | ✅ | 列出当前配置中的所有任务 |
| `add` | 🚧 | 添加任务 (待业务开发) |
| `run <id>` | 🚧 | 立即触发一次 (待业务开发) |
| `rm <id>` | 🚧 | 删除任务 (待业务开发) |
| `logs <id>` | 🚧 | 查看任务历史 (待业务开发) |

## 配置

```yaml
tasks:
  - id: heartbeat
    cron: "*/10 * * * * *"   # 6 字段格式: sec min hour dom mon dow
    prompt: "say hello"
    enabled: true
```

`cron` 表达式使用 `robfig/cron/v3` 的 6 字段格式 (含 seconds)。

## 目录结构

```
cmd/modu_cron/
├── main.go                 # 入口 + 子命令路由
├── config.example.yaml
├── README.md
└── internal/
    ├── cli/                # 子命令实现
    ├── config/             # YAML 加载/保存
    └── scheduler/          # robfig/cron 封装 + Runner hook
```

## 业务开发路线

1. `scheduler.Runner` 接 `coding_agent.CodingSession`, 真正跑 prompt
2. agent 工具集加入 `cron_add` / `cron_remove` / `cron_list` (自然语言管理任务)
3. 任务执行历史持久化, `logs <id>` 查询
4. `add` / `rm` 子命令落地 (写回 YAML)
