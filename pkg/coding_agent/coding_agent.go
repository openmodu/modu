package coding_agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/apikeys"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/config"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/eventbus"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/runtimepaths"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/prompts"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/subagent"
	"github.com/openmodu/modu/pkg/coding_agent/services/approval"
	"github.com/openmodu/modu/pkg/coding_agent/services/bash"
	"github.com/openmodu/modu/pkg/coding_agent/services/bgtask"
	"github.com/openmodu/modu/pkg/coding_agent/services/contextmgr"
	"github.com/openmodu/modu/pkg/coding_agent/services/memory"
	"github.com/openmodu/modu/pkg/coding_agent/services/plan"
	"github.com/openmodu/modu/pkg/coding_agent/services/retry"
	"github.com/openmodu/modu/pkg/coding_agent/services/session"
	"github.com/openmodu/modu/pkg/coding_agent/services/systemprompt"
	"github.com/openmodu/modu/pkg/coding_agent/services/todo"
	"github.com/openmodu/modu/pkg/coding_agent/services/worktree"
	"github.com/openmodu/modu/pkg/coding_agent/tools"
	toolcommon "github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/skills"
	"github.com/openmodu/modu/pkg/types"
)

// CodingSessionOptions configures a new CodingSession.
type CodingSessionOptions struct {
	// Cwd is the working directory.
	Cwd string
	// AgentDir is the configuration directory (default: ~/.modu/).
	AgentDir string
	// Model is the LLM model to use.
	Model *types.Model
	// ThinkingLevel controls reasoning depth.
	ThinkingLevel types.ThinkingLevel
	// Tools are the tools to make available. If nil, defaults to CodingTools.
	Tools []types.Tool
	// CustomTools are additional tools provided by the caller.
	CustomTools []types.Tool
	// ToolProvider constructs and rebinds session tools. If nil, the default
	// coding tool provider is used.
	ToolProvider types.ToolManager
	// Extensions are extensions to initialize.
	Extensions []extension.Extension
	// CustomSystemPrompt overrides the default system prompt.
	CustomSystemPrompt string
	// GetAPIKey retrieves an API key for a provider.
	GetAPIKey func(provider string) (string, error)
	// StreamFn overrides the default stream function.
	StreamFn types.StreamFn
	// ExtraSubagentDirs adds extra directories to scan for subagent definitions.
	ExtraSubagentDirs []string
	// ScopedModels limits model listing/cycling to these model IDs.
	ScopedModels []string
	// ModelConfigPath records the model config file path for diagnostics.
	ModelConfigPath string
	// ResumeSessionID resumes a persisted session by full id or unique prefix.
	// When empty, NewCodingSession starts a fresh session.
	ResumeSessionID string
	// DeferStartupEvent suppresses the "startup" session_start event during
	// construction so the host can finish wiring (subscriptions, background
	// prompt driver) before extensions react to it — notably an active goal
	// auto-continuing. The host must call EmitStartupEvent once it is ready.
	DeferStartupEvent bool
}

// engine is the L1 kernel: it owns all session state, runs agent turns, wires
// the L2 services, and exposes the kernel capability surface they depend on.
// The host-facing API (L5) lives on CodingSession, which embeds *engine.
type engine struct {
	// self points back to the host façade, for the few callbacks whose
	// signature requires a *CodingSession (e.g. slash-command handlers).
	self *CodingSession

	agent           *agent.Agent
	sessionManager  *session.Manager
	sessionTree     *session.Tree
	config          *config.Config
	extensions      *extension.Runner
	skillManager    *skills.Manager
	promptManager   *prompts.Manager
	resources       *resource.Loader
	memoryStore     *memory.Store
	subagentLoader  *subagent.Loader
	cwd             string
	agentDir        string
	promptBuilder   *systemprompt.Builder
	model           *types.Model
	activeTools     []types.Tool
	toolProvider    types.ToolManager
	artifactStore   *toolcommon.ArtifactStore
	slashCommands   map[string]SlashCommand
	getAPIKey       func(provider string) (string, error)
	streamFn        types.StreamFn
	lastSavedIndex  int
	retryManager    *retry.Manager
	eventBus        eventbus.EventBusController
	scopedModels    []string
	modelConfigPath string
	thinkingLevel   types.ThinkingLevel
	ultracode       bool
	sessionName     string
	sessionStarted  int64

	// Session components — each owns its own state behind a narrow API.
	ctxMgr           *contextmgr.Manager // conversation window: tokens, compaction, nested context
	contextRemaining *contextRemainingProxy
	bash             *bash.Runner         // inline !command execution + cancellation
	todos            *todo.Store          // session todo list
	taskManager      *bgtask.Manager      // background async tasks
	plan             *plan.Controller     // plan mode
	worktree         *worktree.Controller // isolated git worktree
	extPrompts       extensionPrompts     // host confirm/select callbacks

	// bgPromptDriver lets a host (e.g. the TUI) take over hidden extension
	// prompts (goal continuations) that would otherwise run in a detached
	// background goroutine. When it returns true the host now drives the turn,
	// so its UI shows the agent as running and ESC can interrupt it; a nil
	// driver or a false return falls back to the background goroutine used by
	// headless hosts.
	bgPromptDriver   func(run func(context.Context) error) bool
	bgPromptDriverMu sync.RWMutex

	// startupEventDeferred records that the constructor skipped the startup
	// session_start event (DeferStartupEvent); EmitStartupEvent emits it once.
	startupEventDeferred bool
	startupEventOnce     sync.Once

	// approvalManager handles tool execution approval.
	approvalManager *approval.Manager

	// gitCache holds the last-known git state to avoid spawning git subprocesses
	// on every writeRuntimeState call.
	gitCache cachedGitState
}

// CodingSession is the L5 host façade: it embeds the kernel engine (promoting
// all kernel methods) and adds the host-facing API (model/session/config
// management, introspection, export). External drivers (rpc, sdk, tui) use it.
type CodingSession struct {
	*engine
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
	cfg, err := config.Load(agentDir, opts.Cwd)
	if err != nil {
		cfg = config.Default()
	}

	// Override config with options
	if opts.ThinkingLevel != "" {
		cfg.ThinkingLevel = opts.ThinkingLevel
	}

	// Initialize memory store (global ~/.modu/memory + project <cwd>/memory)
	memoryStore := memory.New(agentDir, opts.Cwd)
	contextRemaining := &contextRemainingProxy{}

	// Create session manager before tools so tool artifacts can be grouped by
	// the active session id under the canonical project tool-results tree.
	sessionMgr, err := newSessionManager(agentDir, opts.Cwd, opts.ResumeSessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to create session manager: %w", err)
	}
	artifactStore := toolcommon.NewArtifactStore(
		runtimepaths.SessionToolResultsDir(agentDir, opts.Cwd, sessionMgr.SessionID()),
	)

	// Set up tools
	toolProvider := opts.ToolProvider
	if toolProvider == nil {
		toolProvider = tools.NewProvider(tools.ToolSetCoding)
	}
	activeTools := toolProvider.Tools(types.ToolContext{
		Cwd:        opts.Cwd,
		BaseTools:  opts.Tools,
		ExtraTools: opts.CustomTools,
		Features: map[string]bool{
			tools.FeatureMemory:       cfg.FeatureMemoryTool(),
			tools.FeatureTodo:         cfg.FeatureTodoTool(),
			tools.FeatureTaskOutput:   cfg.FeatureTaskOutputTool(),
			tools.FeaturePlanMode:     cfg.FeaturePlanMode(),
			tools.FeatureWorktreeMode: cfg.FeatureWorktreeMode(),
		},
		Values: map[string]any{
			tools.ValueMemoryStore: memoryStore,
			tools.ValueTodoStore:   todoStoreAdapter{session: nil},
			tools.ValuePlanMode:    plan.New(nil),
			tools.ValueWorktree:    worktree.New(nil),
			tools.ValueContext:     contextRemaining,
			tools.ValueArtifacts:   artifactStore,
		},
	})

	// Create extension runner
	extRunner := extension.NewRunner()

	resourceSnapshot := loader.LoadResources()

	// Initialize skills and prompt templates.
	skillMgr := skills.NewManager(agentDir, opts.Cwd)
	skillMgr.SetExtraPaths(skillPathRefs(resourceSnapshot.SkillPaths))
	_ = skillMgr.Discover()
	promptMgr := prompts.NewManager(agentDir, opts.Cwd)
	promptMgr.SetExtraPaths(resourceSnapshot.PromptPaths)
	_ = promptMgr.Discover()

	// Build system prompt
	promptBuilder := systemprompt.NewBuilder(opts.Cwd)
	promptBuilder.SetModel(opts.Model)
	if memoryFeatureEnabled(cfg) {
		promptBuilder.SetMemoryProvider(memoryStore)
	}
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
		keyStore := apikeys.New(agentDir)
		_ = keyStore.Load()
		getAPIKey = func(provider string) (string, error) {
			key, ok := keyStore.Get(provider)
			if ok {
				return key, nil
			}
			return "", fmt.Errorf("no API key found for provider: %s", provider)
		}
	}

	// Discover subagent profiles. The legacy spawn_subagent tool moved
	// to the subagent extension (pkg/coding_agent/extension/subagent);
	// we still load this loader because GetSubagents() exposes its list
	// to host UIs that haven't migrated to ExtensionRuntimeStates.
	subagentLoader := subagent.NewLoader()
	subagentLoader.Discover(agentDir, opts.Cwd)
	subagentLoader.DiscoverExtra(opts.ExtraSubagentDirs...)
	taskMgr := bgtask.New()
	systemPrompt := promptBuilder.Build()

	// Create approval manager
	approvalMgr := approval.New()
	approvalMgr.SetRules(cfg.Permissions)

	// Create the underlying agent
	ag := agent.NewAgent(types.Config{
		GetAPIKey:   getAPIKey,
		ApproveTool: approvalMgr.Approve,
		InitialState: &types.State{
			SystemPrompt:  systemPrompt,
			Model:         opts.Model,
			ThinkingLevel: cfg.ThinkingLevel,
			Tools:         activeTools,
		},
		StreamFn: streamFn,
	})

	cs := &engine{
		agent:            ag,
		sessionManager:   sessionMgr,
		sessionTree:      session.NewTree(sessionMgr),
		config:           cfg,
		extensions:       extRunner,
		skillManager:     skillMgr,
		promptManager:    promptMgr,
		resources:        loader,
		memoryStore:      memoryStore,
		subagentLoader:   subagentLoader,
		cwd:              opts.Cwd,
		agentDir:         agentDir,
		promptBuilder:    promptBuilder,
		model:            opts.Model,
		activeTools:      activeTools,
		toolProvider:     toolProvider,
		artifactStore:    artifactStore,
		slashCommands:    make(map[string]SlashCommand),
		getAPIKey:        getAPIKey,
		streamFn:         streamFn,
		retryManager:     retry.New(cfg.RetrySettings, cfg.AutoRetry),
		eventBus:         eventbus.NewEventBus(),
		scopedModels:     resolveScopedModels(cfg.ScopedModels, opts.ScopedModels),
		modelConfigPath:  opts.ModelConfigPath,
		thinkingLevel:    cfg.ThinkingLevel,
		sessionStarted:   time.Now().UnixMilli(),
		taskManager:      taskMgr,
		approvalManager:  approvalMgr,
		contextRemaining: contextRemaining,
	}
	cs.wireComponents()
	if err := taskMgr.SetStorePath(cs.RuntimePaths().BackgroundTasksFile); err != nil {
		return nil, fmt.Errorf("failed to load background tasks: %w", err)
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
	initialContexts := make([]string, 0, len(resourceSnapshot.ContextFiles))
	for _, ctxFile := range resourceSnapshot.ContextFiles {
		initialContexts = append(initialContexts, ctxFile.Path)
	}
	cs.ctxMgr.MarkInitialContext(initialContexts)
	if strings.TrimSpace(opts.ResumeSessionID) != "" {
		if _, err := cs.RestoreMessages(); err != nil {
			return nil, fmt.Errorf("failed to restore session messages: %w", err)
		}
	}

	// Subscribe to events for token usage tracking (auto-compaction)
	ag.Subscribe(func(event types.Event) {
		if cs.extensions != nil {
			cs.extensions.EmitEvent(event)
		}
		if event.Type == types.EventTypeMessageEnd {
			recordUsage := func(u types.AgentUsage) {
				t := u.TotalTokens
				if t == 0 {
					t = u.Input + u.Output
				}
				if t > 0 {
					cs.ctxMgr.RecordUsageSnapshot(t)
				}
			}
			if msg, ok := event.Message.(types.AssistantMessage); ok {
				recordUsage(msg.Usage)
			} else if msg, ok := event.Message.(*types.AssistantMessage); ok {
				recordUsage(msg.Usage)
			}
			cs.handleMessageEnd(event.Message)
			return
		}
		if event.Type == types.EventTypeToolExecutionEnd && !event.IsError {
			cs.ctxMgr.OnToolExecutionEnd(event)
			return
		}
		if event.Type == types.EventTypeAgentEnd {
			cs.ctxMgr.PruneTransient()
			go cs.refreshGitRuntimeState()
			cs.writeRuntimeState()
		}
	})

	// Register built-in slash commands
	for _, cmd := range BuiltinCommands() {
		cs.slashCommands[cmd.Name] = cmd
	}

	if err := applySettingsExtensionConfig(opts.Extensions, cfg); err != nil {
		return nil, err
	}

	// Initialize extensions
	if len(opts.Extensions) > 0 {
		sendExtensionMessage := func(text string, options extension.MessageOptions) error {
			followUp := options.DeliverAs == "followUp"
			source := "extension"
			if followUp {
				source = hiddenExtensionSource
			}
			msg := &CustomMessage{
				Source:     source,
				Text:       text,
				CustomType: options.CustomType,
				Display:    options.Display,
				DeliverAs:  options.DeliverAs,
			}
			llmMsg := msg.ToLlmMessage()
			if ag.GetState().IsStreaming {
				if followUp {
					ag.FollowUp(llmMsg)
				} else {
					ag.Steer(llmMsg)
				}
				return nil
			}
			runTurn := func(ctx context.Context) error {
				deadline := time.Now().Add(time.Second)
				for {
					err := ag.Prompt(ctx, llmMsg)
					if err == nil {
						return nil
					}
					if !strings.Contains(err.Error(), "already processing") || time.Now().After(deadline) {
						return err
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			// Prefer a host driver (the TUI) so the injected turn runs in the
			// foreground loop — status shows running and ESC can interrupt it.
			// Headless hosts register no driver and fall back to a detached run.
			if driver := cs.backgroundPromptDriver(); driver != nil && driver(runTurn) {
				return nil
			}
			go func() { _ = runTurn(context.Background()) }()
			// Wait briefly for the new turn to enter streaming so a caller's
			// WaitForIdle doesn't race past it.
			deadline := time.Now().Add(200 * time.Millisecond)
			for time.Now().Before(deadline) {
				if ag.GetState().IsStreaming {
					break
				}
				time.Sleep(time.Millisecond)
			}
			return nil
		}
		extRunner.SetCallbacks(
			func(text string, options extension.MessageOptions) error {
				return sendExtensionMessage(text, options)
			},
			func(names []string) {
				cs.SetActiveTools(names)
			},
			func(provider, modelID string) error {
				return cs.SetModelByID(provider, modelID)
			},
			func() string { return cs.GetSessionID() },
			func() string { return cs.sessionManager.Dir() },
			func() string { return cs.agentDir },
			func() string { return cs.cwd },
			func() bool { return !ag.GetState().IsStreaming },
			func() bool { return ag.HasQueuedMessages() },
			func() string {
				if cs.config == nil {
					return ""
				}
				return cs.config.Permissions.DefaultMode
			},
			func(extensionName, text string) {
				cs.emitSessionEvent(SessionEvent{
					Type:          SessionEventExtensionNotify,
					ExtensionName: extensionName,
					Message:       text,
				})
			},
			func(title, body string, defaultYes bool) bool {
				return cs.requestExtensionConfirm(title, body, defaultYes)
			},
			func(title string, options []string) string {
				return cs.requestExtensionSelect(title, options)
			},
			func() []extension.TaskSnapshot {
				tasks := cs.GetBackgroundTasks()
				out := make([]extension.TaskSnapshot, 0, len(tasks))
				for _, task := range tasks {
					out = append(out, extension.TaskSnapshot{
						ID:          task.ID,
						Kind:        task.Kind,
						Status:      task.Status,
						Summary:     task.Summary,
						Agent:       task.Agent,
						Task:        task.Task,
						ParentID:    task.ParentID,
						RunDir:      task.RunDir,
						StatusFile:  task.StatusFile,
						SessionFile: task.SessionFile,
						OutputFile:  task.OutputFile,
						Output:      task.Output,
						Error:       task.Error,
						CreatedAt:   task.CreatedAt,
						UpdatedAt:   task.UpdatedAt,
					})
				}
				return out
			},
			func(id, reason string) (extension.TaskSnapshot, bool) {
				if cs.taskManager == nil {
					return extension.TaskSnapshot{}, false
				}
				task, ok := cs.taskManager.Interrupt(id, reason)
				if !ok {
					return extension.TaskSnapshot{}, false
				}
				return extension.TaskSnapshot{
					ID:          task.ID,
					Kind:        task.Kind,
					Status:      task.Status,
					Summary:     task.Summary,
					Agent:       task.Agent,
					Task:        task.Task,
					ParentID:    task.ParentID,
					RunDir:      task.RunDir,
					StatusFile:  task.StatusFile,
					SessionFile: task.SessionFile,
					OutputFile:  task.OutputFile,
					Output:      task.Output,
					Error:       task.Error,
					CreatedAt:   task.CreatedAt,
					UpdatedAt:   task.UpdatedAt,
				}, true
			},
			// ForkSession dispatches a child agent via the same plumbing
			// exposed by extension/subagent and its spawn_subagent alias
			// (skills/memory injection, optional worktree isolation,
			// optional background execution).
			// See (*CodingSession).forkSession for the per-mode breakdown.
			func(ctx context.Context, opts extension.ForkOptions) (string, error) {
				return cs.forkSession(ctx, opts)
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
		if !opts.DeferStartupEvent {
			extRunner.EmitEvent(types.Event{Type: types.EventType("session_start"), Reason: "startup"})
		} else {
			cs.startupEventDeferred = true
		}
	}

	cs.installHarnessLayer()
	cs.refreshDynamicSystemPrompt()
	cs.writeRuntimeState()

	sess := &CodingSession{engine: cs}
	cs.self = sess
	return sess, nil
}

func applySettingsExtensionConfig(extensions []extension.Extension, cfg *config.Config) error {
	if cfg == nil || !cfg.DisableWorkflows {
		return nil
	}
	for _, ext := range extensions {
		if ext == nil || ext.Name() != "workflow" {
			continue
		}
		configurable, ok := ext.(extension.ConfigurableExtension)
		if !ok {
			continue
		}
		if err := configurable.ApplyConfig(map[string]any{"disabled": true}); err != nil {
			return fmt.Errorf("apply settings to extension %q: %w", ext.Name(), err)
		}
	}
	return nil
}

// wireComponents constructs the session's stateful sub-components and wires
// their dependencies. It runs once, after the session struct is populated, so
// every component can read what it needs from s.
func (s *engine) wireComponents() {
	s.todos = todo.NewStore()
	s.todos.OnChange = func() { s.writeRuntimeState() }
	s.plan = plan.New(s)
	s.worktree = worktree.New(s)
	s.bash = bash.New(s)
	s.ctxMgr = contextmgr.New(contextmgr.Deps{
		Agent:          s.agent,
		Resources:      s.resources,
		SessionManager: s.sessionManager,
		StreamFn:       func() types.StreamFn { return s.streamFn },
		APIKey:         s.getAPIKey,
		Host:           s,
	})
	s.ctxMgr.SetModel(s.model)
	s.ctxMgr.SetPolicy(s.compactionPolicy())
	if s.contextRemaining != nil {
		s.contextRemaining.SetSource(s.ctxMgr)
	}
}

// Prompt sends a user message and starts processing.
func (s *engine) Prompt(ctx context.Context, text string) error {
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
			err := cmd.Handler(s.self, cmdArgs)
			s.writeRuntimeState()
			return err
		}

		s.refreshResourcePaths()

		expandedTemplate := false
		if s.promptManager != nil {
			if template, ok := s.promptManager.Get(cmdName); ok {
				text = template.Expand(cmdArgs)
				text = prompts.SubstituteShell(text, func(command string) (string, error) {
					res, err := s.bash.Execute(ctx, command, 0)
					if err != nil {
						return "", err
					}
					if res.Stderr != "" {
						return res.Stdout + res.Stderr, nil
					}
					return res.Stdout, nil
				})
				input = strings.TrimSpace(text)
				expandedTemplate = true
			}
		}

		// Check skills when no prompt template claimed the slash command.
		if !expandedTemplate && s.skillManager != nil {
			if skill, ok := s.skillManager.Get(cmdName); ok {
				return s.executeSkill(ctx, skill, cmdArgs)
			}
		}
	}

	if s.agent.GetState().IsStreaming {
		return fmt.Errorf("agent is already processing")
	}

	// Record to session
	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeMessage, "", session.MessageData{
		Role:    types.RoleUser,
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
	s.ctxMgr.MaybeAutoCompact(ctx)

	return nil
}

// Continue resumes queued steering/follow-up work and applies the same
// post-turn maintenance as Prompt.
func (s *engine) Continue(ctx context.Context) error {
	if s == nil || s.agent == nil {
		return fmt.Errorf("session is not initialized")
	}
	err := s.agent.Continue(ctx)
	if err != nil {
		return err
	}
	s.ctxMgr.MaybeAutoCompact(ctx)
	return nil
}

func (s *engine) Close(reason string) {
	if s.extensions != nil {
		s.extensions.EmitEvent(types.Event{Type: types.EventType("session_shutdown")})
	}
	s.writeRuntimeState()
	if s.sessionManager != nil {
		_ = s.sessionManager.Flush()
	}
}

// Steer injects a high-priority message during processing.
func (s *engine) Steer(text string) {
	msg := types.UserMessage{
		Role:    "user",
		Content: text,
	}
	s.agent.Steer(msg)
}

// FollowUp queues a message for processing after the current task.
func (s *engine) FollowUp(text string) {
	msg := types.UserMessage{
		Role:    "user",
		Content: text,
	}
	s.agent.FollowUp(msg)
}

// Subscribe registers an event listener. Returns an unsubscribe function.
func (s *engine) Subscribe(fn func(types.Event)) func() {
	return s.agent.Subscribe(fn)
}

// GetAgent returns the underlying agent.
func (s *engine) GetAgent() *agent.Agent {
	return s.agent
}

// WaitForIdle blocks until the agent is idle.
func (s *engine) WaitForIdle() {
	s.agent.WaitForIdle()
}

// WaitForPendingWork blocks until all extension-registered pending background
// work completes. Call after WaitForIdle to ensure background workflows finish
// before the session closes (e.g. in print/headless mode).
func (s *engine) WaitForPendingWork() {
	if s.extensions != nil {
		s.extensions.WaitForPending()
	}
}

// Abort cancels the current operation.
func (s *engine) Abort() {
	s.agent.Abort()
}

// IsStreaming returns whether the agent is currently streaming.
func (s *engine) IsStreaming() bool {
	return s.agent.GetState().IsStreaming
}

// GetMessages returns the current message history.
func (s *engine) GetMessages() []types.AgentMessage {
	return s.agent.GetState().Messages
}

// GetEventBus returns the session's event bus.
func (s *engine) GetEventBus() eventbus.EventBusController {
	return s.eventBus
}

// SubscribeSession registers a handler for session-level events.
// Returns an unsubscribe function.
func (s *engine) SubscribeSession(fn func(SessionEvent)) func() {
	return s.eventBus.On(sessionEventChannel, func(data any) {
		if event, ok := data.(SessionEvent); ok {
			fn(event)
		}
	})
}

func (s *engine) emitSessionEvent(event SessionEvent) {
	s.eventBus.Emit(sessionEventChannel, event)
}

// IsCompacting returns whether compaction is currently in progress.
func (s *engine) IsCompacting() bool {
	return s.ctxMgr.IsCompacting()
}

// GetLastAssistantText returns the text content of the last assistant message.
func (s *engine) GetLastAssistantText() string {
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

func prepareSubagentDefinition(def *subagent.SubagentDefinition, skillMgr *skills.Manager, memoryStore *memory.Store, memoryEnabled bool) *subagent.SubagentDefinition {
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

	if memoryEnabled && memoryStore != nil {
		if mem := memoryContextForScope(memoryStore, clone.MemoryScope); mem != "" {
			parts = append(parts, mem)
		}
	}

	clone.SystemPrompt = strings.Join(parts, "\n\n---\n\n")
	return &clone
}

func memoryContextForScope(store *memory.Store, scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "none":
		return ""
	case "user", "global":
		return store.GetGlobalMemoryContext()
	case "project", "local":
		return store.GetProjectMemoryContext()
	case "both", "all":
		return store.GetMemoryContext()
	}
	return ""
}

func (s *engine) executeSkill(ctx context.Context, skill *skills.Skill, args string) error {
	task := strings.TrimSpace(args)
	if task == "" {
		task = "Use this skill for the user's request."
	}

	s.refreshDynamicSystemPrompt()

	messages := []types.AgentMessage{
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
	s.ctxMgr.MaybeAutoCompact(ctx)
	return nil
}

func (s *engine) skillPrompt(skill *skills.Skill) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("The user explicitly invoked the %q skill. Use the instructions below for this turn.", skill.Name))
	parts = append(parts, skill.Content)
	parts = append(parts, fmt.Sprintf("# Environment\n- Working directory: %s\n- All file and shell tools are already bound to this working directory.", s.cwd))
	return strings.Join(parts, "\n\n---\n\n")
}

func (s *engine) handleMessageEnd(msg types.AgentMessage) {
	if msg == nil {
		return
	}
	if isNonPersistentMessage(msg) {
		return
	}

	role, content, ok := sessionMessageData(msg)
	if !ok {
		return
	}
	if role == types.RoleUser {
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

func (s *engine) currentLeafMessageMatches(role string, content any) bool {
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

func sessionMessageData(msg types.AgentMessage) (string, any, bool) {
	switch m := msg.(type) {
	case types.UserMessage:
		return types.RoleUser, m.Content, true
	case *types.UserMessage:
		return types.RoleUser, m.Content, true
	case types.AssistantMessage:
		return types.RoleAssistant, m, true
	case *types.AssistantMessage:
		return types.RoleAssistant, *m, true
	case types.ToolResultMessage:
		return types.RoleToolResult, m, true
	case *types.ToolResultMessage:
		return types.RoleToolResult, *m, true
	default:
		return "", nil, false
	}
}
