package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

type resumeCwdPromptFunc func(currentCwd, sessionCwd string) (string, error)

var promptResumeCwd resumeCwdPromptFunc = runResumeCwdPrompt

func resolveStartupResumeCwd(agentDir, currentCwd, resumeID string, interactive bool, prompt resumeCwdPromptFunc) (string, error) {
	if strings.TrimSpace(resumeID) == "" {
		return currentCwd, nil
	}
	info, err := coding_agent.ResolveSessionInfoByID(agentDir, resumeID)
	if err != nil {
		return "", err
	}
	sessionCwd := strings.TrimSpace(info.Cwd)
	if sessionCwd == "" {
		return currentCwd, nil
	}
	if !resumeCwdsDiffer(currentCwd, sessionCwd) || !interactive || prompt == nil {
		return sessionCwd, nil
	}
	selected, err := prompt(currentCwd, sessionCwd)
	if err != nil {
		return "", err
	}
	switch selected {
	case sessionCwd:
		return sessionCwd, nil
	case currentCwd:
		return currentCwd, nil
	default:
		return "", fmt.Errorf("invalid resume cwd selection %q", selected)
	}
}

func resumeCwdsDiffer(currentCwd, sessionCwd string) bool {
	currentInfo, currentErr := os.Stat(currentCwd)
	sessionInfo, sessionErr := os.Stat(sessionCwd)
	if currentErr == nil && sessionErr == nil {
		return !os.SameFile(currentInfo, sessionInfo)
	}
	currentAbs, currentAbsErr := filepath.Abs(currentCwd)
	sessionAbs, sessionAbsErr := filepath.Abs(sessionCwd)
	if currentAbsErr == nil && sessionAbsErr == nil {
		currentCwd = filepath.Clean(currentAbs)
		sessionCwd = filepath.Clean(sessionAbs)
	} else {
		currentCwd = filepath.Clean(currentCwd)
		sessionCwd = filepath.Clean(sessionCwd)
	}
	if runtime.GOOS == "windows" {
		return !strings.EqualFold(currentCwd, sessionCwd)
	}
	return currentCwd != sessionCwd
}

func resumeCwdPromptRequest(currentCwd, sessionCwd string) modutui.HumanPromptRequest {
	return modutui.HumanPromptRequest{
		ID:    "resume-cwd",
		Title: "Choose working directory to resume this session",
		Body: "Session = cwd recorded in the resumed session\n" +
			"Current = your current working directory",
		Options: []modutui.HumanPromptOption{
			{Label: "Use session directory (" + sessionCwd + ")", Value: sessionCwd},
			{Label: "Use current directory (" + currentCwd + ")", Value: currentCwd},
		},
		DefaultIndex: 0,
	}
}

type resumeCwdSelectedMsg string

type resumeCwdPromptModel struct {
	inner     modutui.Model
	request   modutui.HumanPromptRequest
	response  chan string
	selection string
}

func (m resumeCwdPromptModel) Init() tea.Cmd {
	request := m.request
	response := m.response
	return tea.Batch(
		func() tea.Msg {
			return modutui.RequestHumanPromptMsg{Request: request, Respond: response}
		},
		func() tea.Msg { return resumeCwdSelectedMsg(<-response) },
	)
}

func (m resumeCwdPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if selected, ok := msg.(resumeCwdSelectedMsg); ok {
		m.selection = string(selected)
		return m, tea.Quit
	}
	next, cmd := m.inner.Update(msg)
	if inner, ok := next.(modutui.Model); ok {
		m.inner = inner
	}
	return m, cmd
}

func (m resumeCwdPromptModel) View() tea.View { return m.inner.View() }

func runResumeCwdPrompt(currentCwd, sessionCwd string) (string, error) {
	width, height := initialTerminalSize(int(os.Stdout.Fd()), 120, 35)
	model := resumeCwdPromptModel{
		inner: modutui.NewModel(modutui.Options{
			Width:         width,
			Height:        height,
			Footer:        "Enter continue · Ctrl+C exit",
			InfoCardLines: []string{"modu_code", "resume session"},
			DisableMouse:  moduTUIMouseDisabledFromEnv(os.Environ()),
		}),
		request:  resumeCwdPromptRequest(currentCwd, sessionCwd),
		response: make(chan string, 1),
	}
	final, err := tea.NewProgram(model, tea.WithWindowSize(width, height)).Run()
	if err != nil {
		return "", err
	}
	result, ok := final.(resumeCwdPromptModel)
	if !ok || strings.TrimSpace(result.selection) == "" {
		return "", errors.New("resume cwd selection cancelled")
	}
	return result.selection, nil
}
