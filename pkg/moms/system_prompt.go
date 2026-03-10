package moms

import (
	"fmt"
	"os"
	"os/exec"
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
		skillDir := filepath.Join(dir, e.Name())
		if err := EnsureSkillPrepared(skillDir); err != nil {
			fmt.Printf("[moms] failed to prepare skill %s: %v\n", e.Name(), err)
		}

		skillMd := filepath.Join(skillDir, "SKILL.md")
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
		skillDir := filepath.Join(dir, e.Name())
		if err := EnsureSkillPrepared(skillDir); err != nil {
			fmt.Printf("[moms] failed to prepare chat skill %s: %v\n", e.Name(), err)
		}
		skillMd := filepath.Join(skillDir, "SKILL.md")
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

// EnsureSkillPrepared runs prepare.sh if it exists and hasn't been run.
func EnsureSkillPrepared(skillDir string) error {
	prepareScript := filepath.Join(skillDir, "prepare.sh")
	marker := filepath.Join(skillDir, ".prepared")

	if _, err := os.Stat(prepareScript); os.IsNotExist(err) {
		return nil
	}

	// If marker exists, we assume it's done.
	// To be smarter, we could check modtime of prepare.sh > marker.
	if info, err := os.Stat(marker); err == nil {
		scriptInfo, err2 := os.Stat(prepareScript)
		if err2 == nil && !scriptInfo.ModTime().After(info.ModTime()) {
			return nil
		}
	}

	fmt.Printf("[moms] running prepare.sh for %s\n", filepath.Base(skillDir))
	cmd := exec.Command("/bin/sh", "prepare.sh")
	cmd.Dir = skillDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	_, _ = os.Create(marker)
	return nil
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

// GetBootstrapContext reads standard context files (AGENTS, SOUL, USER, IDENTITY).
func GetBootstrapContext(chatDir, workingDir string) string {
	var parts []string

	files := []string{"AGENTS.md", "SOUL.md", "USER.md", "IDENTITY.md"}

	for _, name := range files {
		// Global
		globalPath := filepath.Join(workingDir, name)
		if data, err := os.ReadFile(globalPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			parts = append(parts, fmt.Sprintf("### Global %s\n%s", name, strings.TrimSpace(string(data))))
		}

		// Chat-specific
		chatPath := filepath.Join(chatDir, name)
		if data, err := os.ReadFile(chatPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			parts = append(parts, fmt.Sprintf("### Chat-Specific %s\n%s", name, strings.TrimSpace(string(data))))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return "## Agent Configuration\n" + strings.Join(parts, "\n\n") + "\n"
}

// InitBootstrapFiles creates standard context files in the working directory if they do not exist.
func InitBootstrapFiles(workingDir string) error {
	files := map[string]string{
		"AGENTS.md":   "# Global Agent Configuration\n\nDefine global behaviors and traits here.",
		"SOUL.md":     "# Soul Configuration\n\nDefine the core personality and underlying motivations here.",
		"USER.md":     "# User Preferences\n\nDefine user specific preferences and rules here.",
		"IDENTITY.md": "# Identity\n\nDefine the strict identity and role boundaries here.",
	}

	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		return err
	}

	for name, defaultContent := range files {
		path := filepath.Join(workingDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(defaultContent), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// BuildSystemPrompt creates the system prompt for moms, mirroring mom's buildSystemPrompt.
func BuildSystemPrompt(workspacePath string, chatID int64, memory string, sandbox SandboxConfig, skills []SkillInfo) string {
	chatPath := fmt.Sprintf("%s/%d", workspacePath, chatID)
	isDocker := sandbox.Type == SandboxDocker

	var envDesc string
	if isDocker {
		envDesc = `You are running inside a Docker container (Alpine Linux).
- Bash working directory: /
- Install tools with: apk add <package>
- Your changes persist across sessions`
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

	bootstrapCtx := GetBootstrapContext(chatPath, workspacePath)

	return fmt.Sprintf(`You are moms, a Telegram bot assistant. Be concise. No emojis unless the user uses them.

%s
## Context
- For current date/time, use: date
- You have access to previous conversation context.
- For older history, search log.jsonl (contains user messages and your final responses).

## Telegram Formatting (MarkdownV2)
Use Telegram MarkdownV2 formatting: *bold*, _italic_, `+"`code`"+`, `+"```code blocks```"+`, [text](url).
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

Cron schedule format:
- 6 fields (second-level): "秒 分 时 日 月 周" e.g. "*/30 * * * * *" = every 30 seconds, "0 0 9 * * 1-5" = 9am weekdays

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
		bootstrapCtx,
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
