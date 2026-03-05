package moms

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/coding_agent/tools"
"github.com/crosszan/modu/pkg/skills"
	skillstools "github.com/crosszan/modu/pkg/skills/tools"
	"github.com/crosszan/modu/pkg/types"
)

// TelegramContext is the interface the runner uses to communicate back to Telegram.
type TelegramContext interface {
	// Respond appends text to the main response message (creates it on first call).
	Respond(text string, shouldLog bool) error
	// ReplaceMessage replaces the entire main message text.
	ReplaceMessage(text string) error
	// RespondInThread posts a follow-up message in the same chat (used for tool details).
	RespondInThread(text string) error
	// SetWorking toggles the "...working" indicator on the main message.
	SetWorking(working bool) error
	// UploadFile sends the file at filePath to Telegram.
	UploadFile(filePath, title string) error
	// DeleteMessage deletes the main response message.
	DeleteMessage() error
	// ChatID returns the chat this context belongs to.
	ChatID() int64
	// MessageText returns the user's message text.
	MessageText() string
	// MessageTS returns a unique string for the message (used for dedup).
	MessageTS() string
	// SenderName returns the human-readable sender name.
	SenderName() string
	// Images returns any image attachments provided with the message.
	Images() []types.ImageContent
}

// RunResult holds what happened after a run.
type RunResult struct {
	StopReason string
	Error      error
}

// Runner manages an agent per chat channel.
type Runner struct {
	mu          sync.Mutex
	sandbox     *Sandbox
	workingDir  string
	chatID      int64
	model       *types.Model
	getAPIKey   func(provider string) (string, error)
	settings    *Settings
	agentInst   *agent.Agent
	cancelFn    context.CancelFunc
	running     bool
	registryMgr *skills.RegistryManager
	searchCache *skills.SearchCache
}

// NewRunner creates a Runner for a chat.
func NewRunner(sandbox *Sandbox, workingDir string, chatID int64, model *types.Model, getAPIKey func(provider string) (string, error), settings *Settings, registryMgr *skills.RegistryManager, searchCache *skills.SearchCache) *Runner {
	return &Runner{
		sandbox:     sandbox,
		workingDir:  workingDir,
		chatID:      chatID,
		model:       model,
		getAPIKey:   getAPIKey,
		settings:    settings,
		registryMgr: registryMgr,
		searchCache: searchCache,
	}
}

// IsRunning returns true if the agent is currently processing.
func (r *Runner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Abort cancels the current run.
func (r *Runner) Abort() {
	r.mu.Lock()
	fn := r.cancelFn
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Run processes one user message inside the given TelegramContext.
func (r *Runner) Run(parentCtx context.Context, tgCtx TelegramContext) RunResult {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return RunResult{StopReason: "busy"}
	}
	r.running = true
	ctx, cancel := context.WithCancel(parentCtx)
	r.cancelFn = cancel
	r.mu.Unlock()

	defer func() {
		cancel()
		r.mu.Lock()
		r.running = false
		r.cancelFn = nil
		r.mu.Unlock()
	}()

	chatDir := filepath.Join(r.workingDir, fmt.Sprintf("%d", r.chatID))
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		return RunResult{StopReason: "error", Error: err}
	}

	workspacePath := r.sandbox.GetWorkspacePath(r.workingDir)

	// Build (or reuse) the agent instance.
	a := r.getOrCreateAgent(chatDir, workspacePath, tgCtx)

	// Sync missed messages from log.jsonl into the agent context.
	state := a.GetState()
	synced := SyncLogToMessages(chatDir, state.Messages, tgCtx.MessageTS())
	for _, um := range synced {
		a.AppendMessage(um)
	}

	// Refresh system prompt with fresh memory + skills.
	memory := GetMemory(chatDir, r.workingDir)
	skills := LoadAllSkills(chatDir, r.workingDir, workspacePath, r.chatID)
	systemPrompt := BuildSystemPrompt(workspacePath, r.chatID, memory, r.sandbox.cfg, skills)
	a.SetSystemPrompt(systemPrompt)

	// Wire up upload function for the attach tool.
	r.mu.Lock()
	r.mu.Unlock()

	// Set up per-call queue for Telegram messages.
	var queueMu sync.Mutex
	var queueWg sync.WaitGroup

	enqueue := func(fn func() error) {
		queueWg.Add(1)
		go func() {
			defer queueWg.Done()
			queueMu.Lock()
			defer queueMu.Unlock()
			if err := fn(); err != nil {
				fmt.Printf("[moms] telegram api error: %v\n", err)
			}
		}()
	}

	stopReason := "stop"
	var runErr error

	// Subscribe to agent events.
	unsubscribe := a.Subscribe(func(ev agent.AgentEvent) {
		switch ev.Type {
		case agent.EventTypeToolExecutionStart:
			label := ev.ToolName
			if args, ok := ev.Args.(map[string]any); ok {
				if l, ok := args["label"].(string); ok && l != "" {
					label = l
				}
			}
			enqueue(func() error {
				return tgCtx.Respond(fmt.Sprintf("→ _%s_", escapeMarkdown(label)), false)
			})

		case agent.EventTypeToolExecutionEnd:
			result := ""
			if r, ok := ev.Result.(agent.AgentToolResult); ok {
				result = extractResultText(r)
			}
			symbol := "✓"
			if ev.IsError {
				symbol = "✗"
			}
			threadMsg := fmt.Sprintf("*%s %s*\n```\n%s\n```", symbol, ev.ToolName, truncateStr(result, 2000))
			enqueue(func() error {
				return tgCtx.RespondInThread(threadMsg)
			})
			if ev.IsError {
				enqueue(func() error {
					return tgCtx.Respond(fmt.Sprintf("_Error: %s_", escapeMarkdown(truncateStr(result, 200))), false)
				})
			}

		case agent.EventTypeMessageEnd:
			if ev.Message == nil {
				break
			}
			msg, ok := ev.Message.(types.AssistantMessage)
			if !ok {
				break
			}
			if msg.StopReason != "" {
				stopReason = string(msg.StopReason)
			}
			if msg.ErrorMessage != "" {
				runErr = fmt.Errorf("%s", msg.ErrorMessage)
			}
			for _, block := range msg.Content {
				switch b := block.(type) {
				case types.ThinkingContent:
					if strings.TrimSpace(b.Thinking) != "" {
						thinking := b.Thinking
						enqueue(func() error {
							return tgCtx.RespondInThread(fmt.Sprintf("_%s_", escapeMarkdown(thinking)))
						})
					}
				case *types.ThinkingContent:
					if b != nil && strings.TrimSpace(b.Thinking) != "" {
						thinking := b.Thinking
						enqueue(func() error {
							return tgCtx.RespondInThread(fmt.Sprintf("_%s_", escapeMarkdown(thinking)))
						})
					}
				case types.TextContent:
					if strings.TrimSpace(b.Text) != "" {
						text := b.Text
						enqueue(func() error {
							return tgCtx.Respond(text, true)
						})
					}
				case *types.TextContent:
					if b != nil && strings.TrimSpace(b.Text) != "" {
						text := b.Text
						enqueue(func() error {
							return tgCtx.Respond(text, true)
						})
					}
				}
			}
		}
	})
	defer unsubscribe()

	// Build user message.
	now := time.Now()
	userMessage := fmt.Sprintf("[%s] [%s]: %s",
		now.Format("2006-01-02 15:04:05-07:00"),
		tgCtx.SenderName(),
		tgCtx.MessageText(),
	)

	var promptErr error
	images := tgCtx.Images()
	if len(images) > 0 {
		promptErr = a.PromptWithImages(ctx, userMessage, images)
	} else {
		promptErr = a.Prompt(ctx, userMessage)
	}
	a.WaitForIdle()

	// Persist context.
	if msgs := a.GetState().Messages; len(msgs) > 0 {
		var toSave []types.AgentMessage
		for _, m := range msgs {
			toSave = append(toSave, m)
		}
		if err := SaveContextMessages(chatDir, toSave); err != nil {
			fmt.Printf("[moms] failed to save context for chat %d: %v\n", r.chatID, err)
		}
	}

	// Wait for all queued Telegram API calls to complete.
	queueWg.Wait()

	// Handle aborted.
	if ctx.Err() != nil {
		stopReason = "aborted"
	}

	// Handle error.
	if runErr != nil {
		_ = tgCtx.ReplaceMessage("_Sorry, something went wrong_")
		_ = tgCtx.RespondInThread(fmt.Sprintf("_Error: %s_", runErr.Error()))
	}

	if promptErr != nil && runErr == nil {
		runErr = promptErr
	}

	// Done - remove working indicator.
	_ = tgCtx.SetWorking(false)

	return RunResult{StopReason: stopReason, Error: runErr}
}

// getOrCreateAgent returns the persistent agent for this chat, creating it if needed.
func (r *Runner) getOrCreateAgent(chatDir, workspacePath string, tgCtx TelegramContext) *agent.Agent {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.agentInst != nil {
		return r.agentInst
	}

	memory := GetMemory(chatDir, r.workingDir)
	skills := LoadAllSkills(chatDir, r.workingDir, workspacePath, r.chatID)
	systemPrompt := BuildSystemPrompt(workspacePath, r.chatID, memory, r.sandbox.cfg, skills)

	// Check settings for model override
	model := r.model
	settingsModel := r.settings.GetModelID()
	if settingsModel != "" && settingsModel != model.ID {
		mCopy := *model
		mCopy.ID = settingsModel
		mCopy.Name = settingsModel
		model = &mCopy
	}

	// Create tools.
	cwd := chatDir
	agentTools := []agent.AgentTool{
		NewBashSandboxTool(r.sandbox),
		NewReadTool(cwd),
		NewWriteTool(),
		tools.NewEditTool(cwd),
		NewAttachTool(func(filePath, title string) error {
			return tgCtx.UploadFile(filePath, title)
		}),
	}
	if r.registryMgr != nil {
		agentTools = append(agentTools,
			skillstools.NewFindSkillsTool(r.registryMgr, r.searchCache),
			skillstools.NewInstallSkillTool(r.registryMgr, workspacePath),
		)
	}

	a := agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentState{
			SystemPrompt:     systemPrompt,
			Model:            model,
			ThinkingLevel:    agent.ThinkingLevelOff,
			Tools:            agentTools,
			Messages:         []agent.AgentMessage{},
			PendingToolCalls: make(map[string]struct{}),
		},
		GetAPIKey: r.getAPIKey,
	})

	// Load existing messages from context.jsonl if it exists.
	if msgs, err := loadContextMessages(chatDir); err == nil && len(msgs) > 0 {
		a.ReplaceMessages(msgs)
		fmt.Printf("[moms] chat %d: loaded %d messages from context.jsonl\n", r.chatID, len(msgs))
	}

	r.agentInst = a
	return a
}

// loadContextMessages reads messages from context.jsonl.
func loadContextMessages(chatDir string) ([]agent.AgentMessage, error) {
	path := filepath.Join(chatDir, "context.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []agent.AgentMessage
	dec := json.NewDecoder(f)
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			continue
		}

		var base struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			continue
		}

		var msg agent.AgentMessage
		switch base.Role {
		case "user":
			var um types.UserMessage
			if err := json.Unmarshal(raw, &um); err == nil {
				msg = um
			}
		case "assistant":
			var am types.AssistantMessage
			if err := json.Unmarshal(raw, &am); err == nil {
				msg = am
			}
		case "tool":
			var tr types.ToolResultMessage
			if err := json.Unmarshal(raw, &tr); err == nil {
				msg = tr
			}
		}
		if msg != nil {
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

// extractResultText extracts text from AgentToolResult.
func extractResultText(r agent.AgentToolResult) string {
	for _, block := range r.Content {
		switch tc := block.(type) {
		case types.TextContent:
			return tc.Text
		case *types.TextContent:
			if tc != nil {
				return tc.Text
			}
		}
	}
	return ""
}

// escapeMarkdown escapes special MarkdownV2 characters for Telegram.
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(s)
}
