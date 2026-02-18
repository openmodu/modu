package coding_agent

import (
	"context"
	"fmt"
	"strings"
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
)

// CodingSessionOptions configures a new CodingSession.
type CodingSessionOptions struct {
	// Cwd is the working directory.
	Cwd string
	// AgentDir is the configuration directory (default: ~/.coding_agent/).
	AgentDir string
	// Model is the LLM model to use.
	Model *llm.Model
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
	model          *llm.Model
	activeTools    []agent.AgentTool
	slashCommands  map[string]SlashCommand
	getAPIKey      func(provider string) (string, error)
	streamFn       agent.StreamFn
	// totalTokens tracks accumulated token usage for auto-compaction.
	totalTokens    int
	retryManager   *RetryManager
	eventBus       eventbus.EventBusController
	scopedModels   []string
	thinkingLevel  agent.ThinkingLevel
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

	// Add skill descriptions
	for _, desc := range skillMgr.GetDescriptions() {
		promptBuilder.AddSkillDescription(desc)
	}

	systemPrompt := promptBuilder.Build()

	// Determine stream function
	streamFn := opts.StreamFn
	if streamFn == nil {
		streamFn = func(model *llm.Model, ctx *llm.Context, sOpts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
			return llm.StreamSimple(model, ctx, sOpts)
		}
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
	}

	// Subscribe to events for token usage tracking (auto-compaction)
	ag.Subscribe(func(event agent.AgentEvent) {
		if event.Type == agent.EventTypeMessageEnd {
			if msg, ok := event.Message.(llm.AssistantMessage); ok {
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
	msg := llm.UserMessage{
		Role:      "user",
		Content:   text,
		Timestamp: time.Now().UnixMilli(),
	}
	s.agent.Steer(msg)
}

// FollowUp queues a message for processing after the current task.
func (s *CodingSession) FollowUp(text string) {
	msg := llm.UserMessage{
		Role:      "user",
		Content:   text,
		Timestamp: time.Now().UnixMilli(),
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
func (s *CodingSession) SetModel(model *llm.Model) {
	s.model = model
	s.agent.SetModel(model)

	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeModelChange, "", session.ModelChangeData{
		Provider: string(model.Provider),
		ModelID:  model.ID,
	}))
}

// SetModelByID changes the active model by provider and model ID.
func (s *CodingSession) SetModelByID(provider, modelID string) error {
	model := llm.GetModel(llm.Provider(provider), modelID)
	if model == nil {
		return fmt.Errorf("model not found: %s/%s", provider, modelID)
	}
	s.SetModel(model)
	return nil
}

// Compact triggers context compaction.
func (s *CodingSession) Compact(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

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

// maybeAutoCompact checks whether auto-compaction should be triggered
// based on accumulated token usage vs. the model's context window.
func (s *CodingSession) maybeAutoCompact(ctx context.Context) {
	if !s.config.AutoCompaction {
		return
	}
	if s.model == nil || s.model.ContextWindow <= 0 {
		return
	}

	threshold := s.config.CompactionSettings.MaxContextPercentage
	if threshold <= 0 {
		threshold = 80.0
	}

	usagePercent := float64(s.totalTokens) / float64(s.model.ContextWindow) * 100.0
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
func (s *CodingSession) CycleModel() *llm.Model {
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
	model := llm.GetModel("", nextID)
	if model == nil {
		// Create a minimal model if not in registry
		model = &llm.Model{
			ID:   nextID,
			Name: nextID,
		}
	}

	s.SetModel(model)
	s.eventBus.Emit(sessionEventChannel, SessionEvent{
		Type:     SessionEventModelChange,
		Provider: string(model.Provider),
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
func (s *CodingSession) GetModel() *llm.Model {
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
