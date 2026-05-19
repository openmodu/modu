package coding_agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/compaction"
	"github.com/openmodu/modu/pkg/coding_agent/eventbus"
	"github.com/openmodu/modu/pkg/coding_agent/extension"
	"github.com/openmodu/modu/pkg/coding_agent/prompts"
	"github.com/openmodu/modu/pkg/coding_agent/resource"
	"github.com/openmodu/modu/pkg/coding_agent/session"
	"github.com/openmodu/modu/pkg/coding_agent/skills"
	"github.com/openmodu/modu/pkg/coding_agent/subagent"
	"github.com/openmodu/modu/pkg/coding_agent/tools"
	"github.com/openmodu/modu/pkg/providers"
	sessiontrace "github.com/openmodu/modu/pkg/trace"
	"github.com/openmodu/modu/pkg/types"
	"github.com/openmodu/modu/pkg/utils"
	oteltrace "go.opentelemetry.io/otel/trace"
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
	// Tools are the tools to make available. If nil, defaults to CodingTools.
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
	// ExtraSubagentDirs adds extra directories to scan for subagent definitions.
	ExtraSubagentDirs []string
	// ScopedModels limits model listing/cycling to these model IDs.
	ScopedModels []string
	// ModelConfigPath records the model config file path for diagnostics.
	ModelConfigPath string
	// OTelTracerProvider reuses an existing OpenTelemetry tracer provider when set.
	OTelTracerProvider oteltrace.TracerProvider
}

// CodingSession is the main entry point for the coding agent system.
type CodingSession struct {
	agent          *agent.Agent
	sessionManager *session.Manager
	sessionTree    *session.Tree
	config         *Config
	extensions     *extension.Runner
	skillManager   *skills.Manager
	promptManager  *prompts.Manager
	resources      *resource.Loader
	memoryStore    *MemoryStore
	subagentLoader *subagent.Loader
	cwd            string
	agentDir       string
	promptBuilder  *SystemPromptBuilder
	model          *types.Model
	activeTools    []agent.AgentTool
	slashCommands  map[string]SlashCommand
	getAPIKey      func(provider string) (string, error)
	streamFn       agent.StreamFn
	lastSavedIndex int
	// totalTokens tracks accumulated token usage for auto-compaction.
	totalTokens     int
	retryManager    *RetryManager
	eventBus        eventbus.EventBusController
	scopedModels    []string
	modelConfigPath string
	thinkingLevel   agent.ThinkingLevel

	// RPC parity fields
	sessionName    string
	isCompacting   bool
	bashCancel     context.CancelFunc
	bashMu         sync.Mutex
	sessionStarted int64
	todoMu         sync.RWMutex
	todos          []TodoItem
	taskManager    *backgroundTaskManager
	planMode       bool
	planMu         sync.RWMutex
	// planDecisionCb presents the plan to the user and returns the decision:
	// "approve", "approve_auto", "reject", or "reject:<feedback>". nil means
	// headless — the plan is auto-approved.
	planDecisionCb func(plan string, steps []string) string
	worktreeMu     sync.Mutex
	originalCwd    string
	worktreePath   string
	contextMu      sync.Mutex
	loadedContexts map[string]struct{}
	harness        *harnessState
	traceRecorder  *sessiontrace.Recorder
	otelBridge     *sessiontrace.OTelBridge

	// approvalManager handles tool execution approval.
	approvalManager *ApprovalManager

	// gitCache holds the last-known git state to avoid spawning git subprocesses
	// on every writeRuntimeState call.
	gitCache cachedGitState
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

	// Initialize memory store (global ~/.coding_agent/memory + project <cwd>/memory)
	memoryStore := NewMemoryStore(agentDir, opts.Cwd)

	// Set up tools
	activeTools := opts.Tools
	if activeTools == nil {
		activeTools = tools.CodingTools(opts.Cwd)
	}
	if len(opts.CustomTools) > 0 {
		activeTools = append(activeTools, opts.CustomTools...)
	}

	// Always include the memory tool
	if cfg.FeatureMemoryTool() {
		activeTools = append(activeTools, tools.NewMemoryTool(memoryStore))
	}
	if cfg.FeatureTodoTool() {
		activeTools = append(activeTools, tools.NewTodoWriteTool(todoStoreAdapter{session: nil}))
	}
	if cfg.FeatureTaskOutputTool() {
		activeTools = append(activeTools, tools.NewTaskOutputTool(nil))
	}
	if cfg.FeaturePlanMode() {
		activeTools = append(activeTools, tools.NewEnterPlanModeTool(planModeAdapter{session: nil}))
		activeTools = append(activeTools, tools.NewExitPlanModeTool(planModeAdapter{session: nil}))
	}
	if cfg.FeatureWorktreeMode() {
		activeTools = append(activeTools, tools.NewEnterWorktreeTool(worktreeAdapter{session: nil}))
		activeTools = append(activeTools, tools.NewExitWorktreeTool(worktreeAdapter{session: nil}))
	}

	// Create session manager
	sessionMgr, err := session.NewManager(agentDir, opts.Cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to create session manager: %w", err)
	}

	// Create extension runner
	extRunner := extension.NewRunner()

	resourceSnapshot := loader.LoadResources()

	// Initialize skills and prompt templates.
	skillMgr := skills.NewManager(agentDir, opts.Cwd)
	skillMgr.SetExtraPaths(resourceSnapshot.SkillPaths)
	_ = skillMgr.Discover()
	promptMgr := prompts.NewManager(agentDir, opts.Cwd)
	promptMgr.SetExtraPaths(resourceSnapshot.PromptPaths)
	_ = promptMgr.Discover()

	// Build system prompt
	promptBuilder := NewSystemPromptBuilder(opts.Cwd)
	promptBuilder.SetModel(opts.Model)
	promptBuilder.SetMemoryStore(memoryStore)
	promptBuilder.SetTools(activeTools)
	for _, ctxFile := range resourceSnapshot.ContextFiles {
		promptBuilder.AddContextFile(ctxFile.Path)
	}

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

	// Determine stream function
	streamFn := opts.StreamFn
	if streamFn == nil {
		streamFn = agent.StreamDefault
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
			return "", fmt.Errorf("no API key found for provider: %s", provider)
		}
	}

	// Discover subagents and register spawn_subagent tool if any are found.
	subagentLoader := subagent.NewLoader()
	subagentLoader.Discover(agentDir, opts.Cwd)
	subagentLoader.DiscoverExtra(opts.ExtraSubagentDirs...)
	taskMgr := newBackgroundTaskManager()
	if cfg.FeatureSpawnSubagentTool() && subagentLoader.Count() > 0 {
		activeTools = append(activeTools, tools.NewSpawnSubagentTool(opts.Cwd, agentDir, subagentLoader, activeTools, opts.Model, getAPIKey, streamFn, func(def *subagent.SubagentDefinition) *subagent.SubagentDefinition {
			return prepareSubagentDefinition(def, skillMgr, memoryStore)
		}, taskMgr, nil))
		if subagentsPrompt := formatSubagentsForPrompt(subagentLoader.List()); subagentsPrompt != "" {
			promptBuilder.AppendPrompt(subagentsPrompt)
		}
	}
	systemPrompt := promptBuilder.Build()

	// Create approval manager
	approvalMgr := NewApprovalManager()
	approvalMgr.SetRules(cfg.Permissions)

	// Create the underlying agent
	ag := agent.NewAgent(agent.AgentConfig{
		GetAPIKey:   getAPIKey,
		ApproveTool: approvalMgr.Approve,
		InitialState: &agent.AgentState{
			SystemPrompt:  systemPrompt,
			Model:         opts.Model,
			ThinkingLevel: cfg.ThinkingLevel,
			Tools:         activeTools,
		},
		StreamFn: streamFn,
	})

	cs := &CodingSession{
		agent:           ag,
		sessionManager:  sessionMgr,
		sessionTree:     session.NewTree(sessionMgr),
		config:          cfg,
		extensions:      extRunner,
		skillManager:    skillMgr,
		promptManager:   promptMgr,
		resources:       loader,
		memoryStore:     memoryStore,
		subagentLoader:  subagentLoader,
		cwd:             opts.Cwd,
		agentDir:        agentDir,
		promptBuilder:   promptBuilder,
		model:           opts.Model,
		activeTools:     activeTools,
		slashCommands:   make(map[string]SlashCommand),
		getAPIKey:       getAPIKey,
		streamFn:        streamFn,
		retryManager:    NewRetryManager(cfg.RetrySettings, cfg.AutoRetry),
		eventBus:        eventbus.NewEventBus(),
		scopedModels:    resolveScopedModels(cfg.ScopedModels, opts.ScopedModels),
		modelConfigPath: opts.ModelConfigPath,
		thinkingLevel:   cfg.ThinkingLevel,
		sessionStarted:  time.Now().UnixMilli(),
		taskManager:     taskMgr,
		loadedContexts:  make(map[string]struct{}),
		harness:         newHarnessState(),
		approvalManager: approvalMgr,
	}
	if cfg.TracingRecorderEnabled() {
		tracePaths := cs.RuntimePaths()
		traceRecorder, traceErr := sessiontrace.NewRecorder(sessiontrace.Options{
			SessionID:        cs.GetSessionID(),
			Cwd:              opts.Cwd,
			Provider:         opts.Model.ProviderID,
			ModelID:          opts.Model.ID,
			EventsFile:       tracePaths.TraceEventsFile,
			SummaryFile:      tracePaths.TraceSummaryFile,
			MaxFileSizeBytes: int64(cfg.TracingRecorderMaxFileSizeMB()) * 1024 * 1024,
			MaxRotatedFiles:  cfg.TracingRecorderMaxRotatedFiles(),
		})
		if traceErr != nil {
			return nil, fmt.Errorf("failed to init trace recorder: %w", traceErr)
		}
		cs.traceRecorder = traceRecorder
		_ = cs.traceRecorder.RecordSessionEvent("session_start", map[string]any{
			"source":    "startup",
			"cwd":       opts.Cwd,
			"provider":  opts.Model.ProviderID,
			"modelId":   opts.Model.ID,
			"sessionId": cs.GetSessionID(),
		})
	}
	if bridgeOpts, ok := cs.otelOptions(opts); ok {
		bridge, bridgeErr := sessiontrace.NewOTelBridge(context.Background(), bridgeOpts)
		if bridgeErr != nil {
			return nil, fmt.Errorf("failed to init otel bridge: %w", bridgeErr)
		}
		cs.otelBridge = bridge
		cs.otelBridge.RecordSessionEvent("session_start", map[string]any{
			"source":    "startup",
			"cwd":       opts.Cwd,
			"provider":  opts.Model.ProviderID,
			"modelId":   opts.Model.ID,
			"sessionId": cs.GetSessionID(),
		})
	}
	approvalMgr.SetObserver(cs)
	approvalMgr.SetBlocker(func(toolName string, args map[string]any) (bool, string) {
		if cs.planModeBlocksTool(toolName) {
			return true, planModeBlockMessage(toolName)
		}
		return false, ""
	})
	taskMgr.SetOnChange(func() { cs.writeRuntimeState() })
	cs.refreshToolsForCwd(cs.cwd)
	cs.replaceTodoTool()
	cs.replaceTaskOutputTool()
	cs.replacePlanTools()
	cs.replaceWorktreeTools()
	cs.installConfigHarnessHooks()
	for _, ctxFile := range resourceSnapshot.ContextFiles {
		cs.loadedContexts[ctxFile.Path] = struct{}{}
	}

	// Subscribe to events for token usage tracking (auto-compaction)
	ag.Subscribe(func(event agent.AgentEvent) {
		if cs.extensions != nil {
			cs.extensions.EmitEvent(event)
		}
		if cs.traceRecorder != nil {
			_ = cs.traceRecorder.RecordAgentEvent(event)
		}
		if cs.otelBridge != nil {
			cs.otelBridge.RecordAgentEvent(event)
		}
		if event.Type == agent.EventTypeMessageEnd {
			addUsage := func(u types.AgentUsage) {
				t := u.TotalTokens
				if t == 0 {
					t = u.Input + u.Output
				}
				cs.totalTokens += t
			}
			if msg, ok := event.Message.(types.AssistantMessage); ok {
				addUsage(msg.Usage)
			} else if msg, ok := event.Message.(*types.AssistantMessage); ok {
				addUsage(msg.Usage)
			}
			cs.handleMessageEnd(event.Message)
			return
		}
		if event.Type == agent.EventTypeToolExecutionEnd && !event.IsError {
			cs.handleToolExecutionEnd(event)
			return
		}
		if event.Type == agent.EventTypeAgentEnd {
			if errText := strings.TrimSpace(cs.agent.GetState().Error); errText != "" {
				cs.runHarnessStopFailure(fmt.Errorf("%s", errText))
			} else {
				cs.runHarnessStop()
			}
			cs.pruneTransientContextMessages()
			go cs.refreshGitRuntimeState()
			cs.writeRuntimeState()
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

	cs.installHarnessLayer()
	cs.runHarnessSessionStart("startup")
	cs.writeRuntimeState()

	return cs, nil
}

func resolveScopedModels(configured, explicit []string) []string {
	if len(explicit) > 0 {
		return append([]string(nil), explicit...)
	}
	return append([]string(nil), configured...)
}

// Prompt sends a user message and starts processing.
func (s *CodingSession) Prompt(ctx context.Context, text string) error {
	input := strings.TrimSpace(text)

	// Check for slash commands
	if strings.HasPrefix(input, "/") {
		parts := strings.SplitN(input[1:], " ", 2)
		cmdName := parts[0]
		cmdArgs := ""
		if len(parts) > 1 {
			cmdArgs = parts[1]
		}

		if cmd, ok := s.slashCommands[cmdName]; ok {
			return cmd.Handler(s, cmdArgs)
		}

		s.refreshResourcePaths()

		expandedTemplate := false
		if s.promptManager != nil {
			if template, ok := s.promptManager.Get(cmdName); ok {
				text = template.Expand(cmdArgs)
				input = strings.TrimSpace(text)
				expandedTemplate = true
			}
		}

		// Check skills when no prompt template claimed the slash command.
		if !expandedTemplate && s.skillManager != nil {
			if skill, ok := s.skillManager.Get(cmdName); ok {
				return s.executeSkill(ctx, input, skill, cmdArgs)
			}
		}
	}

	if err := s.runHarnessUserPromptSubmit(text); err != nil {
		return err
	}

	// Record to session
	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeMessage, "", session.MessageData{
		Role:    agent.RoleUser,
		Content: text,
	}))

	// Hot-reload skills (and other dynamic context) so the agent sees any
	// changes the user made on disk since the last turn.
	s.refreshDynamicSystemPrompt()

	err := s.agent.Prompt(ctx, text)
	if err != nil {
		return err
	}

	// Auto-compaction: check if we should compact after the agent finishes
	s.maybeAutoCompact(ctx)

	return nil
}

func (s *CodingSession) Close(reason string) {
	if s.traceRecorder != nil {
		_ = s.traceRecorder.RecordSessionEvent("session_end", map[string]any{"reason": reason})
		_ = s.traceRecorder.Close()
	}
	if s.otelBridge != nil {
		s.otelBridge.RecordSessionEvent("session_end", map[string]any{"reason": reason})
		_ = s.otelBridge.Close(context.Background(), reason)
	}
	s.runHarnessSessionEnd(reason)
	s.writeRuntimeState()
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
	s.writeRuntimeState()
}

func (s *CodingSession) refreshResourcePaths() resource.ResourceSnapshot {
	if s.resources == nil {
		return resource.ResourceSnapshot{}
	}
	snapshot := s.resources.LoadResources()
	if s.skillManager != nil {
		s.skillManager.SetExtraPaths(snapshot.SkillPaths)
	}
	if s.promptManager != nil {
		s.promptManager.SetExtraPaths(snapshot.PromptPaths)
	}
	return snapshot
}

// SkillInfo is a minimal view of a skill for display purposes.
type SkillInfo struct {
	Name        string
	Description string
	Source      string // "user" or "project"
}

// SubagentInfo is a minimal view of a discovered subagent definition.
type SubagentInfo struct {
	Name        string
	Description string
	Source      string // "user" or "project"
	FilePath    string
}

// GetSkills returns all discovered skills.
func (s *CodingSession) GetSkills() []SkillInfo {
	if s.skillManager == nil {
		return nil
	}
	s.refreshResourcePaths()
	list := s.skillManager.List()
	out := make([]SkillInfo, len(list))
	for i, sk := range list {
		out[i] = SkillInfo{Name: sk.Name, Description: sk.Description, Source: sk.Source}
	}
	return out
}

// GetSubagents returns all discovered subagent definitions.
func (s *CodingSession) GetSubagents() []SubagentInfo {
	if s.subagentLoader == nil {
		return nil
	}
	list := s.subagentLoader.List()
	out := make([]SubagentInfo, len(list))
	for i, def := range list {
		out[i] = SubagentInfo{
			Name:        def.Name,
			Description: def.Description,
			Source:      def.Source,
			FilePath:    def.FilePath,
		}
	}
	return out
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

func (s *CodingSession) handleToolExecutionEnd(event agent.AgentEvent) {
	if s.resources == nil {
		return
	}
	switch event.ToolName {
	case "read", "edit", "write", "grep", "find", "ls":
	default:
		return
	}

	paths := extractToolPaths(event)
	if len(paths) == 0 {
		return
	}

	newContexts := s.collectNewContextFiles(paths)
	if len(newContexts) == 0 {
		return
	}

	text := formatDynamicContextMessage(paths, newContexts)
	if text == "" {
		return
	}
	s.agent.Steer((&CustomMessage{
		Source: nestedContextSource,
		Text:   text,
	}).ToLlmMessage())
}

func extractToolPaths(event agent.AgentEvent) []string {
	var paths []string
	seen := make(map[string]struct{})
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	if result, ok := event.Result.(agent.AgentToolResult); ok {
		if details, ok := result.Details.(map[string]any); ok {
			collectToolPathsFromDetails(details, add)
		}
	}
	if args, ok := event.Args.(map[string]any); ok {
		collectToolPathsFromDetails(args, add)
	}
	return paths
}

func collectToolPathsFromDetails(details map[string]any, add func(string)) {
	if path, _ := details["path"].(string); path != "" {
		add(path)
	}
	switch matched := details["matched_paths"].(type) {
	case []string:
		for _, path := range matched {
			add(path)
		}
	case []any:
		for _, raw := range matched {
			if path, ok := raw.(string); ok {
				add(path)
			}
		}
	}
}

func (s *CodingSession) collectNewContextFiles(paths []string) []resource.ContextFile {
	if len(paths) == 0 {
		return nil
	}

	// Load context files outside the lock: this involves filesystem I/O and
	// a git subprocess, both of which are expensive to hold a mutex across.
	var candidates []resource.ContextFile
	for _, path := range paths {
		candidates = append(candidates, s.resources.LoadContextFilesForPath(path)...)
	}

	s.contextMu.Lock()
	defer s.contextMu.Unlock()

	var out []resource.ContextFile
	for _, file := range candidates {
		if _, seen := s.loadedContexts[file.Path]; seen {
			continue
		}
		s.loadedContexts[file.Path] = struct{}{}
		out = append(out, file)
	}
	return out
}

func formatDynamicContextMessage(targetPaths []string, files []resource.ContextFile) string {
	const (
		maxFileBytes  = 4 * 1024
		maxTotalBytes = 12 * 1024
	)

	if len(files) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, "Additional path-specific instructions became relevant after accessing:")
	for _, path := range targetPaths {
		parts = append(parts, "- "+path)
	}

	remaining := maxTotalBytes
	for _, file := range files {
		if remaining <= 0 {
			break
		}
		content := strings.TrimSpace(file.Content)
		if content == "" {
			continue
		}
		limit := min(maxFileBytes, remaining)
		if len(content) > limit {
			content = truncateWithNotice(content, limit, file.Name)
		}
		if len(content) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("# Path Context: %s\n%s", file.Name, content))
		remaining -= len(content)
	}

	if len(parts) == 1 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func (s *CodingSession) pruneTransientContextMessages() {
	state := s.agent.GetState()
	if len(state.Messages) == 0 {
		return
	}

	// Scan first to avoid allocation when there is nothing to prune.
	hasTransient := false
	for _, msg := range state.Messages {
		if isTransientContextMessage(msg) {
			hasTransient = true
			break
		}
	}
	if !hasTransient {
		return
	}

	filtered := make([]agent.AgentMessage, 0, len(state.Messages))
	for _, msg := range state.Messages {
		if !isTransientContextMessage(msg) {
			filtered = append(filtered, msg)
		}
	}
	s.agent.ReplaceMessages(filtered)
}

// SetModel changes the active model.
func (s *CodingSession) SetModel(model *types.Model) {
	changed := s.model == nil || s.model.ProviderID != model.ProviderID || s.model.ID != model.ID
	s.model = model
	s.agent.SetModel(model)
	if s.promptBuilder != nil {
		s.promptBuilder.SetModel(model)
	}
	if changed {
		_ = s.ClearConversation()
	}
	s.refreshDynamicSystemPrompt()

	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeModelChange, "", session.ModelChangeData{
		Provider: model.ProviderID,
		ModelID:  model.ID,
	}))
	s.writeRuntimeState()
	s.emitSessionEvent(SessionEvent{
		Type:     SessionEventModelChange,
		Provider: model.ProviderID,
		ModelID:  model.ID,
	})
}

// SetModelByID changes the active model by provider and model ID.
func (s *CodingSession) SetModelByID(provider, modelID string) error {
	llmModel := providers.GetModel(provider, modelID)
	if llmModel == nil {
		return fmt.Errorf("model not found: %s/%s", provider, modelID)
	}
	s.SetModel(llmModel)
	return nil
}

// SetModelByName changes the active model by configured display name or model ID.
func (s *CodingSession) SetModelByName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("model name is required")
	}
	var matches []*types.Model
	for _, model := range s.GetAvailableModels() {
		if model.Name == name || model.ID == name || model.ProviderID+"/"+model.ID == name || model.ProviderID+":"+model.ID == name {
			matches = append(matches, model)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("model not found: %s", name)
	}
	if len(matches) > 1 {
		return fmt.Errorf("model %q is ambiguous; use /model <provider> <modelId>", name)
	}
	s.SetModel(matches[0])
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

	if preErr := s.runHarnessPreCompact(len(state.Messages)); preErr != nil {
		return fmt.Errorf("compaction blocked by harness: %w", preErr)
	}
	result, err := compaction.Compact(ctx, state.Messages, compaction.Options{
		PreserveRecent: s.config.CompactionSettings.PreserveRecentMessages,
		Model:          s.model,
		GetAPIKey:      s.getAPIKey,
		StreamFn:       s.streamFn,
	})
	s.runHarnessPostCompact(result, err)
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

	s.emitSessionEvent(SessionEvent{Type: SessionEventCompactionDone})

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
		s.emitSessionEvent(SessionEvent{Type: SessionEventCompactionStart})
		_ = s.Compact(ctx)
	}
}

// Fork creates a new branch from the given entry ID.
func (s *CodingSession) Fork(entryID string) error {
	return s.sessionManager.Fork(entryID)
}

// GetSessionLeafID returns the current persisted session leaf entry ID.
func (s *CodingSession) GetSessionLeafID() string {
	if s.sessionManager == nil {
		return ""
	}
	return s.sessionManager.LastID()
}

// GetSessionBranches returns branch points in the current session tree.
func (s *CodingSession) GetSessionBranches() []SessionBranchInfo {
	if s.sessionTree == nil {
		return nil
	}
	branches := s.sessionTree.GetBranches()
	out := make([]SessionBranchInfo, 0, len(branches))
	for _, branch := range branches {
		out = append(out, SessionBranchInfo{
			ID:         branch.ID,
			ParentID:   branch.ParentID,
			Label:      branch.Label,
			EntryCount: len(branch.Entries),
		})
	}
	return out
}

// GetSessionTreeNodes returns a depth-first view of the current session tree.
func (s *CodingSession) GetSessionTreeNodes() []SessionTreeNode {
	if s.sessionManager == nil || s.sessionTree == nil {
		return nil
	}
	entries := s.sessionManager.Load()
	lookup := make(map[string]session.SessionEntry, len(entries))
	visible := make(map[string]struct{})
	for _, entry := range entries {
		lookup[entry.ID] = entry
		if visibleSessionTreeEntry(entry) {
			visible[entry.ID] = struct{}{}
		}
	}
	children := make(map[string][]session.SessionEntry)
	for _, entry := range entries {
		if !visibleSessionTreeEntry(entry) {
			continue
		}
		parentID := nearestVisibleSessionParent(entry.ParentID, lookup, visible)
		children[parentID] = append(children[parentID], entry)
	}
	currentPath := make(map[string]struct{})
	for _, entry := range s.sessionTree.GetCurrentPath() {
		currentPath[entry.ID] = struct{}{}
	}
	currentID := s.sessionManager.LastID()
	var out []SessionTreeNode
	var walk func(parentID string, depth int)
	walk = func(parentID string, depth int) {
		for _, entry := range children[parentID] {
			_, inPath := currentPath[entry.ID]
			node := SessionTreeNode{
				ID:            entry.ID,
				ParentID:      nearestVisibleSessionParent(entry.ParentID, lookup, visible),
				Type:          string(entry.Type),
				Role:          sessionEntryRole(entry),
				Label:         s.sessionManager.GetLabel(entry.ID),
				Preview:       sessionEntryPreview(entry),
				Depth:         depth,
				ChildCount:    len(children[entry.ID]),
				Current:       entry.ID == currentID,
				InCurrentPath: inPath,
				Timestamp:     entry.Timestamp,
			}
			out = append(out, node)
			walk(entry.ID, depth+1)
		}
	}
	walk("", 0)
	return out
}

func nearestVisibleSessionParent(parentID string, lookup map[string]session.SessionEntry, visible map[string]struct{}) string {
	for parentID != "" {
		if _, ok := visible[parentID]; ok {
			return parentID
		}
		parent, ok := lookup[parentID]
		if !ok {
			return ""
		}
		parentID = parent.ParentID
	}
	return ""
}

func visibleSessionTreeEntry(entry session.SessionEntry) bool {
	switch entry.Type {
	case session.EntryTypeMessage, session.EntryTypeBranchSummary, session.EntryTypeCompaction, session.EntryTypeModelChange:
		return true
	default:
		return false
	}
}

func sessionEntryRole(entry session.SessionEntry) string {
	if entry.Type != session.EntryTypeMessage {
		return ""
	}
	switch data := entry.Data.(type) {
	case session.MessageData:
		return string(data.Role)
	case map[string]any:
		role, _ := data["role"].(string)
		return role
	default:
		return ""
	}
}

func sessionEntryPreview(entry session.SessionEntry) string {
	switch data := entry.Data.(type) {
	case session.MessageData:
		return previewAnyContent(data.Content)
	case session.BranchSummaryData:
		return data.Summary
	case session.CompactionData:
		return data.Summary
	case session.ModelChangeData:
		if data.Provider != "" {
			return data.Provider + "/" + data.ModelID
		}
		return data.ModelID
	case map[string]any:
		if summary, _ := data["summary"].(string); summary != "" {
			return summary
		}
		if content, ok := data["content"]; ok {
			return previewAnyContent(content)
		}
		if model, _ := data["modelId"].(string); model != "" {
			provider, _ := data["provider"].(string)
			if provider != "" {
				return provider + "/" + model
			}
			return model
		}
	}
	return ""
}

func previewAnyContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []types.ContentBlock:
		var parts []string
		for _, block := range value {
			if text, ok := block.(*types.TextContent); ok && text != nil && text.Text != "" {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, " ")
	case []any:
		var parts []string
		for _, block := range value {
			if m, ok := block.(map[string]any); ok {
				text, _ := m["text"].(string)
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(value)
	}
}

// CreateBranchedSession creates a new session file from the path to entryID.
func (s *CodingSession) CreateBranchedSession(entryID string) (string, error) {
	if s.sessionManager == nil {
		return "", fmt.Errorf("session manager not available")
	}
	path, err := s.sessionManager.CreateBranchedSession(entryID)
	if err != nil {
		return "", err
	}
	s.sessionTree = session.NewTree(s.sessionManager)
	_, _ = s.RestoreMessages()
	s.writeRuntimeState()
	return path, nil
}

// NavigateTree navigates to a specific point in the session tree.
func (s *CodingSession) NavigateTree(entryID string) error {
	if s.sessionManager == nil || s.sessionTree == nil {
		return fmt.Errorf("session tree not available")
	}
	path := s.sessionTree.GetPath(entryID)
	if len(path) == 0 {
		return fmt.Errorf("entry %s not found", entryID)
	}
	if entryID == s.sessionManager.LastID() {
		_, err := s.RestoreMessages()
		return err
	}
	var msgs []types.AgentMessage
	for _, entry := range path {
		if entry.Type != session.EntryTypeMessage {
			continue
		}
		if msg, ok := agentMessageFromSessionData(entry.Data); ok && msg != nil {
			msgs = append(msgs, msg)
		}
	}
	summary, err := compaction.GenerateBranchSummary(context.Background(), msgs, compaction.BranchSummaryOptions{})
	if err != nil {
		return err
	}
	if _, err := s.sessionManager.BranchWithSummary(entryID, summary); err != nil {
		return err
	}
	s.sessionTree = session.NewTree(s.sessionManager)
	_, err = s.RestoreMessages()
	s.writeRuntimeState()
	return err
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

func (s *CodingSession) EffectiveConfigJSON() string {
	if s.config == nil {
		return "{}\n"
	}
	payload := map[string]any{
		"config": s.config,
		"paths": map[string]string{
			"global":  filepath.Join(s.agentDir, "settings.json"),
			"project": filepath.Join(s.cwd, ".coding_agent", "settings.json"),
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(data) + "\n"
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
	llmModel := providers.GetModel("", nextID)
	var model *types.Model
	if llmModel != nil {
		model = llmModel
	} else {
		model = &types.Model{ID: nextID, Name: nextID}
	}

	s.SetModel(model)
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
	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeThinkingChange, "", session.ThinkingChangeData{
		Level: level,
	}))
	s.emitSessionEvent(SessionEvent{
		Type:  SessionEventThinkingChange,
		Level: string(level),
	})
	s.writeRuntimeState()
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
	if s.sessionManager != nil {
		return s.sessionManager.SessionID()
	}
	return s.agent.GetSessionID()
}

// SetAutoCompaction enables or disables auto-compaction.
func (s *CodingSession) SetAutoCompaction(enabled bool) {
	s.config.AutoCompaction = enabled
	s.writeRuntimeState()
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

func (s *CodingSession) TraceSummary() sessiontrace.Summary {
	if s.traceRecorder == nil {
		return sessiontrace.Summary{}
	}
	return s.traceRecorder.Summary()
}

func (s *CodingSession) emitSessionEvent(event SessionEvent) {
	if s.traceRecorder != nil {
		_ = s.traceRecorder.RecordSessionEvent(string(event.Type), sessionEventMeta(event))
	}
	if s.otelBridge != nil {
		s.otelBridge.RecordSessionEvent(string(event.Type), sessionEventMeta(event))
	}
	s.eventBus.Emit(sessionEventChannel, event)
}

func (s *CodingSession) otelOptions(opts CodingSessionOptions) (sessiontrace.OTelOptions, bool) {
	if opts.OTelTracerProvider == nil && !s.config.TracingOTelEnabled() {
		return sessiontrace.OTelOptions{}, false
	}
	serviceName := strings.TrimSpace(s.config.Tracing.OTel.ServiceName)
	if serviceName == "" {
		serviceName = "modu-coding-agent"
	}
	return sessiontrace.OTelOptions{
		Provider:       opts.OTelTracerProvider,
		Exporter:       s.config.Tracing.OTel.Exporter,
		Endpoint:       s.config.Tracing.OTel.Endpoint,
		Headers:        utils.CopyMap(s.config.Tracing.OTel.Headers),
		Insecure:       s.config.TracingOTelInsecure(),
		ServiceName:    serviceName,
		ServiceVersion: strings.TrimSpace(s.config.Tracing.OTel.ServiceVersion),
		InstanceID:     strings.TrimSpace(s.config.Tracing.OTel.InstanceID),
		SamplingRatio:  s.config.Tracing.OTel.SamplingRatio,
		SessionID:      s.GetSessionID(),
		Cwd:            s.cwd,
		ModelProvider:  opts.Model.ProviderID,
		ModelID:        opts.Model.ID,
	}, true
}

func sessionEventMeta(event SessionEvent) map[string]any {
	meta := map[string]any{}
	if event.Attempt != 0 {
		meta["attempt"] = event.Attempt
	}
	if event.MaxAttempts != 0 {
		meta["maxAttempts"] = event.MaxAttempts
	}
	if event.DelayMs != 0 {
		meta["delayMs"] = event.DelayMs
	}
	if event.ErrorMessage != "" {
		meta["errorMessage"] = event.ErrorMessage
	}
	if event.Success != nil {
		meta["success"] = *event.Success
	}
	if event.Provider != "" {
		meta["provider"] = event.Provider
	}
	if event.ModelID != "" {
		meta["modelId"] = event.ModelID
	}
	if event.Level != "" {
		meta["level"] = event.Level
	}
	if event.OldCwd != "" {
		meta["oldCwd"] = event.OldCwd
	}
	if event.NewCwd != "" {
		meta["newCwd"] = event.NewCwd
		meta["cwd"] = event.NewCwd
	}
	if event.Path != "" {
		meta["path"] = event.Path
	}
	if event.SubagentName != "" {
		meta["subagentName"] = event.SubagentName
	}
	if event.SubagentTask != "" {
		meta["subagentTask"] = event.SubagentTask
	}
	if event.SubagentBackground {
		meta["subagentBackground"] = true
	}
	if event.SubagentResult != "" {
		meta["subagentResult"] = event.SubagentResult
	}
	if event.ToolName != "" {
		meta["toolName"] = event.ToolName
	}
	if event.Reason != "" {
		meta["reason"] = event.Reason
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

// GetSessionFile returns the session file path.
func (s *CodingSession) GetSessionFile() string {
	return s.sessionManager.FilePath()
}

// ListSessions returns persisted sessions for the current working directory.
func (s *CodingSession) ListSessions() ([]session.SessionInfo, error) {
	return session.List(s.agentDir, s.cwd)
}

func (s *CodingSession) ListSessionInfos() ([]SessionInfo, error) {
	return s.ListSessions()
}

// ListAllSessions returns persisted sessions across all working directories.
func (s *CodingSession) ListAllSessions() ([]session.SessionInfo, error) {
	return session.ListAll(s.agentDir)
}

func (s *CodingSession) ListAllSessionInfos() ([]SessionInfo, error) {
	return s.ListAllSessions()
}

// ForkFromSession creates and switches to a new session copied from sessionFile.
func (s *CodingSession) ForkFromSession(sessionFile string) error {
	mgr, err := session.ForkFrom(s.agentDir, sessionFile, s.cwd)
	if err != nil {
		return err
	}
	return s.switchSessionManager(mgr)
}

// DeleteSession removes a saved session file, except the active session.
func (s *CodingSession) DeleteSession(sessionFile string) error {
	current, err := filepath.Abs(s.GetSessionFile())
	if err != nil {
		return err
	}
	target, err := filepath.Abs(sessionFile)
	if err != nil {
		return err
	}
	if current == target {
		return fmt.Errorf("refusing to delete the active session")
	}
	return session.Delete(s.agentDir, sessionFile)
}

// SetSessionName sets the display name for this session.
func (s *CodingSession) SetSessionName(name string) {
	s.sessionName = name
	if s.sessionManager != nil {
		_ = s.sessionManager.AppendSessionInfo(name)
	}
}

// GetSessionName returns the display name for this session.
func (s *CodingSession) GetSessionName() string {
	if s.sessionManager != nil {
		return s.sessionManager.SessionName()
	}
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
	entries := s.sessionTree.GetCurrentPath()
	var result []ForkMessage
	for _, entry := range entries {
		if entry.Type != session.EntryTypeMessage {
			continue
		}
		data, ok := entry.Data.(session.MessageData)
		if !ok {
			// Try map-based extraction (from JSON deserialization)
			if m, ok := entry.Data.(map[string]any); ok {
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
	if len(s.scopedModels) > 0 {
		result := make([]*types.Model, 0, len(s.scopedModels))
		for _, id := range s.scopedModels {
			if model := providers.GetModel("", id); model != nil {
				result = append(result, model)
			}
		}
		return result
	}
	var result []*types.Model
	for _, p := range providers.GetAllProviders() {
		result = append(result, providers.GetModels(p)...)
	}
	return result
}

// GetAllAvailableModels returns all registered models, ignoring session scope.
func (s *CodingSession) GetAllAvailableModels() []*types.Model {
	var result []*types.Model
	for _, p := range providers.GetAllProviders() {
		result = append(result, providers.GetModels(p)...)
	}
	return result
}

// GetScopedModelIDs returns the session-local model scope used for cycling.
func (s *CodingSession) GetScopedModelIDs() []string {
	return append([]string(nil), s.scopedModels...)
}

// SetScopedModelIDs updates the session-local model scope used for cycling.
func (s *CodingSession) SetScopedModelIDs(ids []string) {
	s.scopedModels = resolveScopedModels(nil, ids)
	s.writeRuntimeState()
}

// ReloadResources reloads dynamic resources and refreshes the prompt.
func (s *CodingSession) ReloadResources() {
	s.refreshResourcePaths()
	s.refreshDynamicSystemPrompt()
	s.writeRuntimeState()
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
	return s.switchSessionManager(newMgr)
}

func (s *CodingSession) switchSessionManager(newMgr *session.Manager) error {
	var messages []agent.AgentMessage
	newTree := session.NewTree(newMgr)
	for _, entry := range newTree.GetCurrentPath() {
		if entry.Type != session.EntryTypeMessage {
			continue
		}
		if msg, ok := agentMessageFromSessionData(entry.Data); ok && msg != nil {
			messages = append(messages, msg)
		}
	}

	s.sessionManager = newMgr
	s.sessionTree = newTree
	s.sessionName = newMgr.SessionName()
	s.agent.ReplaceMessages(messages)
	s.lastSavedIndex = len(messages)
	s.writeRuntimeState()
	return nil
}

// formatSubagentsForPrompt returns an XML block listing available subagents,
// suitable for injection into the system prompt.
func formatSubagentsForPrompt(defs []*subagent.SubagentDefinition) string {
	if len(defs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\nThe following subagents are available via the spawn_subagent tool.\n")
	sb.WriteString("Use spawn_subagent when a task is well-scoped and can be delegated to a specialist.\n\n")
	sb.WriteString("<available_subagents>\n")
	for _, def := range defs {
		sb.WriteString("  <subagent>\n")
		sb.WriteString("    <name>" + def.Name + "</name>\n")
		sb.WriteString("    <description>" + def.Description + "</description>\n")
		sb.WriteString("  </subagent>\n")
	}
	sb.WriteString("</available_subagents>")
	return sb.String()
}

func prepareSubagentDefinition(def *subagent.SubagentDefinition, skillMgr *skills.Manager, memoryStore *MemoryStore) *subagent.SubagentDefinition {
	if def == nil {
		return nil
	}

	clone := *def
	clone.DisallowedTools = append([]string{}, clone.DisallowedTools...)
	clone.DisallowedTools = append(clone.DisallowedTools, clone.HarnessBlockTools...)
	var parts []string
	if clone.SystemPrompt != "" {
		parts = append(parts, clone.SystemPrompt)
	}

	if skillMgr != nil && len(clone.Skills) > 0 {
		var skillBlocks []string
		for _, name := range clone.Skills {
			skill, ok := skillMgr.Get(strings.TrimSpace(name))
			if !ok {
				continue
			}
			skillBlocks = append(skillBlocks, fmt.Sprintf("## Skill: %s\n\n%s", skill.Name, skill.Content))
		}
		if len(skillBlocks) > 0 {
			parts = append(parts, strings.Join(skillBlocks, "\n\n---\n\n"))
		}
	}

	if memoryStore != nil {
		if mem := memoryContextForScope(memoryStore, clone.MemoryScope); mem != "" {
			parts = append(parts, mem)
		}
	}

	clone.SystemPrompt = strings.Join(parts, "\n\n---\n\n")
	return &clone
}

func memoryContextForScope(store *MemoryStore, scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "none":
		return ""
	case "user", "global":
		if v := strings.TrimSpace(store.ReadGlobalLongTerm()); v != "" {
			return "## Global Memory\n\n" + v
		}
	case "project", "local":
		if v := strings.TrimSpace(store.ReadProjectLongTerm()); v != "" {
			return "## Project Memory\n\n" + v
		}
	case "both", "all":
		return store.GetMemoryContext()
	}
	return ""
}

func (s *CodingSession) executeSkill(ctx context.Context, originalInput string, skill *skills.Skill, args string) error {
	task := strings.TrimSpace(args)
	if task == "" {
		task = "Use this skill for the user's request."
	}

	if err := s.runHarnessUserPromptSubmit(originalInput); err != nil {
		return err
	}

	s.refreshDynamicSystemPrompt()

	messages := []agent.AgentMessage{
		(&CustomMessage{
			Source: explicitSkillSource,
			Text:   s.skillPrompt(skill),
		}).ToLlmMessage(),
		types.UserMessage{
			Role:      "user",
			Content:   task,
			Timestamp: time.Now().UnixMilli(),
		},
	}

	err := s.agent.Prompt(ctx, messages)
	if err != nil {
		return err
	}
	s.maybeAutoCompact(ctx)
	return nil
}

func (s *CodingSession) skillPrompt(skill *skills.Skill) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("The user explicitly invoked the %q skill. Use the instructions below for this turn.", skill.Name))
	parts = append(parts, skill.Content)
	parts = append(parts, fmt.Sprintf("# Environment\n- Working directory: %s\n- All file and shell tools are already bound to this working directory.", s.cwd))
	return strings.Join(parts, "\n\n---\n\n")
}

func (s *CodingSession) handleMessageEnd(msg agent.AgentMessage) {
	if msg == nil {
		return
	}
	if isTransientContextMessage(msg) {
		return
	}

	role, content, ok := sessionMessageData(msg)
	if !ok {
		return
	}
	if role == agent.RoleUser {
		if !s.currentLeafMessageMatches(role, content) {
			_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeMessage, "", session.MessageData{
				Role:    role,
				Content: content,
			}))
		}
		_ = s.SaveMessages()
		return
	}

	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeMessage, "", session.MessageData{
		Role:    role,
		Content: content,
	}))
	_ = s.SaveMessages()
}

func (s *CodingSession) currentLeafMessageMatches(role agent.MessageRole, content any) bool {
	if s.sessionManager == nil {
		return false
	}
	leafID := s.sessionManager.LastID()
	if leafID == "" {
		return false
	}
	entry, ok := s.sessionManager.GetEntry(leafID)
	if !ok || entry.Type != session.EntryTypeMessage {
		return false
	}
	data, ok := entry.Data.(session.MessageData)
	if !ok {
		return false
	}
	return data.Role == role && reflect.DeepEqual(data.Content, content)
}

func sessionMessageData(msg agent.AgentMessage) (agent.MessageRole, any, bool) {
	switch m := msg.(type) {
	case types.UserMessage:
		return agent.RoleUser, m.Content, true
	case *types.UserMessage:
		return agent.RoleUser, m.Content, true
	case types.AssistantMessage:
		return agent.RoleAssistant, m, true
	case *types.AssistantMessage:
		return agent.RoleAssistant, *m, true
	case types.ToolResultMessage:
		return agent.RoleToolResult, m, true
	case *types.ToolResultMessage:
		return agent.RoleToolResult, *m, true
	default:
		return "", nil, false
	}
}
