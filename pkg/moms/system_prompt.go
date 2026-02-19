package moms

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SkillInfo is a loaded skill's metadata.
type SkillInfo struct {
	Name        string
	Description string
	FilePath    string // path to SKILL.md (in sandbox path space)
	BaseDir     string // directory containing SKILL.md (in sandbox path space)
}

// LoadSkills loads SKILL.md files from a directory.
func LoadSkills(dir, sandboxBasePath string) []SkillInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var skills []SkillInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillMd := filepath.Join(dir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillMd)
		if err != nil {
			continue
		}
		name, desc := parseSkillFrontmatter(string(data))
		if name == "" {
			continue
		}
		// Translate host path -> sandbox path
		sandboxSkillMd := filepath.Join(sandboxBasePath, "skills", e.Name(), "SKILL.md")
		sandboxBaseDir := filepath.Join(sandboxBasePath, "skills", e.Name())
		skills = append(skills, SkillInfo{
			Name:        name,
			Description: desc,
			FilePath:    sandboxSkillMd,
			BaseDir:     sandboxBaseDir,
		})
	}
	return skills
}

// LoadAllSkills loads global + chat-specific skills (chat overrides global).
func LoadAllSkills(chatDir, workspaceDir, sandboxWorkspacePath string, chatID int64) []SkillInfo {
	skillMap := make(map[string]SkillInfo)

	// Global skills
	for _, s := range LoadSkills(filepath.Join(workspaceDir, "skills"), sandboxWorkspacePath) {
		skillMap[s.Name] = s
	}

	// Chat-specific skills override global
	chatSandboxPath := fmt.Sprintf("%s/%d", sandboxWorkspacePath, chatID)
	for _, s := range LoadSkillsInChatDir(chatDir, chatSandboxPath) {
		skillMap[s.Name] = s
	}

	out := make([]SkillInfo, 0, len(skillMap))
	for _, s := range skillMap {
		out = append(out, s)
	}
	return out
}

// LoadSkillsInChatDir loads skills from <chatDir>/skills/.
func LoadSkillsInChatDir(chatDir, sandboxChatPath string) []SkillInfo {
	dir := filepath.Join(chatDir, "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var skills []SkillInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillMd := filepath.Join(dir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillMd)
		if err != nil {
			continue
		}
		name, desc := parseSkillFrontmatter(string(data))
		if name == "" {
			continue
		}
		sandboxSkillMd := fmt.Sprintf("%s/skills/%s/SKILL.md", sandboxChatPath, e.Name())
		sandboxBaseDir := fmt.Sprintf("%s/skills/%s", sandboxChatPath, e.Name())
		skills = append(skills, SkillInfo{
			Name:        name,
			Description: desc,
			FilePath:    sandboxSkillMd,
			BaseDir:     sandboxBaseDir,
		})
	}
	return skills
}

// parseSkillFrontmatter extracts name and description from SKILL.md YAML frontmatter.
func parseSkillFrontmatter(content string) (name, description string) {
	if !strings.HasPrefix(content, "---") {
		return
	}
	end := strings.Index(content[3:], "---")
	if end < 0 {
		return
	}
	front := content[3 : end+3]
	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "name:"); ok {
			name = strings.TrimSpace(after)
		}
		if after, ok := strings.CutPrefix(line, "description:"); ok {
			description = strings.TrimSpace(after)
		}
	}
	return
}

// GetMemory reads MEMORY.md files (global + chat-specific).
func GetMemory(chatDir, workingDir string) string {
	var parts []string

	// Global memory
	globalPath := filepath.Join(workingDir, "MEMORY.md")
	if data, err := os.ReadFile(globalPath); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			parts = append(parts, "### Global Memory\n"+s)
		}
	}

	// Chat-specific memory
	chatPath := filepath.Join(chatDir, "MEMORY.md")
	if data, err := os.ReadFile(chatPath); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			parts = append(parts, "### Chat-Specific Memory\n"+s)
		}
	}

	if len(parts) == 0 {
		return "(no working memory yet)"
	}
	return strings.Join(parts, "\n\n")
}

// BuildSystemPrompt creates the system prompt for moms, mirroring mom's buildSystemPrompt.
func BuildSystemPrompt(workspacePath string, chatID int64, memory string, sandbox SandboxConfig, skills []SkillInfo) string {
	chatPath := fmt.Sprintf("%s/%d", workspacePath, chatID)
	isDocker := sandbox.Type == SandboxDocker

	var envDesc string
	if isDocker {
		envDesc = fmt.Sprintf(`You are running inside a Docker container (Alpine Linux).
- Bash working directory: /
- Install tools with: apk add <package>
- Your changes persist across sessions`)
	} else {
		envDesc = `You are running directly on the host machine.
- Be careful with system modifications`
	}

	timezone := time.Now().Format("-07:00")

	var skillLines []string
	for _, s := range skills {
		skillLines = append(skillLines, fmt.Sprintf("- **%s** (`%s`): %s", s.Name, s.FilePath, s.Description))
	}
	skillsSection := "(no skills installed yet)"
	if len(skillLines) > 0 {
		skillsSection = strings.Join(skillLines, "\n")
	}

	return fmt.Sprintf(`You are moms, a Telegram bot assistant. Be concise. No emojis unless the user uses them.

## Context
- For current date/time, use: date
- You have access to previous conversation context.
- For older history, search log.jsonl (contains user messages and your final responses).

## Telegram Formatting (MarkdownV2)
Use Telegram MarkdownV2 formatting: *bold*, _italic_, ` + "`code`" + `, ` + "```code blocks```" + `, [text](url).
For plain responses you may also send plain text.

## Environment
%s

## Workspace Layout
%s/
├── MEMORY.md                    # Global memory (all chats)
├── skills/                      # Global CLI tools you create
├── events/                      # Scheduled events
└── %d/                          # This chat
    ├── MEMORY.md                # Chat-specific memory
    ├── log.jsonl                # Message history
    ├── attachments/             # User-shared files
    ├── scratch/                 # Your working directory
    └── skills/                  # Chat-specific tools

## Skills (Custom CLI Tools)
You can create reusable CLI tools for recurring tasks.
Store in %s/skills/<name>/ (global) or %s/skills/<name>/ (chat-specific).
Each skill needs a SKILL.md with YAML frontmatter (name, description) and usage instructions.

### Available Skills
%s

## Events
Schedule events in %s/events/ directory. JSON files:

**Immediate** - triggers instantly:
{"type": "immediate", "chatId": %d, "text": "description"}

**One-shot** - triggers once at a specific time:
{"type": "one-shot", "chatId": %d, "text": "reminder text", "at": "2025-12-15T09:00:00%s"}

**Periodic** - triggers on cron schedule:
{"type": "periodic", "chatId": %d, "text": "Check inbox", "schedule": "0 9 * * 1-5", "timezone": "Asia/Shanghai"}

Use unique filenames. Max 5 events queued per chat.

### Silent completion
For periodic events with nothing to report, reply with just [SILENT] to suppress the message.

## Memory
Write MEMORY.md to persist context:
- Global (%s/MEMORY.md): skills, preferences, project info
- Chat (%s/MEMORY.md): chat-specific decisions, ongoing work

### Current Memory
%s

## System Configuration Log
Maintain %s/SYSTEM.md to log installed packages, env vars, config file changes.

## Log Queries
Format: {"date":"...","ts":"...","user":"...","userName":"...","text":"...","isBot":false}

`+"```"+`bash
# Recent messages
tail -30 %s/log.jsonl | jq -c '{date: .date[0:19], user: (.userName // .user), text}'

# Search for topic
grep -i "topic" %s/log.jsonl | jq -c '{date: .date[0:19], user: (.userName // .user), text}'
`+"```"+`

## Tools
- bash: Run shell commands (primary tool). Install packages as needed.
- read: Read files
- write: Create/overwrite files
- edit: Surgical file edits
- attach: Send files to Telegram chat
`,
		envDesc,
		workspacePath, chatID,
		workspacePath, chatPath,
		skillsSection,
		workspacePath, chatID, chatID, timezone, chatID,
		workspacePath, chatPath,
		memory,
		workspacePath,
		chatPath, chatPath,
	)
}
