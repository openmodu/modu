package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	codetui "github.com/openmodu/modu/cmd/modu_code/internal/tui"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

const moduTUITerminalStatusTTL = 10 * time.Second
const moduTUIContextCompactDivider = "------------- context compact ------------------"

func runModuTUI(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts RunOptions) (err error) {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
		_ = n
	}

	eventPresenter := newModuTUIEventPresenter()
	initial := moduTUITranscriptEntries(session, eventPresenter)
	if notice := strings.TrimSpace(opts.StartupNotice); notice != "" {
		initial = append(initial, modutui.Entry{
			Role:  modutui.RoleAssistant,
			Nodes: []modutui.Node{modutui.TextNode{Text: notice}},
		})
	}
	var program *tea.Program
	var programMu sync.RWMutex
	defer func() {
		if r := recover(); r != nil {
			programMu.RLock()
			p := program
			programMu.RUnlock()
			if p != nil {
				func() {
					defer func() { _ = recover() }()
					p.Kill()
				}()
			}
			restoreModuTUITerminal()
			fmt.Fprintf(os.Stderr, "modu_code TUI panic: %v\n%s\n", r, debug.Stack())
			err = fmt.Errorf("TUI panic: %v", r)
		}
	}()
	var uiClient modutui.Client
	workflowController := newModuTUIWorkflowController(session)
	rawSend := func(msg tea.Msg) {
		programMu.RLock()
		p := program
		programMu.RUnlock()
		if p != nil {
			p.Send(msg)
		}
	}
	send := func(msg tea.Msg) {
		switch typed := msg.(type) {
		case modutui.UpdateMsg:
			workflowController.ObserveUpdate(typed.Update)
		}
		rawSend(msg)
	}
	uiClient = newModuTUIClient(send)
	workflowController.SetClient(uiClient)
	configWizard := newModuTUIConfigWizard(opts.CommandHooks, uiClient)
	channelWizard := newModuTUIChannelWizard(opts.CommandHooks, uiClient)
	durationTracker := newModuTUIAgentDurationTracker(time.Now, func(entry modutui.Entry) {
		uiClient.AppendEntry(entry)
	})
	sendFooter := func() {}

	session.SetPrompter(&moduTUIPrompter{ctx: ctx, client: uiClient})
	if noApprove {
		// --no-approve skips only tool approval. Other interactive choices,
		// including the cross-directory resume cwd prompt, stay available.
		session.SetToolApprovalCallback(nil)
	}
	historyFile := session.InputHistoryFile()
	inputHistory, _ := loadModuTUIInputHistory(historyFile)

	tuiRuntime, err := codetui.NewRuntime(codetui.RuntimeOptions{
		Context:           ctx,
		Session:           moduTUIRuntimeSession{CodingSession: session},
		Client:            uiClient,
		TerminalStatusTTL: moduTUITerminalStatusTTL,
		FormatDuration:    formatModuTUIActivityDuration,
		RefreshFooter: func() {
			sendFooter()
		},
	})
	if err != nil {
		return err
	}
	// Drive hidden extension turns (goal continuations injected while idle)
	// through the foreground loop instead of a detached background goroutine, so
	// the status line reflects the running agent and ESC can interrupt it.
	session.SetBackgroundPromptDriver(func(run func(context.Context) error) bool {
		tuiRuntime.Run(run)
		return true
	})
	submit := func(ev modutui.SubmitEvent) {
		images := moduTUIImages(ev.Images)
		switch ev.Kind {
		case modutui.SubmitKindFollowUp:
			tuiRuntime.QueueFollowUp(ev.Text, images, false)
		case modutui.SubmitKindSteer:
			tuiRuntime.QueueSteer(ev.Text, images, false)
		default:
			tuiRuntime.RunPrompt(ev.Text, images)
		}
	}
	commandExecutor, err := newModuTUICommandExecutor(moduTUICommandExecutorOptions{
		Session:       session,
		Model:         model,
		Hooks:         opts.CommandHooks,
		Client:        uiClient,
		KeepAgentBusy: tuiRuntime.IsForegroundRunActive,
		StartConfig: func() {
			configWizard.Start(ctx)
		},
		StartChannel: func() {
			channelWizard.Start(ctx)
		},
		StartModelSelect: func() {
			runModuTUIModelSelect(ctx, session, uiClient)
		},
		QueueFollowUp: func(text string, requireActive bool) {
			tuiRuntime.QueueFollowUp(text, nil, requireActive)
		},
		QueueSteer: func(text string, requireActive bool) {
			tuiRuntime.QueueSteer(text, nil, requireActive)
		},
	})
	if err != nil {
		return err
	}

	width, height := initialTerminalSize(int(os.Stdout.Fd()), 120, 35)
	env := os.Environ()
	mouseDisabled := moduTUIMouseDisabledFromEnv(env)
	arrowKeysScroll := moduTUIArrowKeysScrollFromEnv(env)
	ui := modutui.NewModel(modutui.Options{
		Width:          width,
		Height:         height,
		InitialEntries: initial,
		InputHistory:   inputHistory,
		Todos:          moduTUITodos(session),
		Footer:         moduTUIFooter(session),
		InfoCardLines:  moduTUIInfoCardLines(session, model),
		SlashCommands:  commandExecutor.Suggestions(),
		Services: modutui.Services{
			ReadClipboardImages: readModuTUIClipboardImages,
			ResolvePastedImages: func(content string) ([]modutui.ImageAttachment, bool, error) {
				return resolveModuTUIPastedImages(session.Cwd(), content)
			},
			SlashCommands: func() []modutui.SlashCommand {
				return commandExecutor.Suggestions()
			},
			LoadToolArtifact: func(path string) (string, error) {
				data, err := os.ReadFile(path)
				return string(data), err
			},
		},
		DisableMouse:    mouseDisabled,
		ArrowKeysScroll: arrowKeysScroll,
		IntentHandler: codetui.IntentRouter{
			InputHistoryChanged: func(history []string) {
				_ = saveModuTUIInputHistory(historyFile, history)
			},
			Submit: submit,
			PanelAction: func(action modutui.PanelAction) {
				workflowController.HandleAction(ctx, tuiRuntime, commandExecutor, action)
			},
			PanelClosed: workflowController.Forget,
			SlashCommand: func(line string) {
				tuiRuntime.Go("slash command", func() {
					commandExecutor.Execute(ctx, line)
				})
			},
			Interrupt: tuiRuntime.Interrupt,
		}.Handle,
	})
	sendFooter = func() {
		uiClient.SetFooter(moduTUIFooter(session))
	}

	unsubscribe := (moduTUIEventBindings{
		session:       session,
		client:        uiClient,
		workflow:      workflowController,
		presenter:     eventPresenter,
		duration:      durationTracker,
		refreshFooter: sendFooter,
	}).Subscribe()
	defer unsubscribe()

	prog := tea.NewProgram(ui, tea.WithContext(ctx), tea.WithWindowSize(width, height))
	programMu.Lock()
	program = prog
	programMu.Unlock()
	tuiRuntime.Go("feishu bot startup", func() {
		startModuTUIFeishuBot(ctx, session, tuiRuntime.PromptMutex(), uiClient)
	})
	tuiRuntime.Go("telegram bot startup", func() {
		startModuTUITelegramBot(ctx, session, tuiRuntime.PromptMutex(), uiClient)
	})
	refreshDone := make(chan struct{})
	defer close(refreshDone)
	tuiRuntime.Go("workflow panel refresh", func() {
		workflowController.RunRefresh(ctx, refreshDone)
	})
	tuiRuntime.Go("startup event", func() {
		session.EmitStartupEvent()
		session.EmitExtensionEvent("ui_ready")
	})
	_, err = prog.Run()
	return err
}
