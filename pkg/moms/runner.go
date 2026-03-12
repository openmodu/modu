package moms

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/channels"
	"github.com/crosszan/modu/pkg/coding_agent/tools"
	"github.com/crosszan/modu/pkg/skills"
	skillstools "github.com/crosszan/modu/pkg/skills/tools"
	"github.com/crosszan/modu/pkg/types"
)

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
	store       *ContextStore
	summarizer  *Summarizer
}

// NewRunner creates a Runner for a chat.
func NewRunner(sandbox *Sandbox, workingDir string, chatID int64, model *types.Model, getAPIKey func(provider string) (string, error), settings *Settings, registryMgr *skills.RegistryManager, searchCache *skills.SearchCache) *Runner {
	store := NewContextStore(workingDir)

	callLLM := func(ctx context.Context, prompt string) (string, error) {
		llmCtx := &types.LLMContext{
			Messages: []types.AgentMessage{
				types.UserMessage{Role: "user", Content: prompt},
			},
		}
		temp := 0.3
		maxT := 1024
		opts := &types.SimpleStreamOptions{
			StreamOptions: types.StreamOptions{
				Temperature: &temp,
				MaxTokens:   &maxT,
			},
		}
		if getAPIKey != nil {
			key, err := getAPIKey(model.ProviderID)
			if err == nil {
				opts.APIKey = key
			}
		}

		stream, err := agent.StreamDefault(ctx, model, llmCtx, opts)
		if err != nil {
			return "", err
		}
		// Drain stream events to allow resolution.
		for range stream.Events() {
		}
		res, err := stream.Result()
		if err != nil {
			return "", err
		}
		if res == nil {
			return "", fmt.Errorf("empty result from LLM")
		}
		return extractText(res.Content), nil
	}

	return &Runner{
		sandbox:     sandbox,
		workingDir:  workingDir,
		chatID:      chatID,
		model:       model,
		getAPIKey:   getAPIKey,
		settings:    settings,
		registryMgr: registryMgr,
		searchCache: searchCache,
		store:       store,
		summarizer:  NewSummarizer(store, callLLM),
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

// Run processes one user message via the given ChannelContext.
func (r *Runner) Run(parentCtx context.Context, chCtx channels.ChannelContext) RunResult {
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
	a := r.getOrCreateAgent(chatDir, workspacePath, chCtx)

	// Sync missed messages from log.jsonl into the agent context.
	state := a.GetState()
	synced := SyncLogToMessages(chatDir, state.Messages, chCtx.MessageTS())
	for _, um := range synced {
		a.AppendMessage(um)
	}

	// Refresh system prompt with fresh memory + skills.
	memory := GetMemory(chatDir, r.workingDir)
	skills := LoadAllSkills(chatDir, r.workingDir, workspacePath, r.chatID)
	systemPrompt := BuildSystemPrompt(workspacePath, r.chatID, memory, r.sandbox.cfg, skills)
	a.SetSystemPrompt(systemPrompt)

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
			callID := ev.ToolCallID
			toolName := ev.ToolName
			args, _ := ev.Args.(map[string]any)
			argsSummary := toolArgsSummary(toolName, args)
			fmt.Printf("[tool/start] chat=%d id=%s tool=%s args=%q\n",
				r.chatID, callID, toolName, argsSummary)

		case agent.EventTypeToolExecutionEnd:
			callID := ev.ToolCallID
			toolName := ev.ToolName
			isError := ev.IsError
			result := ""
			if res, ok := ev.Result.(agent.AgentToolResult); ok {
				result = extractResultText(res)
			}
			fmt.Printf("[tool/end] chat=%d id=%s tool=%s isError=%v result_len=%d\n",
				r.chatID, callID, toolName, isError, len(result))

		case agent.EventTypeMessageEnd:
			if ev.Message == nil {
				break
			}
			msg, ok := ev.Message.(types.AssistantMessage)
			if !ok {
				if msgPtr, ok2 := ev.Message.(*types.AssistantMessage); ok2 && msgPtr != nil {
					msg = *msgPtr
					ok = true
				}
			}
			if !ok {
				// UserMessage / ToolResultMessage 也会触发此事件，静默跳过.
				break
			}
			fmt.Printf("[moms] chat %d: LLM reply stop=%q err=%q blocks=%d\n", r.chatID, msg.StopReason, msg.ErrorMessage, len(msg.Content))
			if msg.StopReason != "" {
				stopReason = string(msg.StopReason)
			}
			if msg.ErrorMessage != "" {
				runErr = fmt.Errorf("%s", msg.ErrorMessage)
			}
			for _, block := range msg.Content {
				switch b := block.(type) {
				case *types.ThinkingContent:
					if b != nil && strings.TrimSpace(b.Thinking) != "" {
						thinking := b.Thinking
						enqueue(func() error {
							return chCtx.RespondInThread(thinking)
						})
					}
				case *types.TextContent:
					if b != nil && strings.TrimSpace(b.Text) != "" {
						text := b.Text
						enqueue(func() error {
							return chCtx.Respond(text, true)
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
		chCtx.SenderName(),
		chCtx.MessageText(),
	)

	var promptErr error
	images := chCtx.Images()
	// Capture message count before the LLM run so we can append only new ones.
	prevMsgCount := len(a.GetState().Messages)
	if len(images) > 0 {
		promptErr = a.PromptWithImages(ctx, userMessage, images)
	} else {
		promptErr = a.Prompt(ctx, userMessage)
	}
	if promptErr != nil {
		fmt.Printf("[moms] chat %d prompt error: %v\n", r.chatID, promptErr)
	}
	a.WaitForIdle()

	// Persist new messages to ContextStore (append-only, crash-safe).
	// Compare the previous message count with the current to find what's new.
	newState := a.GetState()
	if len(newState.Messages) > prevMsgCount {
		for i := prevMsgCount; i < len(newState.Messages); i++ {
			if err := r.store.AddMessage(r.chatID, newState.Messages[i]); err != nil {
				fmt.Printf("[moms] failed to persist message for chat %d: %v\n", r.chatID, err)
			}
		}
	}

	// Apply hard compaction (force compression) if enabled.
	if r.settings != nil && r.settings.Compaction != nil && r.settings.Compaction.Enabled {
		allMsgs := newState.Messages
		maxTokens := r.settings.Compaction.KeepRecentTokens
		if maxTokens > 0 && EstimateTokens(allMsgs) > maxTokens {
			compacted := r.forceCompress(allMsgs)
			if len(compacted) < len(allMsgs) {
				var agentMsgs []agent.AgentMessage
				for _, m := range compacted {
					agentMsgs = append(agentMsgs, m)
				}
				a.ReplaceMessages(agentMsgs)
				if err := r.store.SetHistory(r.chatID, agentMsgs); err != nil {
					fmt.Printf("[moms] failed to persist compacted context for chat %d: %v\n", r.chatID, err)
				}
				fmt.Printf("[moms] chat %d: hard compaction from %d to %d messages\n",
					r.chatID, len(allMsgs), len(compacted))
			}
		}
	}

	// Trigger soft summarization asynchronously.
	r.summarizer.MaybeSummarize(r.chatID, r.model, r.settings)

	// Wait for all queued Telegram API calls to complete.
	queueWg.Wait()

	// Handle aborted.
	if ctx.Err() != nil {
		stopReason = "aborted"
	}

	if promptErr != nil && runErr == nil {
		runErr = promptErr
	}

	// Handle error.
	if runErr != nil {
		_ = chCtx.ReplaceMessage("_Sorry, something went wrong_")
		_ = chCtx.RespondInThread(fmt.Sprintf("_Error: %s_", runErr.Error()))
	}

	// Done - remove working indicator.
	_ = chCtx.SetWorking(false)

	return RunResult{StopReason: stopReason, Error: runErr}
}

// getOrCreateAgent returns the persistent agent for this chat, creating it if needed.
func (r *Runner) getOrCreateAgent(chatDir, workspacePath string, chCtx channels.ChannelContext) *agent.Agent {
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
			return chCtx.UploadFile(filePath, title)
		}),
	}
	if r.registryMgr != nil {
		agentTools = append(agentTools,
			skillstools.NewFindSkillsTool(r.registryMgr, r.searchCache),
			skillstools.NewInstallSkillTool(r.registryMgr, workspacePath),
		)
	}

	// Web tools from env vars.
	if searchTool, err := newWebSearchToolFromEnv(); err != nil {
		fmt.Printf("[moms] web_search init error: %v\n", err)
	} else if searchTool != nil {
		agentTools = append(agentTools, searchTool)
	}
	if fetchEnabled := os.Getenv("MOMS_WEB_FETCH"); fetchEnabled == "true" || fetchEnabled == "1" {
		fetchTool, err := NewWebFetchAgentTool(WebFetchConfig{
			Proxy: os.Getenv("MOMS_WEB_PROXY"),
		})
		if err != nil {
			fmt.Printf("[moms] web_fetch init error: %v\n", err)
		} else {
			agentTools = append(agentTools, fetchTool)
		}
	}

	a := agent.NewAgent(agent.AgentConfig{
		GetAPIKey: r.getAPIKey,
		InitialState: &agent.AgentState{
			SystemPrompt:     systemPrompt,
			Model:            model,
			ThinkingLevel:    agent.ThinkingLevelOff,
			Tools:            agentTools,
			Messages:         []agent.AgentMessage{},
			PendingToolCalls: make(map[string]struct{}),
		},
	})

	// Load existing messages from ContextStore (append-only JSONL with Summary).
	if msgs, err := r.store.GetHistory(r.chatID); err == nil && len(msgs) > 0 {
		msgs = sanitizeHistory(msgs)
		a.ReplaceMessages(msgs)
		fmt.Printf("[moms] chat %d: loaded %d messages from context store\n", r.chatID, len(msgs))
	}

	r.agentInst = a
	return a
}


// newWebSearchToolFromEnv creates a WebSearchAgentTool from environment variables.
// Returns nil, nil if MOMS_WEB_SEARCH is not set.
//
// Environment variables:
//   - MOMS_WEB_SEARCH: provider name (brave/tavily/duckduckgo/perplexity/searxng/glm)
//   - MOMS_WEB_MAX_RESULTS: max results (default 5)
//   - MOMS_WEB_PROXY: optional proxy URL
//   - MOMS_BRAVE_API_KEY, MOMS_TAVILY_API_KEY, MOMS_TAVILY_BASE_URL
//   - MOMS_PERPLEXITY_API_KEY, MOMS_SEARXNG_URL
//   - MOMS_GLM_API_KEY, MOMS_GLM_SEARCH_ENGINE, MOMS_GLM_BASE_URL
func newWebSearchToolFromEnv() (*WebSearchAgentTool, error) {
	provider := os.Getenv("MOMS_WEB_SEARCH")
	if provider == "" {
		return nil, nil
	}
	maxResults := 5
	if v := os.Getenv("MOMS_WEB_MAX_RESULTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxResults = n
		}
	}
	return NewWebSearchAgentTool(WebSearchConfig{
		Provider:         provider,
		MaxResults:       maxResults,
		Proxy:            os.Getenv("MOMS_WEB_PROXY"),
		BraveAPIKey:      os.Getenv("MOMS_BRAVE_API_KEY"),
		TavilyAPIKey:     os.Getenv("MOMS_TAVILY_API_KEY"),
		TavilyURL:        os.Getenv("MOMS_TAVILY_BASE_URL"),
		PerplexityAPIKey: os.Getenv("MOMS_PERPLEXITY_API_KEY"),
		SearXNGURL:       os.Getenv("MOMS_SEARXNG_URL"),
		GLMAPIKey:        os.Getenv("MOMS_GLM_API_KEY"),
		GLMEngine:        os.Getenv("MOMS_GLM_SEARCH_ENGINE"),
		GLMURL:           os.Getenv("MOMS_GLM_BASE_URL"),
	})
}

// extractResultText extracts text from AgentToolResult.
func extractResultText(r agent.AgentToolResult) string {
	for _, block := range r.Content {
		if tc, ok := block.(*types.TextContent); ok && tc != nil {
			return tc.Text
		}
	}
	return ""
}

// toolArgsSummary extracts a one-line human-readable summary of the key argument(s).
func toolArgsSummary(name string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch name {
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			return TruncateStr(cmd, 120)
		}
	case "read":
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "write":
		if path, ok := args["path"].(string); ok {
			content, _ := args["content"].(string)
			return fmt.Sprintf("%s (%d bytes)", path, len(content))
		}
	case "edit":
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "attach":
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "find_skills":
		if q, ok := args["query"].(string); ok {
			return fmt.Sprintf("query: %q", TruncateStr(q, 60))
		}
	case "install_skill":
		if n, ok := args["name"].(string); ok {
			return fmt.Sprintf("name: %q", n)
		}
	case "web_search":
		if q, ok := args["query"].(string); ok {
			return fmt.Sprintf("query: %q", TruncateStr(q, 80))
		}
	case "web_fetch":
		if u, ok := args["url"].(string); ok {
			return TruncateStr(u, 100)
		}
	}
	// Fallback: compact JSON of all args.
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return TruncateStr(string(b), 120)
}

// forceCompress mirrors PicoClaw's forceCompression: drops the oldest 50% of
// conversation messages. The compression note is appended to the first message
// (usually the system prompt) to avoid having two consecutive system messages,
// which some APIs (e.g. Zhipu) reject.
func (r *Runner) forceCompress(msgs []types.AgentMessage) []types.AgentMessage {
	if len(msgs) <= 4 {
		return msgs
	}

	// msgs[0] is typically the system-prompt user message.
	// We operate on msgs[1:len-1] as "conversation".
	conversation := msgs[1 : len(msgs)-1]
	if len(conversation) == 0 {
		return msgs
	}

	mid := len(conversation) / 2
	dropped := mid
	kept := conversation[mid:]

	result := make([]types.AgentMessage, 0, 1+len(kept)+1)

	// Append the compression note to the first message instead of injecting a
	// new system message, so APIs that disallow two consecutive system messages
	// remain compatible.
	note := fmt.Sprintf(
		"\n\n[System Note: Emergency compression dropped %d oldest messages due to context limit]",
		dropped,
	)
	first := msgs[0]
	if um, ok := first.(types.UserMessage); ok {
		um.Content = extractText(um.Content) + note
		result = append(result, um)
	} else {
		result = append(result, first)
	}

	result = append(result, kept...)
	result = append(result, msgs[len(msgs)-1])
	return result
}
