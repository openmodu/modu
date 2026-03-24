# pkg/moms — Telegram-based mom Bot

A Telegram smart bot built on `pkg/agent` and `pkg/providers`, which is a Go/Telegram port of the pi-mono mom Slack bot.

The bot can **execute bash commands, read and write files**, and features persistent workspaces, a skills system, scheduled events, and cross-session memory (`MEMORY.md`). It can autonomously install tools, configure environments, and complete various tasks in development scenarios.

## Quick Start

See the full example in [`examples/moms/main.go`](../../examples/moms/main.go).

```bash
mkdir -p /tmp/moms-data

export MOMS_TG_TOKEN="<token issued by @BotFather>"
export ANTHROPIC_API_KEY="<Anthropic Key>"
# Optional: override default model
export MOMS_MODEL="claude-sonnet-4-5"

go run github.com/openmodu/modu/examples/moms --sandbox=host /tmp/moms-data
```

## Environment Variables

| Variable | Required | Description |
|------|------|------|
| `MOMS_TG_TOKEN` | ✅ | Telegram Bot token (@BotFather) |
| `ANTHROPIC_API_KEY` | ✅ | Anthropic API key |
| `MOMS_MODEL` | ❌ | Model ID (default `claude-sonnet-4-5`) |

## CLI Arguments

```
go run .../examples/moms [--sandbox=host|docker:<container>] <working-dir>
```

| Argument | Default | Description |
|------|------|------|
| `--sandbox` | `host` | Execution environment: `host` runs directly on the host, `docker:<name>` runs inside a container |
| `<working-dir>` | Required | Root path of the bot's working directory |

## Working Directory Structure

```
<working-dir>/
├── MEMORY.md              # Global memory (shared across all sessions)
├── settings.json          # Global settings
├── skills/                # Global custom CLI tools (skills)
├── events/                # Scheduled events (JSON files)
└── <chatID>/              # Independent directory for each Telegram session
    ├── MEMORY.md          # Session-specific memory
    ├── log.jsonl          # Full message history
    ├── attachments/       # Files uploaded by the user
    ├── scratch/           # Bot's working directory
    └── skills/            # Session-specific skills (overrides global ones)
```

## Features

### Tools
The bot has the following built-in tools:

| Tool | Description |
|------|------|
| `bash` | Execute shell commands (supports host / Docker sandbox) |
| `read` | Read a file |
| `write` | Create or overwrite a file |
| `edit` | Precisely replace text in a file |
| `attach` | Send a file to the Telegram session |

### Skills
Create CLI tools in `skills/<name>/SKILL.md`, and the system prompt will automatically load them.

SKILL.md format:
```markdown
---
name: my-tool
description: Tool for doing something
---

# Instructions
...
```

### Scheduled Events
Drop JSON files into the `events/` directory to trigger the bot to perform tasks:

```json
// Trigger immediately
{"type":"immediate","chatId":12345678,"text":"Check server status"}

// Trigger once at a specific time
{"type":"one-shot","chatId":12345678,"text":"Send weekly report reminder","at":"2026-03-01T09:00:00+08:00"}

// Periodic trigger via Cron
{"type":"periodic","chatId":12345678,"text":"Good morning, check emails","schedule":"0 9 * * 1-5","timezone":"Asia/Shanghai"}
```

### Memory
- Global Memory: `<working-dir>/MEMORY.md`
- Session Memory: `<working-dir>/<chatID>/MEMORY.md`

The bot can autonomously update these files to achieve persistence across sessions.

### Telegram Commands
| Input | Effect |
|------|------|
| Any message | Sent to the bot, automatically triggers an agent response |
| `stop` | Interrupts the currently running agent |

## Package Structure

| File | Description |
|------|------|
| `telegram.go` | Telegram Bot access layer: message queue, update loop, file upload |
| `runner.go` | Agent instance management for each chat |
| `events.go` | `events/` directory monitoring and scheduling |
| `store.go` | `log.jsonl` message persistence |
| `context.go` | `log.jsonl` → Agent context synchronization |
| `system_prompt.go` | System prompt construction (including memory, skills, and event docs) |
| `tools.go` | Implementation of `bash`, `read`, `write`, `attach` tools |
| `sandbox.go` | Host / Docker execution sandbox |
