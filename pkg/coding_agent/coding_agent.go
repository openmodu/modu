package coding_agent

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/coding_agent/compaction"
	"github.com/crosszan/modu/pkg/coding_agent/eventbus"
	"github.com/crosszan/modu/pkg/coding_agent/extension"
	"github.com/crosszan/modu/pkg/coding_agent/resource"
	"github.com/crosszan/modu/pkg/coding_agent/session"
	"github.com/crosszan/modu/pkg/coding_agent/skills"
	"github.com/crosszan/modu/pkg/coding_agent/tools"
	"github.com/crosszan/modu/pkg/llm"
	"github.com/crosszan/modu/pkg/providers"
	"github.com/crosszan/modu/pkg/types"
)

// CodingSessionOptions configures a new CodingSession.
type CodingSessionOptions struct {
	// Cwd is the working directory.
	Cwd string
	// AgentDir is the configuration directory (default: ~/.coding_agent/).
	AgentDir string
	// Model is the LLM model to use.
	Model *types.Model
	// ThinkingLevel controls reasoning depth.
	ThinkingLevel agent.ThinkingLevel
	// Tools are the tools to make available. If nil, defaults to AllTools.
	Tools []agent.AgentTool
	// CustomTools are additional tools provided by the caller.
	CustomTools []agent.AgentTool
	// Extensions are extensions to initialize.
	Extensions []extension.Extension
	// CustomSystemPrompt overrides the default system prompt.
	CustomSystemPrompt string
	// GetAPIKey retrieves an API key for a provider.
	GetAPIKey func(provider string) (string, error)
	// StreamFn overrides the default stream function.
	StreamFn agent.StreamFn
}

// CodingSession is the main entry point for the coding agent system.
type CodingSession struct {
	agent          *agent.Agent
	sessionManager *session.Manager
	sessionTree    *session.Tree
	config         *Config
	extensions     *extension.Runner
	skillManager   *skills.Manager
	templateMgr    *skills.TemplateManager
	resources      *resource.Loader
	cwd            string
	agentDir       string
	model          *types.Model
	activeTools    []agent.AgentTool
	slashCommands  map[string]SlashCommand
	getAPIKey      func(provider string) (string, error)
	streamFn       agent.StreamFn
	// totalTokens tracks accumulated token usage for auto-compaction.
	totalTokens   int
	retryManager  *RetryManager
	eventBus      eventbus.EventBusController
	scopedModels  []string
	thinkingLevel agent.ThinkingLevel

	// RPC parity fields
	sessionName    string
	isCompacting   bool
	bashCancel     context.CancelFunc
	bashMu         sync.Mutex
	sessionStarted int64
}

// NewCodingSession creates and initializes a new coding session.
func NewCodingSession(opts CodingSessionOptions) (*CodingSession, error) {
	if opts.Cwd == "" {
		return nil, fmt.Errorf("Cwd is required")
	}
	if opts.Model == nil {
		return nil, fmt.Errorf("Model is required")
	}

	// Default agent directory
	agentDir := opts.AgentDir
	if agentDir == "" {
		agentDir = resource.DefaultAgentDir()
	}

	// Ensure directories exist
	loader := resource.NewLoader(agentDir, opts.Cwd)
	if err := loader.EnsureAgentDir(); err != nil {
		return nil, fmt.Errorf("failed to ensure agent dir: %w", err)
	}

	// Load config
	cfg, err := LoadConfig(agentDir, opts.Cwd)
	if err != nil {
		cfg = DefaultConfig()
	}

	// Override config with options
	if opts.ThinkingLevel != "" {
		cfg.ThinkingLevel = opts.ThinkingLevel
	}

	// Set up tools
	activeTools := opts.Tools
	if activeTools == nil {
		activeTools = tools.AllTools(opts.Cwd)
	}
	if len(opts.CustomTools) > 0 {
		activeTools = append(activeTools, opts.CustomTools...)
	}

	// Create session manager
	sessionMgr, err := session.NewManager(agentDir, opts.Cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to create session manager: %w", err)
	}

	// Record session start
	_ = sessionMgr.Append(session.NewEntry(session.EntryTypeSessionInfo, "", session.SessionInfoData{
		Cwd:       opts.Cwd,
		StartTime: time.Now().UnixMilli(),
	}))

	// Create extension runner
	extRunner := extension.NewRunner()

	// Initialize skills
	skillMgr := skills.NewManager(agentDir, opts.Cwd)
	_ = skillMgr.Discover()

	templateMgr := skills.NewTemplateManager(agentDir, opts.Cwd)
	_ = templateMgr.Discover()

	// Build system prompt
	promptBuilder := NewSystemPromptBuilder(opts.Cwd)
	promptBuilder.SetTools(activeTools)

	if opts.CustomSystemPrompt != "" {
		promptBuilder.SetCustomPrompt(opts.CustomSystemPrompt)
	} else if cfg.CustomSystemPrompt != "" {
		promptBuilder.SetCustomPrompt(cfg.CustomSystemPrompt)
	}

	for _, p := range cfg.AppendPrompts {
		promptBuilder.AppendPrompt(p)
	}

	// Add skills in XML format per Agent Skills spec
	if skillsPrompt := skillMgr.FormatForPrompt(); skillsPrompt != "" {
		promptBuilder.SetSkillsPrompt(skillsPrompt)
	}

	systemPrompt := promptBuilder.Build()

	// Determine stream function
	streamFn := opts.StreamFn
	if streamFn == nil {
		streamFn = providers.StreamDefault
	}

	// Determine API key function
	getAPIKey := opts.GetAPIKey
	if getAPIKey == nil {
		keyStore := NewAPIKeyStore(agentDir)
		_ = keyStore.Load()
		getAPIKey = func(provider string) (string, error) {
			key, ok := keyStore.Get(provider)
			if ok {
				return key, nil
			}
			key = llm.GetEnvAPIKey(provider)
			if key != "" {
				return key, nil
			}
			return "", fmt.Errorf("no API key found for provider: %s", provider)
		}
	}

	// Create the underlying agent
	ag := agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentState{
			SystemPrompt:  systemPrompt,
			Model:         opts.Model,
			ThinkingLevel: cfg.ThinkingLevel,
			Tools:         activeTools,
		},
		StreamFn:  streamFn,
		GetAPIKey: getAPIKey,
	})

	cs := &CodingSession{
		agent:          ag,
		sessionManager: sessionMgr,
		sessionTree:    session.NewTree(sessionMgr),
		config:         cfg,
		extensions:     extRunner,
		skillManager:   skillMgr,
		templateMgr:    templateMgr,
		resources:      loader,
		cwd:            opts.Cwd,
		agentDir:       agentDir,
		model:          opts.Model,
		activeTools:    activeTools,
		slashCommands:  make(map[string]SlashCommand),
		getAPIKey:      getAPIKey,
		streamFn:       streamFn,
		retryManager:   NewRetryManager(cfg.RetrySettings, cfg.AutoRetry),
		eventBus:       eventbus.NewEventBus(),
		scopedModels:   cfg.ScopedModels,
		thinkingLevel:  cfg.ThinkingLevel,
		sessionStarted: time.Now().UnixMilli(),
	}

	// Subscribe to events for token usage tracking (auto-compaction)
	ag.Subscribe(func(event agent.AgentEvent) {
		if event.Type == agent.EventTypeMessageEnd {
			if msg, ok := event.Message.(types.AssistantMessage); ok {
				cs.totalTokens += msg.Usage.TotalTokens
			} else if msg, ok := event.Message.(*types.AssistantMessage); ok {
				cs.totalTokens += msg.Usage.TotalTokens
			}
		}
	})

	// Register built-in slash commands
	for _, cmd := range BuiltinCommands() {
		cs.slashCommands[cmd.Name] = cmd
	}

	// Initialize extensions
	if len(opts.Extensions) > 0 {
		extRunner.SetCallbacks(
			func(text string) error {
				msg := &CustomMessage{Source: "extension", Text: text}
				ag.Steer(msg.ToLlmMessage())
				return nil
			},
			func(names []string) {
				cs.SetActiveTools(names)
			},
			func(provider, modelID string) error {
				return cs.SetModelByID(provider, modelID)
			},
		)

		if err := extRunner.Init(opts.Extensions); err != nil {
			return nil, fmt.Errorf("failed to init extensions: %w", err)
		}

		// Add extension tools
		for _, tool := range extRunner.GetTools() {
			cs.activeTools = append(cs.activeTools, tool)
		}

		// Apply extension hooks to all tools via WrapTools
		hooks := extRunner.GetHooks()
		if len(hooks) > 0 {
			cs.activeTools = extension.WrapTools(cs.activeTools, hooks)
		}

		ag.SetTools(cs.activeTools)

		// Register extension slash commands
		for _, cmd := range extRunner.GetCommands() {
			cmdName := cmd.Name // capture for closure
			cs.slashCommands[cmd.Name] = SlashCommand{
				Name:        cmd.Name,
				Description: cmd.Description,
				Handler: func(s *CodingSession, args string) error {
					return extRunner.ExecuteCommand(cmdName, args)
				},
			}
		}
	}

	return cs, nil
}

// Prompt sends a user message and starts processing.
func (s *CodingSession) Prompt(ctx context.Context, text string) error {
	// Check for slash commands
	if strings.HasPrefix(text, "/") {
		parts := strings.SplitN(text[1:], " ", 2)
		cmdName := parts[0]
		cmdArgs := ""
		if len(parts) > 1 {
			cmdArgs = parts[1]
		}

		if cmd, ok := s.slashCommands[cmdName]; ok {
			return cmd.Handler(s, cmdArgs)
		}

		// Check template expansion
		if expanded, ok := s.templateMgr.Expand(text); ok {
			text = expanded
		}

		// Check skills
		if skill, ok := s.skillManager.Get(cmdName); ok {
			text = skill.Content
			if cmdArgs != "" {
				text = text + "\n\n" + cmdArgs
			}
		}
	}

	// Record to session
	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeMessage, "", session.MessageData{
		Role:    agent.RoleUser,
		Content: text,
	}))

	err := s.agent.Prompt(ctx, text)
	if err != nil {
		return err
	}

	// Auto-compaction: check if we should compact after the agent finishes
	s.maybeAutoCompact(ctx)

	return nil
}

// Steer injects a high-priority message during processing.
func (s *CodingSession) Steer(text string) {
	msg := types.UserMessage{
		Role:    "user",
		Content: text,
	}
	s.agent.Steer(msg)
}

// FollowUp queues a message for processing after the current task.
func (s *CodingSession) FollowUp(text string) {
	msg := types.UserMessage{
		Role:    "user",
		Content: text,
	}
	s.agent.FollowUp(msg)
}

// Subscribe registers an event listener. Returns an unsubscribe function.
func (s *CodingSession) Subscribe(fn func(agent.AgentEvent)) func() {
	return s.agent.Subscribe(fn)
}

// SetActiveTools sets which tools are active by name.
func (s *CodingSession) SetActiveTools(names []string) {
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}

	var active []agent.AgentTool
	for _, tool := range s.activeTools {
		if nameSet[tool.Name()] {
			active = append(active, tool)
		}
	}

	s.agent.SetTools(active)
}

// GetActiveToolNames returns the names of currently active tools.
func (s *CodingSession) GetActiveToolNames() []string {
	state := s.agent.GetState()
	names := make([]string, len(state.Tools))
	for i, t := range state.Tools {
		names[i] = t.Name()
	}
	return names
}

// SetModel changes the active model.
func (s *CodingSession) SetModel(model *types.Model) {
	s.model = model
	s.agent.SetModel(model)

	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeModelChange, "", session.ModelChangeData{
		Provider: model.ProviderID,
		ModelID:  model.ID,
	}))
}

// SetModelByID changes the active model by provider and model ID.
func (s *CodingSession) SetModelByID(provider, modelID string) error {
	llmModel := llm.GetModel(llm.Provider(provider), modelID)
	if llmModel == nil {
		return fmt.Errorf("model not found: %s/%s", provider, modelID)
	}
	s.SetModel(llmModelToProviders(llmModel))
	return nil
}

// Compact triggers context compaction.
func (s *CodingSession) Compact(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.isCompacting = true
	defer func() { s.isCompacting = false }()

	state := s.agent.GetState()

	result, err := compaction.Compact(ctx, state.Messages, compaction.Options{
		PreserveRecent: s.config.CompactionSettings.PreserveRecentMessages,
		Model:          s.model,
		GetAPIKey:      s.getAPIKey,
		StreamFn:       s.streamFn,
	})
	if err != nil {
		return fmt.Errorf("compaction failed: %w", err)
	}

	// Replace messages
	s.agent.ReplaceMessages(result.Messages)

	// Reset token counter after compaction
	s.totalTokens = 0

	// Record compaction
	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeCompaction, "", session.CompactionData{
		Summary:       result.Summary,
		OriginalCount: result.OriginalCount,
		NewCount:      result.NewCount,
	}))

	return nil
}

// defaultContextWindow is the assumed context window when not available from the model.
const defaultContextWindow = 128000

// maybeAutoCompact checks whether auto-compaction should be triggered
// based on accumulated token usage vs. the assumed context window.
func (s *CodingSession) maybeAutoCompact(ctx context.Context) {
	if !s.config.AutoCompaction {
		return
	}
	if s.model == nil {
		return
	}

	threshold := s.config.CompactionSettings.MaxContextPercentage
	if threshold <= 0 {
		threshold = 80.0
	}

	usagePercent := float64(s.totalTokens) / float64(defaultContextWindow) * 100.0
	if usagePercent >= threshold {
		_ = s.Compact(ctx)
	}
}

// Fork creates a new branch from the given entry ID.
func (s *CodingSession) Fork(entryID string) error {
	return s.sessionManager.Fork(entryID)
}

// NavigateTree navigates to a specific point in the session tree.
func (s *CodingSession) NavigateTree(entryID string) error {
	return s.sessionTree.NavigateTo(entryID)
}

// GetAgent returns the underlying agent.
func (s *CodingSession) GetAgent() *agent.Agent {
	return s.agent
}

// WaitForIdle blocks until the agent is idle.
func (s *CodingSession) WaitForIdle() {
	s.agent.WaitForIdle()
}

// Abort cancels the current operation.
func (s *CodingSession) Abort() {
	s.agent.Abort()
}

// GetConfig returns the current configuration.
func (s *CodingSession) GetConfig() *Config {
	return s.config
}

// CycleModel cycles to the next model in the scopedModels list.
// Returns the new model, or nil if no scoped models are configured.
func (s *CodingSession) CycleModel() *types.Model {
	if len(s.scopedModels) == 0 {
		return nil
	}

	currentID := s.model.ID
	nextIdx := 0
	for i, id := range s.scopedModels {
		if id == currentID {
			nextIdx = (i + 1) % len(s.scopedModels)
			break
		}
	}

	nextID := s.scopedModels[nextIdx]
	llmModel := llm.GetModel("", nextID)
	var model *types.Model
	if llmModel != nil {
		model = llmModelToProviders(llmModel)
	} else {
		model = &types.Model{ID: nextID, Name: nextID}
	}

	s.SetModel(model)
	s.eventBus.Emit(sessionEventChannel, SessionEvent{
		Type:     SessionEventModelChange,
		Provider: model.ProviderID,
		ModelID:  model.ID,
	})
	return model
}

// CycleThinkingLevel cycles through: off -> low -> medium -> high -> off.
func (s *CodingSession) CycleThinkingLevel() agent.ThinkingLevel {
	var next agent.ThinkingLevel
	switch s.thinkingLevel {
	case agent.ThinkingLevelOff:
		next = agent.ThinkingLevelLow
	case agent.ThinkingLevelLow:
		next = agent.ThinkingLevelMedium
	case agent.ThinkingLevelMedium:
		next = agent.ThinkingLevelHigh
	case agent.ThinkingLevelHigh:
		next = agent.ThinkingLevelOff
	default:
		next = agent.ThinkingLevelLow
	}

	s.SetThinkingLevel(next)
	return next
}

// SetThinkingLevel sets the thinking level.
func (s *CodingSession) SetThinkingLevel(level agent.ThinkingLevel) {
	s.thinkingLevel = level
	s.agent.SetThinkingLevel(level)
	s.eventBus.Emit(sessionEventChannel, SessionEvent{
		Type:  SessionEventThinkingChange,
		Level: string(level),
	})
}

// GetThinkingLevel returns the current thinking level.
func (s *CodingSession) GetThinkingLevel() agent.ThinkingLevel {
	return s.thinkingLevel
}

// GetModel returns the current model.
func (s *CodingSession) GetModel() *types.Model {
	return s.model
}

// IsStreaming returns whether the agent is currently streaming.
func (s *CodingSession) IsStreaming() bool {
	return s.agent.GetState().IsStreaming
}

// GetSessionID returns the current session ID.
func (s *CodingSession) GetSessionID() string {
	return s.agent.GetSessionID()
}

// SetAutoCompaction enables or disables auto-compaction.
func (s *CodingSession) SetAutoCompaction(enabled bool) {
	s.config.AutoCompaction = enabled
}

// SetAutoRetry enables or disables auto-retry.
func (s *CodingSession) SetAutoRetry(enabled bool) {
	s.config.AutoRetry = enabled
	s.retryManager.SetEnabled(enabled)
}

// AbortRetry cancels any pending retry wait.
func (s *CodingSession) AbortRetry() {
	s.retryManager.AbortRetry()
}

// GetMessages returns the current message history.
func (s *CodingSession) GetMessages() []agent.AgentMessage {
	return s.agent.GetState().Messages
}

// GetEventBus returns the session's event bus.
func (s *CodingSession) GetEventBus() eventbus.EventBusController {
	return s.eventBus
}

// SubscribeSession registers a handler for session-level events.
// Returns an unsubscribe function.
func (s *CodingSession) SubscribeSession(fn func(SessionEvent)) func() {
	return s.eventBus.On(sessionEventChannel, func(data any) {
		if event, ok := data.(SessionEvent); ok {
			fn(event)
		}
	})
}

// GetSessionFile returns the session file path.
func (s *CodingSession) GetSessionFile() string {
	return s.sessionManager.FilePath()
}

// SetSessionName sets the display name for this session.
func (s *CodingSession) SetSessionName(name string) {
	s.sessionName = name
}

// GetSessionName returns the display name for this session.
func (s *CodingSession) GetSessionName() string {
	return s.sessionName
}

// IsCompacting returns whether compaction is currently in progress.
func (s *CodingSession) IsCompacting() bool {
	return s.isCompacting
}

// GetLastAssistantText returns the text content of the last assistant message.
func (s *CodingSession) GetLastAssistantText() string {
	msgs := s.agent.GetState().Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		msg, ok := msgs[i].(types.AssistantMessage)
		if !ok {
			if ptr, ok2 := msgs[i].(*types.AssistantMessage); ok2 {
				msg = *ptr
			} else {
				continue
			}
		}
		for _, block := range msg.Content {
			if tc, ok := block.(*types.TextContent); ok && tc != nil && tc.Text != "" {
				return tc.Text
			}
		}
	}
	return ""
}

// GetForkMessages returns user messages from the session history, suitable for forking.
func (s *CodingSession) GetForkMessages() []ForkMessage {
	entries := s.sessionManager.Load()
	var result []ForkMessage
	for _, entry := range entries {
		if entry.Type != session.EntryTypeMessage {
			continue
		}
		data, ok := entry.Data.(session.MessageData)
		if !ok {
			// Try map-based extraction (from JSON deserialization)
			if m, ok := entry.Data.(map[string]interface{}); ok {
				role, _ := m["role"].(string)
				if role != string(agent.RoleUser) {
					continue
				}
				content, _ := m["content"].(string)
				result = append(result, ForkMessage{
					EntryID: entry.ID,
					Role:    role,
					Content: content,
				})
			}
			continue
		}
		if data.Role != agent.RoleUser {
			continue
		}
		content, _ := data.Content.(string)
		result = append(result, ForkMessage{
			EntryID: entry.ID,
			Role:    string(data.Role),
			Content: content,
		})
	}
	return result
}

// GetSessionStats returns aggregate statistics for the current session.
func (s *CodingSession) GetSessionStats() SessionStats {
	msgs := s.agent.GetState().Messages
	now := time.Now().UnixMilli()
	return SessionStats{
		TotalTokens:    s.totalTokens,
		MessageCount:   len(msgs),
		SessionStarted: s.sessionStarted,
		DurationMs:     now - s.sessionStarted,
	}
}

// GetAvailableModels returns all registered models from all providers.
func (s *CodingSession) GetAvailableModels() []*types.Model {
	var result []*types.Model
	for _, p := range llm.GetProviders() {
		for _, m := range llm.GetModels(p) {
			result = append(result, llmModelToProviders(m))
		}
	}
	return result
}

// llmModelToProviders converts a *llm.Model to *types.Model.
func llmModelToProviders(m *llm.Model) *types.Model {
	if m == nil {
		return nil
	}
	return &types.Model{
		ID:         m.ID,
		Name:       m.Name,
		ProviderID: string(m.Provider),
	}
}

// ExecuteBash executes a shell command and returns the result.
func (s *CodingSession) ExecuteBash(ctx context.Context, command string, timeoutMs int) (*BashResult, error) {
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}

	bashCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)

	s.bashMu.Lock()
	s.bashCancel = cancel
	s.bashMu.Unlock()

	defer func() {
		cancel()
		s.bashMu.Lock()
		s.bashCancel = nil
		s.bashMu.Unlock()
	}()

	cmd := exec.CommandContext(bashCtx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = s.cwd

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, err
		}
	}

	return &BashResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// AbortBash cancels the currently running bash command, if any.
func (s *CodingSession) AbortBash() {
	s.bashMu.Lock()
	defer s.bashMu.Unlock()
	if s.bashCancel != nil {
		s.bashCancel()
	}
}

// ExportHTML writes the session messages as a simple HTML file.
func (s *CodingSession) ExportHTML(path string) error {
	msgs := s.agent.GetState().Messages

	var buf bytes.Buffer
	buf.WriteString("<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"><title>Session Export</title></head><body>\n")

	for _, msg := range msgs {
		role := "unknown"
		content := ""

		switch m := msg.(type) {
		case types.UserMessage:
			role = "user"
			if str, ok := m.Content.(string); ok {
				content = str
			}
		case *types.UserMessage:
			role = "user"
			if str, ok := m.Content.(string); ok {
				content = str
			}
		case types.AssistantMessage:
			role = "assistant"
			for _, block := range m.Content {
				if tc, ok := block.(*types.TextContent); ok && tc != nil {
					content += tc.Text
				}
			}
		case *types.AssistantMessage:
			role = "assistant"
			for _, block := range m.Content {
				if tc, ok := block.(*types.TextContent); ok && tc != nil {
					content += tc.Text
				}
			}
		}

		buf.WriteString(fmt.Sprintf("<div class=\"message %s\"><strong>%s:</strong><pre>%s</pre></div>\n",
			role, role, html.EscapeString(content)))
	}

	buf.WriteString("</body></html>\n")
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// SwitchSession loads messages from a different session file and replaces the current agent messages.
func (s *CodingSession) SwitchSession(sessionFile string) error {
	newMgr, err := session.NewManagerFromFile(sessionFile)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	entries := newMgr.Load()
	var messages []agent.AgentMessage
	for _, entry := range entries {
		if entry.Type != session.EntryTypeMessage {
			continue
		}
		if m, ok := entry.Data.(map[string]interface{}); ok {
			role, _ := m["role"].(string)
			content, _ := m["content"].(string)
			switch role {
			case "user":
				messages = append(messages, types.UserMessage{Role: "user", Content: content})
			case "assistant":
				messages = append(messages, types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: content}}})
			}
		}
	}

	s.agent.ReplaceMessages(messages)
	return nil
}
