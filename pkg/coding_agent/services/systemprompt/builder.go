// Package systemprompt assembles the agent's system prompt from its many
// sources (base prompt, tool descriptions, context files, skills, memory,
// environment, and active-mode blocks) through a single Build path.
package systemprompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

const (
	maxContextFileBytes   = 12 * 1024
	maxTotalContextBytes  = 48 * 1024
	maxMemoryContextBytes = 16 * 1024
)

// sectionSeparator joins every top-level section of the assembled prompt.
const sectionSeparator = "\n\n---\n\n"

const defaultSystemPrompt = `You are an expert software engineer operating as a terminal assistant. Coding work is your specialty, but you can also answer general questions and perform safe non-coding tasks when the user asks. You have tools to read, write, and edit files, run shell commands, and search code. You work in the user's working directory and can make changes directly when the task requires it.

# Core Workflow

For coding or repository tasks, follow this sequence:
1. **Explore** – read the relevant files and understand the existing code before acting
2. **Plan** – for non-trivial tasks, decide the approach before making changes
3. **Implement** – make targeted, minimal changes
4. **Verify** – build and run tests to confirm correctness

For non-coding tasks:
- Answer directly when no repository inspection is needed
- Use available tools when the request requires current machine, repository, command-line, or external information
- Do not refuse solely because the task is not about code; only refuse when the request is unsafe or impossible with the available tools
- If the user asks for current facts such as weather, time-sensitive data, or remote service information, use a suitable safe command or explain what access is missing

# Tool Use

- Use ` + "`" + `read` + "`" + ` to inspect a specific file you already know about
- Use ` + "`" + `grep` + "`" + ` to search for a symbol, pattern, or string across files
- Use ` + "`" + `find` + "`" + ` to locate files by name or path pattern
- Use ` + "`" + `ls` + "`" + ` to explore a directory you haven't seen
- Use ` + "`" + `bash` + "`" + ` to run builds, tests, linters, or safe one-off commands, including read-only commands that answer non-coding requests
- Prefer ` + "`" + `edit` + "`" + ` over ` + "`" + `write` + "`" + ` for modifying existing files – it makes diffs reviewable
- Read a file before editing it; never assume its contents

# Code Changes

- Make the **minimum change** that solves the problem – don't refactor surrounding code unless asked
- Match the existing style, naming, and patterns in the file
- Don't add comments, docstrings, or type annotations to code you didn't change
- Don't add error handling, fallbacks, or validation for scenarios that cannot happen
- Don't introduce backwards-compat shims or unused variables

# Code Review and Analysis

When asked to review, audit, or analyse a package or module:
- **Always read every file** in the target directory before forming conclusions
- Start with ` + "`" + `ls` + "`" + ` to enumerate all files, then ` + "`" + `read` + "`" + ` each one systematically
- Base findings on the actual source code in this session – not on prior conversation summaries
- Cite specific file and line numbers for each finding

# Communication

- Be concise. The user can see your tool calls; don't narrate what you are about to do
- Don't summarise changes at the end – the diff speaks for itself
- If a task is genuinely ambiguous, ask one focused clarifying question before proceeding
- Report blockers clearly; don't retry a failed approach without changing something

# Git Claims

- Before claiming files are staged, unstaged, committed, or unchanged, verify with explicit git commands
- Never say a commit was created unless you have verified the new commit hash
- Distinguish carefully between:
  - staged changes
  - unstaged changes
  - committed changes
- If ` + "`" + `git diff --stat` + "`" + ` is empty, that only means there are no unstaged changes; it does not mean nothing is staged
- When summarising git state, ground every claim in the latest observed git command output

# Security

Write safe code by default. Avoid command injection, SQL injection, path traversal, and hardcoded secrets. If you notice a security issue in existing code, flag it explicitly.`

// MemoryProvider supplies the persistent memory context block. *MemoryStore in
// the parent package satisfies this; the interface keeps this package free of a
// dependency back on coding_agent.
type MemoryProvider interface {
	GetMemoryContext() string
}

// Builder constructs the system prompt from multiple sources.
type Builder struct {
	customPrompt  string
	tools         []types.Tool
	contextFiles  []string
	skillsPrompt  string
	appendPrompts []string
	cwd           string
	memory        MemoryProvider
	model         *types.Model
	modeBlocks    []string
}

// NewBuilder creates a new system prompt builder bound to a working directory.
func NewBuilder(cwd string) *Builder {
	return &Builder{cwd: cwd}
}

// SetCustomPrompt sets a custom base prompt (replaces the default).
func (b *Builder) SetCustomPrompt(prompt string) *Builder {
	b.customPrompt = prompt
	return b
}

// SetTools sets the active tools whose descriptions will be included.
func (b *Builder) SetTools(tools []types.Tool) *Builder {
	b.tools = tools
	return b
}

// AddContextFile adds a context file path to be loaded.
func (b *Builder) AddContextFile(path string) *Builder {
	b.contextFiles = append(b.contextFiles, path)
	return b
}

// SetSkillsPrompt sets the pre-formatted skills prompt (XML format per Agent Skills spec).
func (b *Builder) SetSkillsPrompt(prompt string) *Builder {
	b.skillsPrompt = prompt
	return b
}

// AppendPrompt adds additional prompt text from settings or extensions.
func (b *Builder) AppendPrompt(prompt string) *Builder {
	b.appendPrompts = append(b.appendPrompts, prompt)
	return b
}

// SetMemoryProvider sets the persistent memory source.
func (b *Builder) SetMemoryProvider(provider MemoryProvider) *Builder {
	b.memory = provider
	return b
}

// SetModel sets the model currently connected to the session.
func (b *Builder) SetModel(model *types.Model) *Builder {
	b.model = model
	return b
}

// SetModeBlocks replaces the active-mode blocks (e.g. plan mode, worktree)
// appended after the environment section. Passing nil clears them, so callers
// can re-set the current set on every prompt refresh.
func (b *Builder) SetModeBlocks(blocks []string) *Builder {
	b.modeBlocks = blocks
	return b
}

// Build constructs the final system prompt.
func (b *Builder) Build() string {
	var parts []string

	// 1. Base prompt
	basePrompt := b.customPrompt
	if basePrompt == "" {
		basePrompt = defaultSystemPrompt
	}
	parts = append(parts, basePrompt)

	// 2. Tool descriptions
	if len(b.tools) > 0 {
		var toolDescs []string
		toolDescs = append(toolDescs, "# Available Tools")
		for _, tool := range b.tools {
			toolDescs = append(toolDescs, fmt.Sprintf("## %s\n%s", tool.Name(), tool.Description()))
		}
		parts = append(parts, strings.Join(toolDescs, "\n\n"))
	}

	remainingContextBudget := maxTotalContextBytes
	seenPaths := make(map[string]struct{})
	appendContext := func(label, path string) {
		clean := filepath.Clean(path)
		if _, ok := seenPaths[clean]; ok {
			return
		}
		seenPaths[clean] = struct{}{}
		content, used := b.loadContextFile(path, min(maxContextFileBytes, remainingContextBudget))
		if content == "" || used == 0 {
			return
		}
		remainingContextBudget -= used
		parts = append(parts, fmt.Sprintf("# Context: %s\n%s", label, content))
	}

	// 3. Context files (AGENTS.md, .agents.md, etc.)
	for _, path := range b.contextFiles {
		if remainingContextBudget <= 0 {
			break
		}
		appendContext(filepath.Base(path), path)
	}

	// Auto-discover standard context files
	bootstrapFiles := []string{
		"AGENTS.md",
		"SOUL.md",
		"USER.md",
		"IDENTITY.md",
		".agents.md",
	}

	for _, name := range bootstrapFiles {
		if remainingContextBudget <= 0 {
			break
		}
		path := filepath.Join(b.cwd, name)
		appendContext(name, path)
	}

	// 4. Skill descriptions (XML format per Agent Skills spec)
	if b.skillsPrompt != "" {
		parts = append(parts, b.skillsPrompt)
	}

	// 5. Append prompts
	if len(b.appendPrompts) > 0 {
		parts = append(parts, b.appendPrompts...)
	}

	// 6. Memory Context
	if b.memory != nil {
		memCtx := b.memory.GetMemoryContext()
		if memCtx != "" {
			if len(memCtx) > maxMemoryContextBytes {
				memCtx = truncateWithNotice(memCtx, maxMemoryContextBytes, "memory context")
			}
			parts = append(parts, memCtx)
		}
	}

	// 7. Environment info
	envLines := []string{
		"# Environment",
		fmt.Sprintf("- Current date: %s", time.Now().Format("2006-01-02")),
		fmt.Sprintf("- Working directory: %s", b.cwd),
	}
	if b.model != nil && b.model.ID != "" {
		connectedModel := b.model.ID
		if b.model.ProviderID != "" {
			connectedModel = b.model.ProviderID + "/" + connectedModel
		}
		envLines = append(envLines, fmt.Sprintf("- Connected model: %s", connectedModel))
		if b.model.Name != "" && b.model.Name != b.model.ID {
			envLines = append(envLines, fmt.Sprintf("- Connected model display name: %s", b.model.Name))
		}
	}
	parts = append(parts, strings.Join(envLines, "\n"))

	// 8. Active-mode blocks (plan mode, worktree) — appended last so the
	// stable prompt above is unaffected by transient mode changes.
	parts = append(parts, b.modeBlocks...)

	return strings.Join(parts, sectionSeparator)
}

func (b *Builder) loadContextFile(path string, maxBytes int) (string, int) {
	if maxBytes <= 0 {
		return "", 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", 0
	}
	if len(content) > maxBytes {
		content = truncateWithNotice(content, maxBytes, filepath.Base(path))
	}
	return content, len(content)
}

func truncateWithNotice(content string, maxBytes int, label string) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	const ellipsis = "\n\n...[truncated for context budget]"
	keep := maxBytes - len(ellipsis)
	if keep < 0 {
		keep = 0
	}
	content = strings.TrimSpace(content[:keep])
	if label != "" {
		return content + ellipsis + " (" + label + ")"
	}
	return content + ellipsis
}
