package modutui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type streamTickMsg struct{}
type autoScrollTickMsg struct{}
type statusExpireMsg struct {
	status string
}
type clipboardImagesMsg struct {
	images []ImageAttachment
	err    error
}
type pastedImagesResolvedMsg struct {
	content string
	images  []ImageAttachment
	handled bool
	err     error
}
type slashCommandsLoadedMsg struct {
	commands []SlashCommand
}
type toolPermissionResolvedMsg struct {
	toolID     string
	permission ToolPermissionState
}
type toolArtifactLoadedMsg struct {
	path string
	text string
	err  string
}

const maxInputHistory = 100

// bottomFixedRowsBase counts the always-present rows below the viewport: a gap,
// the agent status line, the input top rule, the input bottom rule, and the
// footer. The input area itself adds inputRows() on top (1..maxInputRows).
const bottomFixedRowsBase = 5
const maxInputRows = 5
const minViewportRows = 1
const maxAutoScrollTicksWithoutDrag = 80

type pendingApproval struct {
	request ToolApprovalRequest
	respond chan<- ToolApprovalDecision
}

type pendingHumanPrompt struct {
	request  HumanPromptRequest
	respond  chan<- string
	selected int
}

type pendingHumanText struct {
	request HumanTextRequest
	respond chan<- string
	input   InputBlock
}

type toolArtifactCacheEntry struct {
	text string
	err  string
}

type Model struct {
	width, height int

	transcriptModel
	composerModel
	overlayModel
	chromeModel

	disableMouse  bool
	services      Services
	intentHandler func(Intent)
	initCmds      []tea.Cmd
}

func NewModel(options ...Options) Model {
	opts := Options{Width: 120, Height: 35, StatusHint: DefaultStatusHint, BlockGap: 1}
	if len(options) > 0 {
		if options[0].Width > 0 {
			opts.Width = options[0].Width
		}
		if options[0].Height > 0 {
			opts.Height = options[0].Height
		}
		opts.InitialEntries = make([]Entry, len(options[0].InitialEntries))
		for i := range options[0].InitialEntries {
			opts.InitialEntries[i] = cloneEntry(options[0].InitialEntries[i])
		}
		opts.InputHistory = append([]string(nil), options[0].InputHistory...)
		opts.Todos = append([]TodoItem(nil), options[0].Todos...)
		opts.StreamReply = options[0].StreamReply
		opts.Footer = options[0].Footer
		opts.InfoCardLines = append([]string(nil), options[0].InfoCardLines...)
		opts.DisableMouse = options[0].DisableMouse
		opts.ArrowKeysScroll = options[0].ArrowKeysScroll
		opts.Services = options[0].Services
		opts.IntentHandler = options[0].IntentHandler
		opts.BlockFactories = append([]EntryBlockFactory(nil), options[0].BlockFactories...)
		opts.SlashCommands = append([]SlashCommand(nil), options[0].SlashCommands...)
		if options[0].BlockGap > 0 {
			opts.BlockGap = options[0].BlockGap
		}
		if options[0].StatusHint != "" {
			opts.StatusHint = options[0].StatusHint
		}
	}
	m := Model{
		width:  opts.Width,
		height: opts.Height,
		transcriptModel: transcriptModel{
			follow:              true,
			selStart:            cell{line: -1},
			selEnd:              cell{line: -1},
			infoCardLines:       cleanInfoCardLines(opts.InfoCardLines),
			blockFactories:      opts.BlockFactories,
			blockGap:            opts.BlockGap,
			toolArtifactCache:   make(map[string]toolArtifactCacheEntry),
			toolArtifactLoading: make(map[string]bool),
			loadToolArtifact:    opts.Services.LoadToolArtifact,
		},
		composerModel: composerModel{
			arrowKeysScroll:       opts.ArrowKeysScroll,
			slashCommands:         normalizeSlashCommands(opts.SlashCommands),
			slashCommandsProvider: opts.Services.SlashCommands,
			inputHistory:          normalizeInputHistory(opts.InputHistory),
		},
		chromeModel: chromeModel{
			streamReply: opts.StreamReply,
			statusHint:  opts.StatusHint,
			footer:      opts.Footer,
			todos:       normalizeTodos(opts.Todos),
		},
		disableMouse:  opts.DisableMouse,
		services:      opts.Services,
		intentHandler: opts.IntentHandler,
	}
	m.historyIdx = len(m.inputHistory)
	for _, entry := range opts.InitialEntries {
		m.appendEntry(entry)
	}
	for _, entry := range m.entries {
		if cmd := m.toolPermissionCmd(entry); cmd != nil {
			m.initCmds = append(m.initCmds, cmd)
		}
	}
	m.initCmds = append(m.initCmds, m.loadExpandedToolArtifactsCmd())
	m.rebuild()
	return m
}

func (m Model) Init() tea.Cmd { return batchCmds(m.initCmds...) }

func (m Model) Lines() []string {
	return append([]string(nil), m.lines...)
}

func (m *Model) submitInput(steer bool) tea.Cmd {
	v := strings.TrimSpace(m.input.ExpandedValue())
	images := m.input.ImageAttachments()
	if v == "" && len(images) == 0 {
		return nil
	}
	if len(m.slashMatches) > 0 && len(images) == 0 && !steer {
		v = m.slashMatches[clamp(m.slashIndex, 0, len(m.slashMatches)-1)].Name
	}

	trimmed := strings.TrimSpace(v)
	display := strings.TrimSpace(m.input.DisplayValue())
	kind := SubmitKindPrompt
	if m.streaming || m.busy {
		if steer {
			kind = SubmitKindSteer
		} else {
			kind = SubmitKindFollowUp
		}
	}

	m.entries = append(m.entries, Entry{
		Role:  RoleUser,
		Nodes: []Node{TextNode{Text: display}},
	})
	historyCmd := m.appendInputHistory(v)
	m.input.Reset()
	m.historyIdx = len(m.inputHistory)
	m.historyHold = ""
	m.clearSlashMatches()
	m.clearSelection()
	m.follow = true
	m.unseen = 0
	m.rebuild()
	if len(images) == 0 && strings.HasPrefix(trimmed, "/") {
		return batchCmds(historyCmd, m.emitIntent(SlashCommandIntent{Line: v}))
	}
	submitCmd := m.emitIntent(SubmitIntent{Event: SubmitEvent{Text: v, Images: images, Kind: kind}})
	if kind == SubmitKindPrompt && m.streamReply != "" {
		m.startStream()
		m.rebuild()
		return batchCmds(historyCmd, submitCmd, m.tick())
	}
	return batchCmds(historyCmd, submitCmd)
}

func (m Model) emitIntent(intent Intent) tea.Cmd {
	return intentCmd(m.intentHandler, intent)
}

func (m Model) handlesIntent(intent Intent) bool {
	return m.intentHandler != nil
}

func (m Model) toolPermissionCmd(entry Entry) tea.Cmd {
	node, _, ok := toolNodeFromEntry(entry)
	if !ok || node.Call.ID == "" || node.Permission != ToolPermissionUnknown || m.services.ToolPermission == nil {
		return nil
	}
	for _, current := range m.entries {
		currentNode, _, currentOK := toolNodeFromEntry(current)
		if currentOK && currentNode.Call.ID == node.Call.ID && currentNode.Permission != ToolPermissionUnknown {
			return nil
		}
	}
	resolve := m.services.ToolPermission
	return func() tea.Msg {
		return toolPermissionResolvedMsg{toolID: node.Call.ID, permission: resolve(node.Call)}
	}
}

func (m *Model) loadExpandedToolArtifactsCmd() tea.Cmd {
	if m.loadToolArtifact == nil {
		return nil
	}
	if m.toolArtifactCache == nil {
		m.toolArtifactCache = make(map[string]toolArtifactCacheEntry)
	}
	if m.toolArtifactLoading == nil {
		m.toolArtifactLoading = make(map[string]bool)
	}
	var cmds []tea.Cmd
	queued := make(map[string]bool)
	for _, entry := range m.entries {
		node, _, ok := toolNodeFromEntry(entry)
		if !ok {
			continue
		}
		path := strings.TrimSpace(node.Call.ArtifactPath)
		if node.Call.ArtifactRead || path == "" || (!node.Expanded && !node.Call.NoCollapse) || queued[path] {
			continue
		}
		queued[path] = true
		if entry, ok := m.toolArtifactCache[path]; ok {
			entry := entry
			cmds = append(cmds, func() tea.Msg {
				return toolArtifactLoadedMsg{path: path, text: entry.text, err: entry.err}
			})
			continue
		}
		if m.toolArtifactLoading[path] {
			continue
		}
		m.toolArtifactLoading[path] = true
		load := m.loadToolArtifact
		cmds = append(cmds, func() tea.Msg {
			text, err := load(path)
			errText := ""
			if err != nil {
				errText = err.Error()
			}
			return toolArtifactLoadedMsg{path: path, text: text, err: errText}
		})
	}
	return batchCmds(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuild()

	case tea.KeyPressMsg:
		if m.approval != nil {
			return m.handleApprovalKey(msg)
		}
		if m.humanPrompt != nil {
			return m.handleHumanPromptKey(msg)
		}
		if m.humanText != nil {
			return m.handleHumanTextKey(msg)
		}
		if m.panel != nil {
			return m.handlePanelKey(msg)
		}
		switch {
		case isCtrlCKey(msg):
			if strings.TrimSpace(m.input.ExpandedValue()) != "" {
				m.resetIMEState()
				m.input.Reset()
				m.clearHistorySelection()
				m.clearSlashMatches()
				m.rebuild()
				return m, nil
			}
			return m, tea.Quit
		case isCtrlVKey(msg) && m.services.ReadClipboardImages != nil:
			m.resetIMEState()
			read := m.services.ReadClipboardImages
			return m, func() tea.Msg {
				images, err := read()
				return clipboardImagesMsg{images: images, err: err}
			}
		case msg.String() == "ctrl+o":
			m.resetIMEState()
			if m.toggleLatestToolExpansion() {
				m.rebuild()
				return m, m.loadExpandedToolArtifactsCmd()
			}
		case isEscKey(msg):
			m.resetIMEState()
			if len(m.slashMatches) > 0 {
				m.clearSlashMatches()
			} else if m.streaming || m.busy {
				m.status = "interrupting"
				return m, m.emitIntent(InterruptIntent{})
			}
		case msg.String() == "ctrl+end":
			m.resetIMEState()
			m.jumpToBottom()
		case msg.Code == tea.KeyPgUp:
			m.resetIMEState()
			m.scroll(-max(1, m.vpHeight()-1))
		case msg.Code == tea.KeyPgDown:
			m.resetIMEState()
			m.scroll(max(1, m.vpHeight()-1))
		case msg.Code == tea.KeyEnter && msg.Mod.Contains(tea.ModAlt):
			m.resetIMEState()
			m.input.InsertNewline()
			m.clearHistorySelection()
			return m, m.refreshSlashMatches()
		case msg.String() == "shift+enter":
			m.resetIMEState()
			if cmd := m.submitInput(true); cmd != nil {
				return m, cmd
			}
		case msg.Code == tea.KeyEnter:
			m.resetIMEState()
			if cmd := m.submitInput(false); cmd != nil {
				return m, cmd
			}
		case msg.Code == tea.KeyLeft, msg.String() == "ctrl+b":
			m.resetIMEState()
			m.input.MoveLeft()
		case msg.Code == tea.KeyRight, msg.String() == "ctrl+f":
			m.resetIMEState()
			m.input.MoveRight()
		case msg.Code == tea.KeyHome, msg.String() == "ctrl+a":
			m.resetIMEState()
			m.input.MoveHome()
		case msg.Code == tea.KeyEnd, msg.String() == "ctrl+e":
			m.resetIMEState()
			m.input.MoveEnd()
		case msg.Code == tea.KeyBackspace, msg.String() == "ctrl+h":
			m.resetIMEState()
			m.input.Backspace()
			m.clearHistorySelection()
			return m, m.refreshSlashMatches()
		case isCtrlWKey(msg):
			m.resetIMEState()
			m.input.DeleteWordBackward()
			m.clearHistorySelection()
			return m, m.refreshSlashMatches()
		case msg.Code == tea.KeyDelete:
			m.resetIMEState()
			m.input.DeleteForward()
			m.clearHistorySelection()
			return m, m.refreshSlashMatches()
		case msg.Code == tea.KeyTab:
			m.resetIMEState()
			m.completeSlashMatch()
		case msg.Code == tea.KeyUp:
			m.resetIMEState()
			if len(m.slashMatches) > 0 {
				m.slashIndex = (m.slashIndex - 1 + len(m.slashMatches)) % len(m.slashMatches)
			} else if m.shouldArrowKeyScroll() {
				m.scroll(-3)
			} else {
				return m, m.navigateInputHistory(-1)
			}
		case msg.Code == tea.KeyDown:
			m.resetIMEState()
			if len(m.slashMatches) > 0 {
				m.slashIndex = (m.slashIndex + 1) % len(m.slashMatches)
			} else if m.shouldArrowKeyScroll() {
				m.scroll(3)
			} else {
				return m, m.navigateInputHistory(1)
			}
		case msg.Text != "":
			m.insertKeyText(msg.Text)
			m.clearHistorySelection()
			return m, m.refreshSlashMatches()
		}

	case tea.PasteMsg:
		if m.humanText != nil {
			m.humanText.input.InsertPaste(msg.Content)
			m.rebuild()
			return m, nil
		}
		m.resetIMEState()
		if m.services.ResolvePastedImages != nil {
			resolve := m.services.ResolvePastedImages
			content := msg.Content
			return m, func() tea.Msg {
				images, handled, err := resolve(content)
				return pastedImagesResolvedMsg{content: content, images: images, handled: handled, err: err}
			}
		}
		return m, m.insertPastedText(msg.Content)

	case pastedImagesResolvedMsg:
		if !msg.handled {
			return m, m.insertPastedText(msg.content)
		}
		if msg.err != nil {
			m.status = "image paste failed: " + msg.err.Error()
			return m, nil
		}
		for _, image := range msg.images {
			m.input.InsertImage(image)
		}
		m.status = fmt.Sprintf("attached %d image(s)", len(msg.images))
		m.clearHistorySelection()
		m.clearSlashMatches()
		m.rebuild()
		return m, nil

	case slashCommandsLoadedMsg:
		m.slashCommands = normalizeSlashCommands(msg.commands)
		m.updateSlashMatches()
		m.rebuild()
		return m, nil

	case clipboardImagesMsg:
		if msg.err != nil {
			m.status = "image paste failed: " + msg.err.Error()
			return m, nil
		}
		if len(msg.images) == 0 {
			m.status = "clipboard has no supported image"
			return m, nil
		}
		for _, image := range msg.images {
			m.input.InsertImage(image)
		}
		m.status = fmt.Sprintf("attached %d image(s)", len(msg.images))
		m.clearHistorySelection()
		m.clearSlashMatches()
		m.rebuild()

	case clipboardCopyResultMsg:
		m.status = fmt.Sprintf("✓ copied %d chars (%s)", msg.chars, msg.how)
		if msg.needsOSC52 {
			// OSC52 has no acknowledgement. Keep the terminal-native selection
			// fallback visible when a multiplexer or terminal drops it.
			m.status += " · no clipboard? Shift+drag to copy via terminal"
			return m, tea.Batch(tea.SetClipboard(msg.text), tea.Raw(msg.osc52String))
		}
		return m, nil

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scroll(-3)
		case tea.MouseWheelDown:
			m.scroll(3)
		}

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			return m, m.onPress(msg.X, msg.Y)
		}

	case tea.MouseMotionMsg:
		if m.selecting {
			return m, m.onDrag(msg.X, msg.Y)
		}

	case tea.MouseReleaseMsg:
		if m.selecting {
			m.selecting = false
			m.autoScroll = 0
			m.autoScrollTicks = 0
			return m, m.copySelection()
		}

	case autoScrollTickMsg:
		if m.selecting && m.autoScroll != 0 {
			m.autoScrollTicks++
			if m.autoScrollTicks > maxAutoScrollTicksWithoutDrag {
				m.autoScroll = 0
				m.autoScrolling = false
				m.clearSelection()
				return m, nil
			}
			m.scroll(m.autoScroll)
			edge := m.yOffset
			if m.autoScroll > 0 {
				edge = m.yOffset + m.vpHeight() - 1
			}
			m.selEnd = m.cellAt(edge, m.dragCol)
			return m, m.autoScrollTick()
		}
		m.autoScrolling = false

	case streamTickMsg:
		if m.streaming {
			m.streamIdx += 4
			if m.streamIdx >= len(m.streamRunes) {
				m.streamIdx = len(m.streamRunes)
				m.finishStream()
			}
			m.rebuild()
			if m.streaming {
				return m, m.tick()
			}
		}

	case UpdateMsg:
		return m.applyHostUpdate(msg.Update)

	case statusExpireMsg:
		if m.status == msg.status && m.status == m.statusExpiresText && !m.statusExpiresAt.IsZero() && !time.Now().Before(m.statusExpiresAt) {
			m.status = ""
			m.statusExpiresAt = time.Time{}
			m.statusExpiresText = ""
		}

	case toolPermissionResolvedMsg:
		if msg.toolID == "" {
			return m, nil
		}
		changed := false
		for i := range m.entries {
			node, nodeIndex, ok := toolNodeFromEntry(m.entries[i])
			if ok && node.Call.ID == msg.toolID {
				node.Permission = msg.permission
				setEntryToolNode(&m.entries[i], nodeIndex, node)
				changed = true
			}
		}
		if changed {
			m.rebuild()
		}
		return m, nil

	case toolArtifactLoadedMsg:
		delete(m.toolArtifactLoading, msg.path)
		entry := toolArtifactCacheEntry{text: msg.text, err: msg.err}
		m.toolArtifactCache[msg.path] = entry
		changed := false
		for i := range m.entries {
			node, nodeIndex, ok := toolNodeFromEntry(m.entries[i])
			if ok && node.Call.ArtifactPath == msg.path {
				node.Call.ArtifactText = entry.text
				node.Call.ArtifactErr = entry.err
				node.Call.ArtifactRead = true
				setEntryToolNode(&m.entries[i], nodeIndex, node)
				changed = true
			}
		}
		if changed {
			m.rebuild()
		}
		return m, nil

	case RequestToolApprovalMsg:
		m.overlayModel.openApproval(pendingApproval{request: msg.Request, respond: msg.Respond})
		m.clearSlashMatches()
		m.follow = true
		m.unseen = 0
		m.rebuild()

	case CancelToolApprovalMsg:
		if m.overlayModel.cancelApproval(msg.ID) {
			m.rebuild()
		}

	case RequestHumanPromptMsg:
		req := normalizeHumanPromptRequest(msg.Request)
		selected := req.DefaultIndex
		if selected < 0 && len(req.Options) > 0 {
			selected = 0
		}
		m.overlayModel.openHumanPrompt(pendingHumanPrompt{request: req, respond: msg.Respond, selected: selected})
		m.clearSlashMatches()
		m.follow = true
		m.unseen = 0
		m.rebuild()

	case CancelHumanPromptMsg:
		if m.overlayModel.cancelHumanPrompt(msg.ID) {
			m.rebuild()
		}

	case RequestHumanTextMsg:
		req := normalizeHumanTextRequest(msg.Request)
		input := InputBlock{}
		if req.Default != "" {
			input.Insert(req.Default)
		}
		m.overlayModel.openHumanText(pendingHumanText{request: req, respond: msg.Respond, input: input})
		m.clearSlashMatches()
		m.follow = true
		m.unseen = 0
		m.rebuild()

	case CancelHumanTextMsg:
		if m.overlayModel.cancelHumanText(msg.ID) {
			m.rebuild()
		}
	}

	return m, nil
}

func (m Model) applyHostUpdate(update Update) (tea.Model, tea.Cmd) {
	switch update := update.(type) {
	case AppendEntryUpdate:
		return m.appendHostEntry(update.Entry)
	case UpsertEntryUpdate:
		wasAtBottom := m.atBottom()
		added := m.upsertEntry(update.Entry)
		m.rebuild()
		if wasAtBottom || m.follow {
			m.follow = true
			m.unseen = 0
		} else if added {
			m.unseen++
		}
		m.clampScroll()
		return m, nil
	case RemoveEntryUpdate:
		if m.removeEntry(update.ID) {
			m.rebuild()
			m.clampScroll()
		}
		return m, nil
	case ReplaceEntriesUpdate:
		m.entries = nil
		m.clearSelection()
		for _, entry := range update.Entries {
			m.appendEntry(entry)
		}
		m.follow = true
		m.unseen = 0
		m.rebuild()
		m.clampScroll()
		return m, nil
	case ClearEntriesUpdate:
		m.entries = nil
		m.clearSelection()
		m.follow = true
		m.unseen = 0
		m.rebuild()
		return m, nil
	case SetTodoListUpdate:
		m.todos = normalizeTodos(update.Items)
		m.todosCurrent = (m.busy || m.streaming) && hasOutstandingTodos(m.todos)
		m.clampScroll()
		return m, nil
	case ShowPanelUpdate:
		m.openHostPanel(update.Panel, false)
		return m, nil
	case RefreshPanelUpdate:
		m.openHostPanel(update.Panel, true)
		return m, nil
	case ClosePanelUpdate:
		if m.overlayModel.closePanel(update.ID) {
			m.rebuild()
		}
		return m, nil
	case SetStatusUpdate:
		m.status = update.Status
		m.statusExpiresAt = time.Time{}
		m.statusExpiresText = ""
		if update.Status != "" && update.TTL > 0 {
			m.statusExpiresAt = time.Now().Add(update.TTL)
			m.statusExpiresText = update.Status
			return m, tea.Tick(update.TTL, func(time.Time) tea.Msg {
				return statusExpireMsg{status: update.Status}
			})
		}
		return m, nil
	case SetBusyUpdate:
		if update.Busy != m.busy {
			m.todosCurrent = false
		}
		m.busy = update.Busy
		m.clampScroll()
		return m, nil
	case SetFooterUpdate:
		m.footer = update.Footer
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) appendHostEntry(entry Entry) (tea.Model, tea.Cmd) {
	entry = cloneEntry(entry)
	wasAtBottom := m.atBottom()
	added := m.appendEntry(entry)
	m.rebuild()
	if wasAtBottom || m.follow {
		m.follow = true
		m.unseen = 0
		m.clampScroll()
	} else {
		m.follow = false
		if added {
			m.unseen++
		}
		m.clampScroll()
	}
	return m, batchCmds(m.toolPermissionCmd(entry), m.loadExpandedToolArtifactsCmd())
}

func (m *Model) openHostPanel(panel Panel, refresh bool) {
	panel = normalizePanel(panel)
	if refresh {
		m.overlayModel.refreshPanel(panel)
	} else {
		m.overlayModel.openPanel(panel)
	}
	m.clearSlashMatches()
	m.clearSelection()
	m.rebuild()
	m.ensurePanelSelectionVisible()
	m.rebuild()
}

func (m *Model) insertPastedText(content string) tea.Cmd {
	m.input.InsertPaste(content)
	m.clearHistorySelection()
	cmd := m.refreshSlashMatches()
	m.rebuild()
	return cmd
}

func (m *Model) insertKeyText(text string) {
	text = normalizeInputText(text)
	if text == "" {
		return
	}
	if m.coalesceIMEText(text) {
		return
	}

	m.input.Insert(text)
	if isASCIICompositionText(text) {
		m.imeTail = text
		m.imeActive = utf8.RuneCountInString(text) > 1
		return
	}
	m.resetIMEState()
}

func (m *Model) coalesceIMEText(text string) bool {
	if m.imeTail == "" || m.input.Cursor != m.input.Len() || !strings.HasSuffix(m.input.Value, m.imeTail) {
		m.resetIMEState()
		return false
	}

	tail := m.imeTail
	switch {
	case isASCIICompositionText(tail) && isASCIICompositionText(text) && text != tail && hasPrefixEither(text, tail):
		m.input.ReplaceBeforeCursor(utf8.RuneCountInString(tail), text)
	case isASCIICompositionText(tail) && containsHan(text) && (m.imeActive || utf8.RuneCountInString(tail) > 1):
		m.input.ReplaceBeforeCursor(utf8.RuneCountInString(tail), text)
	case m.imeActive && containsHan(tail) && containsHan(text):
		if strings.HasPrefix(tail, text) {
			return true
		}
		m.input.ReplaceBeforeCursor(utf8.RuneCountInString(tail), text)
	default:
		m.resetIMEState()
		return false
	}

	m.imeTail = text
	m.imeActive = true
	return true
}

func (m *Model) resetIMEState() {
	m.imeTail = ""
	m.imeActive = false
}

func hasPrefixEither(a, b string) bool {
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func isASCIICompositionText(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == '\'' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		return false
	}
	return true
}

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func (m *Model) appendEntry(entry Entry) bool {
	entry = normalizeEntry(entry)
	node, _, isTool := toolNodeFromEntry(entry)
	if isTool && node.Call.ID != "" {
		for i := range m.entries {
			current, nodeIndex, ok := toolNodeFromEntry(m.entries[i])
			if ok && current.Call.ID == node.Call.ID {
				setEntryToolNode(&m.entries[i], nodeIndex, mergeToolNode(current, node))
				return false
			}
		}
	}
	m.entries = append(m.entries, entry)
	return true
}

func (m *Model) upsertEntry(entry Entry) bool {
	entry = normalizeEntry(entry)
	if entry.ID != "" {
		for i := range m.entries {
			if m.entries[i].ID == entry.ID {
				m.entries[i] = entry
				return false
			}
		}
	}
	return m.appendEntry(entry)
}

func (m *Model) removeEntry(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for i := range m.entries {
		if m.entries[i].ID != id {
			continue
		}
		copy(m.entries[i:], m.entries[i+1:])
		m.entries = m.entries[:len(m.entries)-1]
		return true
	}
	return false
}

func (m *Model) toggleLatestToolExpansion() bool {
	for i := len(m.entries) - 1; i >= 0; i-- {
		node, _, ok := toolNodeFromEntry(m.entries[i])
		if !ok || node.Call.NoCollapse {
			continue
		}
		// A batched tool folds/unfolds as one group; toggle the group's first
		// message, whose Expanded flag is the group's collapse state.
		gi := m.batchGroupStart(i)
		setEntryExpanded(&m.entries[gi], !entryExpanded(m.entries[gi]))
		return true
	}
	return false
}

// batchGroupLen returns how many consecutive messages starting at idx belong to
// the same parallel batch (shared non-empty ToolBatchID). Returns 1 for a
// single (non-batched) tool or non-tool message.
func (m *Model) batchGroupLen(idx int) int {
	node, _, ok := toolNodeFromEntry(m.entries[idx])
	if !ok || node.Call.BatchID == "" {
		return 1
	}
	id := node.Call.BatchID
	n := 1
	for idx+n < len(m.entries) {
		next, _, nextOK := toolNodeFromEntry(m.entries[idx+n])
		if !nextOK || next.Call.BatchID != id {
			break
		}
		n++
	}
	return n
}

// batchGroupStart returns the index of the first message in idx's batch group,
// or idx itself when it is not part of a batch.
func (m *Model) batchGroupStart(idx int) int {
	node, _, ok := toolNodeFromEntry(m.entries[idx])
	if !ok || node.Call.BatchID == "" {
		return idx
	}
	id := node.Call.BatchID
	for idx > 0 {
		previous, _, previousOK := toolNodeFromEntry(m.entries[idx-1])
		if !previousOK || previous.Call.BatchID != id {
			break
		}
		idx--
	}
	return idx
}

func toolGroupBlockFrom(group []Entry) ToolGroupBlock {
	calls := make([]ToolCall, 0, len(group))
	for _, entry := range group {
		node, _, ok := toolNodeFromEntry(entry)
		if ok {
			calls = append(calls, node.Call)
		}
	}
	return ToolGroupBlock{Calls: calls, Expanded: entryExpanded(group[0])}
}

func mergeToolNode(base, update ToolNode) ToolNode {
	if update.Call.Name != "" {
		base.Call.Name = update.Call.Name
	}
	if update.Call.Summary != "" && (!base.Call.Done || update.Call.Done) {
		base.Call.Summary = update.Call.Summary
	}
	if update.Call.Detail != "" {
		base.Call.Detail = update.Call.Detail
	}
	if update.Call.Input != "" {
		base.Call.Input = update.Call.Input
	}
	if update.Call.Output != "" {
		base.Call.Output = update.Call.Output
	}
	if update.Permission != ToolPermissionUnknown {
		base.Permission = update.Permission
	}
	if update.Call.ArtifactID != "" {
		base.Call.ArtifactID = update.Call.ArtifactID
	}
	if update.Call.ArtifactPath != "" {
		if base.Call.ArtifactPath != "" && base.Call.ArtifactPath != update.Call.ArtifactPath {
			base.Call.ArtifactText = ""
			base.Call.ArtifactErr = ""
			base.Call.ArtifactRead = false
		}
		base.Call.ArtifactPath = update.Call.ArtifactPath
	}
	if update.Call.ArtifactRead {
		base.Call.ArtifactText = update.Call.ArtifactText
		base.Call.ArtifactErr = update.Call.ArtifactErr
		base.Call.ArtifactRead = true
	}
	base.Call.Truncated = base.Call.Truncated || update.Call.Truncated
	if update.Call.BatchSize > 0 {
		base.Call.BatchSize = update.Call.BatchSize
	}
	if update.Call.BatchID != "" {
		base.Call.BatchID = update.Call.BatchID
	}
	if update.Call.Code != "" {
		base.Call.Code = update.Call.Code
	}
	if update.Call.Language != "" {
		base.Call.Language = update.Call.Language
	}
	base.Call.Error = base.Call.Error || update.Call.Error
	base.Call.Done = base.Call.Done || update.Call.Done
	base.Call.NoCollapse = base.Call.NoCollapse || update.Call.NoCollapse
	base.Expanded = base.Expanded || update.Expanded
	return base
}

func normalizeEntry(entry Entry) Entry {
	entry = cloneEntry(entry)
	if entry.ID == "" {
		if node, _, ok := toolNodeFromEntry(entry); ok {
			entry.ID = strings.TrimSpace(node.Call.ID)
		}
	}
	return entry
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	if m.disableMouse {
		v.MouseMode = tea.MouseModeNone
	} else {
		v.MouseMode = tea.MouseModeCellMotion
	}
	_, cursorRow, caretX := m.input.Render(m.inputRenderWidth(), m.inputRows())
	if m.hasBlockingPrompt() {
		caretX, cursorRow = 0, 0
	}
	v.Cursor = tea.NewCursor(caretX, m.vpHeight()+m.approvalPanelHeight()+m.humanPromptPanelHeight()+m.slashPanelHeight()+m.todoPanelHeight()+3+cursorRow)
	return v
}

// inputRows is the number of rows the input area currently occupies: the visual
// wrapped line count clamped to [1, maxInputRows]. Blocking prompt modes use a
// single placeholder line.
func (m *Model) inputRows() int {
	if m.hasBlockingPrompt() || m.panel != nil {
		return 1
	}
	return clamp(m.input.VisualLineCount(m.inputRenderWidth()), 1, maxInputRows)
}

func (m *Model) bottomFixedRows() int {
	return bottomFixedRowsBase + m.inputRows()
}

func (m *Model) vpHeight() int {
	return max(m.minViewportRows(), m.height-m.bottomFixedRows()-m.approvalPanelHeight()-m.humanPromptPanelHeight()-m.slashPanelHeight()-m.todoPanelHeight())
}
func (m *Model) approvalPanelHeight() int {
	return len(m.approvalPanelLines())
}
func (m *Model) humanPromptPanelHeight() int {
	return len(m.humanPromptPanelLines())
}
func (m *Model) slashPanelHeight() int {
	return len(m.slashPanelLines())
}
func (m *Model) todoPanelHeight() int {
	return len(m.todoPanelLines())
}
func (m *Model) showJumpPanel() bool {
	if m.panel != nil {
		return false
	}
	if m.vpHeight() <= m.minViewportRows() {
		return false
	}
	return !m.atBottom()
}

func (m *Model) minViewportRows() int {
	if m.height <= m.bottomFixedRows() {
		return 0
	}
	return minViewportRows
}

func (m *Model) hasBlockingPrompt() bool {
	return m.approval != nil || m.humanPrompt != nil || m.humanText != nil
}
func (m *Model) maxOffset() int {
	if m.panel != nil {
		return max(0, len(m.panelLines)-m.vpHeight())
	}
	return max(0, len(m.lines)-m.vpHeight())
}
func (m *Model) viewOffset() int {
	if m.panel != nil {
		return m.panelOffset
	}
	return m.yOffset
}
func (m *Model) atBottom() bool { return m.viewOffset() >= m.maxOffset() }

func (m *Model) scroll(n int) {
	if m.panel != nil {
		m.panelOffset = clamp(m.panelOffset+n, 0, m.maxOffset())
		return
	}
	before := m.yOffset
	m.yOffset = clamp(m.yOffset+n, 0, m.maxOffset())
	if m.yOffset < before {
		m.follow = false
	} else {
		m.follow = m.atBottom()
	}
	if m.follow {
		m.unseen = 0
	}
}

func (m *Model) jumpToBottom() {
	m.clearSelection()
	m.follow = true
	m.autoScroll = 0
	m.unseen = 0
	m.clampScroll()
}

func (m *Model) rebuild() {
	m.lines, m.gutters, m.headers = m.buildTranscript()
	m.panelLines = m.buildPanelLines()
	m.clampSelection()
	m.clampScroll()
}

func (m *Model) gutterAt(li int) int {
	if li >= 0 && li < len(m.gutters) {
		return m.gutters[li]
	}
	return 0
}

func (m *Model) clampScroll() {
	if m.panel != nil {
		m.panelOffset = clamp(m.panelOffset, 0, m.maxOffset())
		return
	}
	if m.follow && !m.selecting {
		m.yOffset = m.maxOffset()
	} else {
		m.yOffset = clamp(m.yOffset, 0, m.maxOffset())
	}
}

func (m *Model) buildTranscript() ([]string, []int, map[int]int) {
	width := max(m.width, 1)
	contentWidth := max(1, width-2)
	ctx := RenderContext{ContentWidth: contentWidth, Markdown: markdownRenderer(contentWidth)}
	var lines []string
	var gutters []int
	headers := map[int]int{}
	addGap := func() {
		for range m.blockGap {
			lines = append(lines, "")
			gutters = append(gutters, 0)
		}
	}

	if len(m.infoCardLines) > 0 {
		for _, line := range (CardBlock{Lines: m.infoCardLines}).RenderWidth(width) {
			lines = append(lines, line)
			gutters = append(gutters, 0)
		}
		if len(m.entries) > 0 || m.streaming {
			addGap()
		}
	}

	for idx := 0; idx < len(m.entries); {
		entry := m.entries[idx]
		startLine := len(lines)

		if groupLen := m.batchGroupLen(idx); groupLen > 1 {
			block := toolGroupBlockFrom(m.entries[idx : idx+groupLen])
			for offset, line := range block.Render(ctx).Lines {
				// Only the first (header) line is a header: clicking it toggles
				// the whole group via the group's first message.
				if offset == 0 {
					headers[startLine] = idx
				}
				lines = append(lines, line.Text)
				gutters = append(gutters, line.Gutter)
			}
			idx += groupLen
			if idx < len(m.entries) {
				addGap()
			}
			continue
		}

		rendered := m.blockFromEntry(entry).Render(ctx).Lines
		tool, _, hasTool := toolNodeFromEntry(entry)
		_, _, hasThinking := thinkingNodeFromEntry(entry)
		expanded := entryExpanded(entry)
		for offset, line := range rendered {
			if (hasThinking || hasTool) && !tool.Call.NoCollapse && (offset == 0 || expanded) {
				headers[startLine+offset] = idx
			}
			lines = append(lines, line.Text)
			gutters = append(gutters, line.Gutter)
		}
		idx++
		if idx < len(m.entries) {
			addGap()
		}
	}
	if m.streaming {
		if len(lines) > 0 {
			addGap()
		}
		block := TextBlock{
			Marker: streamingAssistantMarkerStyle.Render("● "),
			Text:   string(m.streamRunes[:m.streamIdx]),
		}.Render(ctx)
		for _, line := range block.Lines {
			lines = append(lines, line.Text)
			gutters = append(gutters, line.Gutter)
		}
		lines, gutters = append(lines, "  "+dimStyle.Render("┄ streaming…")), append(gutters, 2)
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines, gutters = lines[:len(lines)-1], gutters[:len(gutters)-1]
	}
	return lines, gutters, headers
}

func (m *Model) buildPanelLines() []string {
	if m.panel == nil {
		m.panelRowLines = nil
		return nil
	}
	m.panelRowLines = nil
	width := max(1, m.width)
	innerWidth := max(1, width-4)
	panel := *m.panel
	var body []string
	title := strings.TrimSpace(panel.Title)
	if title == "" {
		title = "Panel"
	}
	body = append(body, panelTitleStyle.Render(title))
	if subtitle := strings.TrimSpace(panel.Subtitle); subtitle != "" {
		body = append(body, dimStyle.Render(ansi.Truncate(subtitle, innerWidth, "…")))
	}
	if len(panel.Lines) > 0 {
		body = append(body, "")
	}
	body = append(body, panelBodyLines(panel.Lines, innerWidth, panel.Markdown)...)
	if len(panel.Rows) > 0 {
		if len(panel.Lines) > 0 {
			body = append(body, "")
		}
		for i, row := range panel.Rows {
			m.panelRowLines = append(m.panelRowLines, len(body)+1)
			body = append(body, panelRowLine(row, i, m.panelSelected, innerWidth))
		}
	}
	footer := strings.TrimSpace(panel.Footer)
	if footer == "" {
		if len(panel.Rows) > 0 {
			footer = "[up/down] select  [enter] open  [esc/q] close"
		} else {
			footer = "[esc/q] close  [up/down] scroll"
		}
	}
	body = append(body, "", dimStyle.Render(footer))
	return CardBlock{Lines: body}.RenderWidth(width)
}

func panelBodyLines(lines []string, width int, renderMarkdown bool) []string {
	width = max(1, width)
	var renderer MarkdownRenderer
	if renderMarkdown {
		renderer = markdownRenderer(width)
	}
	var out []string
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) == "" {
			out = append(out, "")
			i++
			continue
		}

		block := []string{lines[i]}
		if panelLineStartsCodeFence(lines[i]) {
			i++
			for i < len(lines) {
				block = append(block, lines[i])
				if panelLineStartsCodeFence(lines[i]) {
					i++
					break
				}
				i++
			}
		} else {
			i++
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				block = append(block, lines[i])
				i++
			}
		}

		if renderMarkdown && panelBlockLooksMarkdown(block) {
			out = append(out, panelMarkdownLines(renderer, strings.Join(block, "\n"), width)...)
		} else {
			for _, raw := range block {
				out = append(out, panelPlainLines(raw, width)...)
			}
		}
	}
	return out
}

func panelPlainLines(raw string, width int) []string {
	raw = panelStylePlainLine(raw)
	wrapped := ansi.Wrap(raw, width, "")
	if wrapped == "" {
		return []string{""}
	}
	return strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n")
}

func panelStylePlainLine(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw
	}
	if strings.HasPrefix(trimmed, "+-- ") {
		return panelSectionStyle.Render(raw)
	}
	if panelLooksLikeSectionHeading(raw) {
		return panelSectionStyle.Render(raw)
	}
	return raw
}

func panelLooksLikeSectionHeading(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed != raw {
		return false
	}
	if strings.ContainsAny(trimmed, ":|[]{}()") || strings.Contains(trimmed, "->") {
		return false
	}
	if strings.HasPrefix(trimmed, "#") || panelLineStartsMarkdownBlock(trimmed) {
		return false
	}
	words := strings.Fields(trimmed)
	return len(words) > 0 && len(words) <= 4 && len(trimmed) <= 48
}

func panelMarkdownLines(renderer MarkdownRenderer, source string, width int) []string {
	rendered, err := renderMarkdownWithBorderedTables(renderer, source, width)
	if err != nil {
		var out []string
		for _, line := range strings.Split(source, "\n") {
			out = append(out, panelPlainLines(line, width)...)
		}
		return out
	}
	rendered = strings.TrimRight(rendered, "\n")
	if rendered == "" {
		return nil
	}
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		if heading, ok := panelMarkdownHeadingText(line); ok {
			lines[i] = panelSectionStyle.Render(heading)
		}
	}
	return lines
}

func panelBlockLooksMarkdown(lines []string) bool {
	for _, line := range lines {
		if panelLineStartsMarkdownBlock(line) || panelLineHasInlineMarkdown(line) {
			return true
		}
	}
	return false
}

func panelLineStartsMarkdownBlock(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "> ") || panelLineStartsCodeFence(trimmed) {
		return true
	}
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return true
	}
	if len(trimmed) >= 3 && trimmed[0] >= '0' && trimmed[0] <= '9' {
		for i := 1; i < len(trimmed); i++ {
			if trimmed[i] == '.' && i+1 < len(trimmed) && trimmed[i+1] == ' ' {
				return true
			}
			if trimmed[i] < '0' || trimmed[i] > '9' {
				break
			}
		}
	}
	if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
		return true
	}
	return false
}

func panelOrderedMarkdownLine(line string) bool {
	if len(line) < 3 || line[0] < '0' || line[0] > '9' {
		return false
	}
	for i := 1; i < len(line); i++ {
		if line[i] == '.' && i+1 < len(line) && line[i+1] == ' ' {
			return true
		}
		if line[i] < '0' || line[i] > '9' {
			return false
		}
	}
	return false
}

func panelLineStartsCodeFence(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

func panelLineHasInlineMarkdown(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, "**") ||
		strings.Contains(trimmed, "__") ||
		strings.Contains(trimmed, "`") ||
		strings.Contains(trimmed, "](")
}

func panelMarkdownHeadingText(line string) (string, bool) {
	trimmed := strings.TrimSpace(ansi.Strip(line))
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	markers := 0
	for markers < len(trimmed) && trimmed[markers] == '#' {
		markers++
	}
	if markers == 0 || markers > 6 || markers >= len(trimmed) || trimmed[markers] != ' ' {
		return "", false
	}
	text := strings.TrimSpace(trimmed[markers:])
	return text, text != ""
}

func panelRowLine(row PanelRow, idx, selected, width int) string {
	label := strings.TrimSpace(row.Label)
	if label == "" {
		label = strings.TrimSpace(row.Value)
	}
	if label == "" {
		label = fmt.Sprintf("item %d", idx+1)
	}
	prefix := "  "
	if idx == selected {
		prefix = "> "
	}
	line := prefix + label
	if detail := strings.TrimSpace(row.Detail); detail != "" {
		space := max(1, width-ansi.StringWidth(ansi.Strip(line))-ansi.StringWidth(detail))
		line += strings.Repeat(" ", space) + detail
	}
	line = ansi.Truncate(line, max(1, width), "…")
	if idx == selected {
		return selStyle.Render(fitLine(line, width))
	}
	return line
}

func (m *Model) blockFromEntry(entry Entry) Block {
	for _, factory := range m.blockFactories {
		if block, ok := factory(entry); ok {
			return block
		}
	}
	return defaultBlockFromEntry(entry)
}

func (m *Model) render() string {
	h := m.vpHeight()
	sourceLines := m.lines
	offset := m.yOffset
	if m.panel != nil {
		sourceLines = m.panelLines
		offset = m.panelOffset
	}
	window := make([]string, 0, h)
	for i := range h {
		li := offset + i
		if li < len(sourceLines) {
			line := sourceLines[li]
			if m.panel == nil {
				line = m.highlightLine(li)
			}
			window = append(window, fitLine(line, m.width))
		} else {
			window = append(window, fitLine("", m.width))
		}
	}
	view := strings.Join(window, "\n")

	inputLines, _, _ := m.input.Render(m.inputRenderWidth(), m.inputRows())
	if m.approval != nil {
		inputLines = []string{fitLine(dimStyle.Render(" approval pending "), m.inputRenderWidth())}
	} else if m.panel != nil {
		inputLines = []string{fitLine(dimStyle.Render(" panel open "), m.inputRenderWidth())}
	} else if m.humanPrompt != nil || m.humanText != nil {
		inputLines = []string{fitLine(dimStyle.Render(" human input pending "), m.inputRenderWidth())}
	}
	for i := range inputLines {
		inputLines[i] = clearToEndOfLine(inputLines[i])
	}
	state := "○ idle"
	if m.streaming {
		state = "● streaming"
	} else if m.approval != nil {
		state = "● approval"
	} else if m.panel != nil {
		state = "● panel"
	} else if m.humanPrompt != nil || m.humanText != nil {
		state = "● input"
	} else if m.busy {
		state = "● running"
	}
	inner := agentStatusText(state, m.status)
	if m.panel != nil {
		inner = agentStatusText(state, panelStatusText(m.panel, m.panelOffset, m.maxOffset()))
	} else if (m.busy || m.streaming) && !m.hasBlockingPrompt() && !m.showJumpPanel() && runStatusAllowsHint(m.status) {
		inner += "  ·  " + steerFollowupHint
	}
	status := m.statusLine(inner)
	footer := fitLine(dimStyle.Render(" "+m.footer+" "), m.width)

	parts := []string{view}
	if panel := m.approvalPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	if panel := m.humanPromptPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	if panel := m.slashPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	if panel := m.todoPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	parts = append(parts,
		fitLine("", m.width),
		status,
		m.inputTopRuleLine(),
	)
	parts = append(parts, inputLines...)
	parts = append(parts,
		ruleStyle.Render(strings.Repeat("─", max(0, m.width))),
		footer,
	)
	return strings.Join(parts, "\n")
}

// steerFollowupHint is shown in the agent status line while a task is running
// so the operator knows how a typed message will be delivered.
const steerFollowupHint = "Enter follow-up · ⇧Enter steer"

// runStatusAllowsHint reports whether the running-state hint should be appended,
// i.e. only during the plain running state, not while a transient status
// (interrupting / steering / queued / completed) is being shown.
func runStatusAllowsHint(status string) bool {
	switch strings.TrimSpace(status) {
	case "", "running", "idle":
		return true
	default:
		return false
	}
}

func agentStatusText(state, status string) string {
	status = strings.TrimSpace(status)
	switch {
	case status == "", status == "idle", status == "running":
		return state
	case strings.HasPrefix(status, "✓"):
		return status
	default:
		return state + " · " + status
	}
}

func panelStatusText(panel *Panel, offset, maxOffset int) string {
	if panel == nil {
		return ""
	}
	title := strings.TrimSpace(panel.Title)
	if title == "" {
		title = "panel"
	}
	if maxOffset > 0 {
		return fmt.Sprintf("%s · %d/%d · esc closes", title, offset+1, maxOffset+1)
	}
	return title + " · esc closes"
}

func (m *Model) statusLine(inner string) string {
	if m.showJumpPanel() {
		jump := m.jumpHint()
		jumpWidth := ansi.StringWidth(jump)
		if jumpWidth >= m.width {
			return fitLine(jump, m.width)
		}
		jumpStart := max(0, (m.width-jumpWidth)/2)
		left := ansi.Truncate(" "+inner+" ", jumpStart, "…")
		return fitLine(fitLine(dimStyle.Render(left), jumpStart)+jump, m.width)
	}
	return fitLine(dimStyle.Render(" "+inner+" "), m.width)
}

func (m *Model) inputRenderWidth() int {
	if m.width <= 1 {
		return max(1, m.width)
	}
	return m.width - 1
}

func clearToEndOfLine(line string) string {
	return line + "\x1b[K"
}

func (m *Model) inputTopRuleLine() string {
	width := max(0, m.width)
	hint := m.inputHistoryHint()
	if hint == "" || width <= 0 {
		return ruleStyle.Render(strings.Repeat("─", width))
	}
	label := " " + hint + " "
	labelWidth := ansi.StringWidth(label)
	if labelWidth >= width {
		return dimStyle.Render(ansi.Truncate(label, width, ""))
	}
	leftWidth := min(3, width-labelWidth)
	rightWidth := max(0, width-labelWidth-leftWidth)
	return ruleStyle.Render(strings.Repeat("─", leftWidth)) +
		dimStyle.Render(label) +
		ruleStyle.Render(strings.Repeat("─", rightWidth))
}

func (m *Model) slashPanelLines() []string {
	if m.hasBlockingPrompt() || len(m.slashMatches) == 0 {
		return nil
	}
	available := m.height - m.bottomFixedRows() - m.minViewportRows() - m.approvalPanelHeight() - m.humanPromptPanelHeight()
	if available < 3 {
		return nil
	}
	commands := m.slashMatches
	selected := clamp(m.slashIndex, 0, len(commands)-1)
	maxRows := max(1, available-2)
	if len(commands) > maxRows {
		if available == 3 {
			commands = []SlashCommand{commands[selected]}
			selected = 0
		} else {
			maxRows = max(1, available-3)
		}
	}
	return SlashCommandBlock{
		Commands: commands,
		Selected: selected,
		MaxRows:  min(8, maxRows),
	}.RenderWidth(m.width)
}

func (m *Model) todoPanelLines() []string {
	if m.hasBlockingPrompt() || (!m.busy && !m.streaming) || !m.todosCurrent {
		return nil
	}
	budget := m.height - m.bottomFixedRows() - m.minViewportRows() - m.approvalPanelHeight() - m.humanPromptPanelHeight() - m.slashPanelHeight()
	if budget < 3 {
		return nil
	}
	maxRows := max(1, min(todoBlockMaxRows, budget-2))
	return (TodoBlock{Items: m.todos, MaxRows: maxRows}).RenderWidth(m.width)
}

func (m *Model) refreshSlashMatches() tea.Cmd {
	if m.slashCommandsProvider == nil {
		m.updateSlashMatches()
		return nil
	}
	provider := m.slashCommandsProvider
	return func() tea.Msg {
		return slashCommandsLoadedMsg{commands: provider()}
	}
}

func (m *Model) updateSlashMatches() {
	matches := matchSlashCommands(m.input.Value, m.slashCommands)
	if len(matches) == 0 {
		m.clearSlashMatches()
		m.clampScroll()
		return
	}
	if m.slashIndex >= len(matches) {
		m.slashIndex = len(matches) - 1
	}
	m.slashMatches = matches
	m.clampScroll()
}

func (m *Model) clearSlashMatches() {
	m.slashMatches = nil
	m.slashIndex = 0
}

func (m *Model) completeSlashMatch() bool {
	if len(m.slashMatches) == 0 {
		return false
	}
	chosen := m.slashMatches[clamp(m.slashIndex, 0, len(m.slashMatches)-1)]
	m.input.Value = chosen.Name + " "
	m.input.Cursor = m.input.Len()
	m.clearSlashMatches()
	return true
}

func (m *Model) shouldArrowKeyScroll() bool {
	return m.arrowKeysScroll && strings.TrimSpace(m.input.ExpandedValue()) == "" && len(m.inputHistory) == 0
}

func (m *Model) appendInputHistory(line string) tea.Cmd {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	m.inputHistory = append(m.inputHistory, line)
	m.inputHistory = trimInputHistory(m.inputHistory)
	m.historyIdx = len(m.inputHistory)
	m.historyHold = ""
	return m.emitIntent(InputHistoryChangedIntent{History: append([]string(nil), m.inputHistory...)})
}

func (m *Model) navigateInputHistory(delta int) tea.Cmd {
	if len(m.inputHistory) == 0 {
		return nil
	}
	if m.historyIdx == len(m.inputHistory) {
		m.historyHold = m.input.ExpandedValue()
	}
	next := clamp(m.historyIdx+delta, 0, len(m.inputHistory))
	m.historyIdx = next
	if m.historyIdx == len(m.inputHistory) {
		m.input.Value = m.historyHold
	} else {
		m.input.Value = m.inputHistory[m.historyIdx]
		m.input.Pastes = nil
	}
	m.input.Cursor = m.input.Len()
	return m.refreshSlashMatches()
}

func (m *Model) clearHistorySelection() {
	m.historyIdx = len(m.inputHistory)
	m.historyHold = ""
}

func (m *Model) inputHistoryHint() string {
	if len(m.inputHistory) == 0 || m.historyIdx < 0 || m.historyIdx >= len(m.inputHistory) {
		return ""
	}
	return fmt.Sprintf("History %d/%d", m.historyIdx+1, len(m.inputHistory))
}

func normalizeSlashCommands(commands []SlashCommand) []SlashCommand {
	out := make([]SlashCommand, 0, len(commands))
	seen := map[string]struct{}{}
	for _, cmd := range commands {
		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, SlashCommand{Name: name, Description: strings.TrimSpace(cmd.Description)})
	}
	return out
}

func normalizeInputHistory(history []string) []string {
	out := make([]string, 0, min(len(history), maxInputHistory))
	for _, item := range history {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return trimInputHistory(out)
}

func trimInputHistory(history []string) []string {
	if len(history) <= maxInputHistory {
		return history
	}
	return append([]string(nil), history[len(history)-maxInputHistory:]...)
}

func cleanInfoCardLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (m *Model) approvalPanelLines() []string {
	if m.approval == nil {
		return nil
	}
	width := max(1, m.width)
	innerWidth := max(1, width-2)
	req := m.approval.request
	title := strings.TrimSpace(req.Summary)
	if title == "" {
		title = "Tool approval"
	}
	if req.ToolName != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(req.ToolName)) {
		title += ": " + req.ToolName
	}

	var body []string
	body = append(body, botStyle.Render("Approval required")+" "+dimStyle.Render("for "+toolDisplayName(req.ToolName)))
	detail := strings.TrimSpace(req.Detail)
	if detail != "" {
		body = append(body, dimStyle.Render(toolDisplayName(req.ToolName)+" command:"))
		body = append(body, approvalDetailLines(detail, innerWidth-2, m.maxApprovalDetailLines())...)
	}
	body = append(body, "")
	body = append(body, ApprovalBlock{Request: req}.ActionsLine())

	for i, line := range body {
		if i == 0 && title != "" {
			body[i] = line + dimStyle.Render(" · "+title)
		}
	}
	lines := CardBlock{Lines: body, BorderStyle: approvalBorderStyle}.RenderWidth(width)
	budget := m.approvalPanelBudget()
	if budget <= 0 {
		return nil
	}
	if len(lines) <= budget {
		return lines
	}
	return compactApprovalPanelLines(req, width, budget)
}

func (m *Model) humanPromptPanelLines() []string {
	if m.humanText != nil {
		return m.humanTextPanelLines()
	}
	if m.humanPrompt == nil {
		return nil
	}
	width := max(1, m.width)
	innerWidth := max(1, width-4)
	req := m.humanPrompt.request
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Input required"
	}
	var body []string
	body = append(body, botStyle.Render("Human input required")+" "+dimStyle.Render(title))
	if text := strings.TrimSpace(req.Body); text != "" {
		body = append(body, limitedWrappedLines(text, innerWidth, m.maxApprovalDetailLines())...)
	}
	if len(req.Options) > 0 {
		body = append(body, "")
		body = append(body, humanPromptOptionLines(req, m.humanPrompt.selected, innerWidth)...)
		body = append(body, "")
		body = append(body, humanPromptActionsLine(req))
	}
	lines := CardBlock{Lines: body, BorderStyle: approvalBorderStyle}.RenderWidth(width)
	budget := m.humanPromptPanelBudget()
	if budget <= 0 {
		return nil
	}
	if len(lines) <= budget {
		return lines
	}
	if budget < 3 {
		return nil
	}
	bodyCap := max(1, budget-2)
	compact := []string{botStyle.Render("Human input required") + " " + dimStyle.Render(ansi.Truncate(title, max(1, innerWidth), "…"))}
	if len(req.Options) > 0 && bodyCap >= 2 {
		options := humanPromptOptionLines(req, m.humanPrompt.selected, innerWidth)
		if len(options) > 0 {
			compact = append(compact, options[0])
		}
		compact = append(compact, humanPromptActionsLine(req))
	}
	for len(compact) > bodyCap {
		compact = compact[:len(compact)-1]
	}
	return CardBlock{Lines: compact, BorderStyle: approvalBorderStyle}.RenderWidth(width)
}

func (m *Model) humanPromptPanelBudget() int {
	return m.height - m.bottomFixedRows() - m.minViewportRows() - m.approvalPanelHeight()
}

func (m *Model) humanTextPanelLines() []string {
	if m.humanText == nil {
		return nil
	}
	width := max(1, m.width)
	innerWidth := max(1, width-4)
	req := m.humanText.request
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Input required"
	}
	value := m.humanText.input.ExpandedValue()
	display := value
	if req.Secret && display != "" {
		display = strings.Repeat("*", len([]rune(display)))
	}
	if strings.TrimSpace(display) == "" {
		display = dimStyle.Render(fallbackString(req.Placeholder, "type value"))
	}
	var body []string
	body = append(body, botStyle.Render("Human input required")+" "+dimStyle.Render(title))
	if text := strings.TrimSpace(req.Body); text != "" {
		body = append(body, limitedWrappedLines(text, innerWidth, m.maxApprovalDetailLines())...)
	}
	body = append(body, "")
	body = append(body, fitLine("  "+display, innerWidth))
	body = append(body, "")
	body = append(body, dimStyle.Render("[enter] save  [esc] cancel"))
	lines := CardBlock{Lines: body, BorderStyle: approvalBorderStyle}.RenderWidth(width)
	budget := m.humanPromptPanelBudget()
	if budget <= 0 {
		return nil
	}
	if len(lines) <= budget {
		return lines
	}
	if budget < 3 {
		return nil
	}
	compact := []string{
		botStyle.Render("Human input required") + " " + dimStyle.Render(ansi.Truncate(title, max(1, innerWidth), "…")),
		fitLine("  "+display, innerWidth),
		dimStyle.Render("[enter] save  [esc] cancel"),
	}
	for len(compact) > max(1, budget-2) {
		compact = compact[:len(compact)-1]
	}
	return CardBlock{Lines: compact, BorderStyle: approvalBorderStyle}.RenderWidth(width)
}

func humanPromptActionsLine(req HumanPromptRequest) string {
	if len(req.Options) == 0 {
		return dimStyle.Render("[enter] continue  [esc] cancel")
	}
	return dimStyle.Render("[up/down] select  [enter] choose  [1-9] quick  [esc] cancel")
}

func humanPromptOptionLines(req HumanPromptRequest, selected int, width int) []string {
	if len(req.Options) == 0 {
		return nil
	}
	lines := make([]string, 0, len(req.Options))
	for i, option := range req.Options {
		if i >= 9 {
			break
		}
		label := strings.TrimSpace(option.Label)
		if label == "" {
			label = strings.TrimSpace(option.Value)
		}
		if label == "" {
			label = fmt.Sprintf("Option %d", i+1)
		}
		prefix := fmt.Sprintf("  %d. ", i+1)
		if i == selected {
			prefix = "> " + strconv.Itoa(i+1) + ". "
		}
		line := ansi.Truncate(prefix+label, max(1, width), "…")
		if i == selected {
			line = selStyle.Render(line)
		} else {
			line = dimStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func normalizeHumanPromptRequest(req HumanPromptRequest) HumanPromptRequest {
	if req.DefaultIndex < 0 || req.DefaultIndex >= len(req.Options) {
		req.DefaultIndex = -1
	}
	for i := range req.Options {
		req.Options[i].Label = strings.TrimSpace(req.Options[i].Label)
		req.Options[i].Value = strings.TrimSpace(req.Options[i].Value)
		if req.Options[i].Value == "" {
			req.Options[i].Value = req.Options[i].Label
		}
		if req.Options[i].Label == "" {
			req.Options[i].Label = req.Options[i].Value
		}
	}
	return req
}

func (m *Model) maxApprovalDetailLines() int {
	return max(1, min(4, m.height-7))
}

func (m *Model) approvalPanelBudget() int {
	return m.height - m.bottomFixedRows() - m.minViewportRows()
}

func compactApprovalPanelLines(req ToolApprovalRequest, width int, budget int) []string {
	if budget < 3 {
		return nil
	}
	bodyCap := max(1, budget-2)
	body := []string{
		botStyle.Render("Approval required") + " " + dimStyle.Render("for "+toolDisplayName(req.ToolName)),
	}
	actions := ApprovalBlock{Request: req}.ActionsLine()
	detail := strings.TrimSpace(req.Detail)
	if bodyCap >= 3 && detail != "" {
		body = append(body, dimStyle.Render(ansi.Truncate(detail, max(1, width-4), "…")))
	}
	if bodyCap >= 4 {
		body = append(body, "")
	}
	if bodyCap >= 2 {
		body = append(body, actions)
	}
	for len(body) > bodyCap {
		body = body[:len(body)-1]
	}
	return CardBlock{Lines: body, BorderStyle: approvalBorderStyle}.RenderWidth(width)
}

func limitedWrappedLines(text string, width int, limit int) []string {
	width = max(1, width)
	limit = max(1, limit)
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		wrapped := ansi.Wrap(raw, width, "")
		if wrapped == "" {
			wrapped = "\n"
		}
		for _, line := range strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n") {
			out = append(out, "  "+line)
		}
	}
	if len(out) > limit {
		out = out[:limit]
		out[len(out)-1] = ansi.Truncate(out[len(out)-1], width+2, "…")
	}
	return out
}

func approvalDetailLines(text string, width int, limit int) []string {
	lines := limitedWrappedLines(text, width, limit)
	for i, line := range lines {
		lines[i] = toolExpandedLine(width+2, strings.TrimPrefix(line, "  "))
	}
	return lines
}

func normalizeHumanTextRequest(req HumanTextRequest) HumanTextRequest {
	req.ID = strings.TrimSpace(req.ID)
	req.Title = strings.TrimSpace(req.Title)
	req.Body = strings.TrimSpace(req.Body)
	req.Placeholder = strings.TrimSpace(req.Placeholder)
	req.Default = normalizeInputText(req.Default)
	return req
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

// preserveRowSelection keeps the cursor on the same logical row across a
// panel refresh even when the row's position shifts (e.g. a cockpit list
// re-sorted by last-updated time). It matches by PanelRow.Value, which
// callers use as a stable row identity (run ID, phase title, agent ID),
// falling back to clamping the old index when the row can't be found.
func preserveRowSelection(oldRows []PanelRow, oldSelected int, newRows []PanelRow) int {
	if oldSelected >= 0 && oldSelected < len(oldRows) {
		if key := oldRows[oldSelected].Value; key != "" {
			for i, row := range newRows {
				if row.Value == key {
					return i
				}
			}
		}
	}
	return clamp(oldSelected, 0, max(0, len(newRows)-1))
}

func normalizePanel(panel Panel) Panel {
	panel.ID = strings.TrimSpace(panel.ID)
	panel.Title = strings.TrimSpace(panel.Title)
	panel.Subtitle = strings.TrimSpace(panel.Subtitle)
	panel.Footer = strings.TrimSpace(panel.Footer)
	panel.Lines = append([]string(nil), panel.Lines...)
	panel.Rows = append([]PanelRow(nil), panel.Rows...)
	panel.Shortcuts = append([]PanelShortcut(nil), panel.Shortcuts...)
	for i := range panel.Rows {
		panel.Rows[i].Label = strings.TrimSpace(panel.Rows[i].Label)
		panel.Rows[i].Detail = strings.TrimSpace(panel.Rows[i].Detail)
		panel.Rows[i].Value = strings.TrimSpace(panel.Rows[i].Value)
		panel.Rows[i].Command = strings.TrimSpace(panel.Rows[i].Command)
		panel.Rows[i].Action.ID = strings.TrimSpace(panel.Rows[i].Action.ID)
	}
	for i := range panel.Shortcuts {
		panel.Shortcuts[i].Key = strings.TrimSpace(panel.Shortcuts[i].Key)
		panel.Shortcuts[i].Label = strings.TrimSpace(panel.Shortcuts[i].Label)
		panel.Shortcuts[i].Command = strings.TrimSpace(panel.Shortcuts[i].Command)
		panel.Shortcuts[i].Action.ID = strings.TrimSpace(panel.Shortcuts[i].Action.ID)
	}
	panel.Selected = clamp(panel.Selected, 0, max(0, len(panel.Rows)-1))
	return panel
}

func (m Model) handlePanelKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlCKey(msg) || isEscKey(msg) || strings.EqualFold(strings.TrimSpace(msg.Text), "q") {
		panelID := ""
		if m.panel != nil {
			panelID = m.panel.ID
		}
		m.closePanel()
		m.rebuild()
		return m, m.emitIntent(PanelClosedIntent{PanelID: panelID})
	}
	if m.panel != nil && len(m.panel.Shortcuts) > 0 {
		if shortcut, ok := panelShortcutForKey(m.panel.Shortcuts, msg); ok {
			action := PanelAction{
				PanelID: m.panel.ID,
				Index:   -1,
				Row: PanelRow{
					Label:   shortcut.Label,
					Command: shortcut.Command,
					Action:  shortcut.Action,
				},
				Command: strings.TrimSpace(shortcut.Command),
				Action:  shortcut.Action,
			}
			// Leave the panel showing until the host replaces or clears it via
			// panel updates — closing eagerly here forces a render
			// with no panel in between, which flickers on every navigation.
			intent := PanelActionIntent{Action: action}
			if m.handlesIntent(intent) {
				return m, m.emitIntent(intent)
			} else {
				m.closePanel()
				m.rebuild()
			}
			return m, nil
		}
	}
	if m.panel != nil && len(m.panel.Rows) > 0 {
		switch {
		case msg.Code == tea.KeyUp || strings.TrimSpace(msg.Text) == "k":
			m.panelSelected = (m.panelSelected - 1 + len(m.panel.Rows)) % len(m.panel.Rows)
			m.ensurePanelSelectionVisible()
			m.rebuild()
			return m, nil
		case msg.Code == tea.KeyDown || strings.TrimSpace(msg.Text) == "j":
			m.panelSelected = (m.panelSelected + 1) % len(m.panel.Rows)
			m.ensurePanelSelectionVisible()
			m.rebuild()
			return m, nil
		case msg.Code == tea.KeyEnter:
			row := m.panel.Rows[clamp(m.panelSelected, 0, len(m.panel.Rows)-1)]
			action := PanelAction{
				PanelID: m.panel.ID,
				Index:   m.panelSelected,
				Row:     row,
				Command: strings.TrimSpace(row.Command),
				Action:  row.Action,
			}
			// See the shortcut case above: don't close eagerly, or every
			// drill-down flickers through a no-panel frame.
			intent := PanelActionIntent{Action: action}
			if m.handlesIntent(intent) {
				return m, m.emitIntent(intent)
			} else {
				m.closePanel()
				m.rebuild()
			}
			return m, nil
		}
	}
	switch {
	case msg.Code == tea.KeyUp || strings.TrimSpace(msg.Text) == "k":
		m.scroll(-1)
	case msg.Code == tea.KeyDown || strings.TrimSpace(msg.Text) == "j":
		m.scroll(1)
	case msg.Code == tea.KeyPgUp:
		m.scroll(-max(1, m.vpHeight()-1))
	case msg.Code == tea.KeyPgDown:
		m.scroll(max(1, m.vpHeight()-1))
	case msg.Code == tea.KeyHome:
		m.panelOffset = 0
	case msg.Code == tea.KeyEnd || msg.String() == "ctrl+end":
		m.panelOffset = m.maxOffset()
	}
	m.rebuild()
	return m, nil
}

func panelShortcutForKey(shortcuts []PanelShortcut, msg tea.KeyPressMsg) (PanelShortcut, bool) {
	text := strings.TrimSpace(msg.Text)
	if text == "" || msg.Mod != 0 {
		return PanelShortcut{}, false
	}
	for _, shortcut := range shortcuts {
		if strings.EqualFold(strings.TrimSpace(shortcut.Key), text) &&
			(strings.TrimSpace(shortcut.Command) != "" || strings.TrimSpace(shortcut.Action.ID) != "") {
			return shortcut, true
		}
	}
	return PanelShortcut{}, false
}

func (m *Model) closePanel() {
	m.overlayModel.closePanel("")
}

func (m *Model) ensurePanelSelectionVisible() {
	if m.panel == nil || len(m.panelRowLines) == 0 || m.panelSelected < 0 || m.panelSelected >= len(m.panelRowLines) {
		return
	}
	line := m.panelRowLines[m.panelSelected]
	if line < m.panelOffset {
		m.panelOffset = line
		return
	}
	bottom := m.panelOffset + max(1, m.vpHeight()) - 1
	if line > bottom {
		m.panelOffset = line - max(1, m.vpHeight()) + 1
	}
	m.panelOffset = clamp(m.panelOffset, 0, m.maxOffset())
}

func (m Model) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlCKey(msg) {
		return m, tea.Quit
	}
	if isEscKey(msg) {
		return m.resolveApproval(ToolApprovalDeny)
	}
	switch msg.String() {
	case "y", "Y":
		return m.resolveApproval(ToolApprovalAllow)
	case "a", "A":
		return m.resolveApproval(ToolApprovalAllowAlways)
	case "n", "N":
		return m.resolveApproval(ToolApprovalDeny)
	case "d", "D":
		return m.resolveApproval(ToolApprovalDenyAlways)
	}
	return m, nil
}

func (m Model) handleHumanPromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlCKey(msg) {
		return m, tea.Quit
	}
	if isEscKey(msg) {
		return m.resolveHumanPrompt(m.defaultHumanPromptValue())
	}
	if m.humanPrompt != nil && len(m.humanPrompt.request.Options) > 0 {
		switch msg.String() {
		case "up":
			return m.moveHumanPromptSelection(-1), nil
		case "down":
			return m.moveHumanPromptSelection(1), nil
		}
		switch strings.TrimSpace(msg.Text) {
		case "k":
			return m.moveHumanPromptSelection(-1), nil
		case "j":
			return m.moveHumanPromptSelection(1), nil
		}
	}
	if msg.Code == tea.KeyEnter {
		return m.resolveHumanPrompt(m.selectedHumanPromptValue())
	}
	if idx, ok := humanPromptOptionIndex(msg); ok && m.humanPrompt != nil {
		options := m.humanPrompt.request.Options
		if idx >= 0 && idx < len(options) {
			return m.resolveHumanPrompt(options[idx].Value)
		}
	}
	return m, nil
}

func (m Model) moveHumanPromptSelection(delta int) Model {
	if m.humanPrompt == nil || len(m.humanPrompt.request.Options) == 0 {
		return m
	}
	n := min(len(m.humanPrompt.request.Options), 9)
	if n <= 0 {
		return m
	}
	selected := m.humanPrompt.selected
	if selected < 0 || selected >= n {
		selected = 0
	}
	m.humanPrompt.selected = (selected + delta + n) % n
	m.rebuild()
	return m
}

func (m Model) handleHumanTextKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.humanText == nil {
		return m, nil
	}
	if isCtrlCKey(msg) {
		return m, tea.Quit
	}
	if isEscKey(msg) {
		return m.resolveHumanText("")
	}
	switch {
	case msg.Code == tea.KeyEnter:
		value := m.humanText.input.ExpandedValue()
		if m.humanText.request.Required && strings.TrimSpace(value) == "" {
			m.status = "input required"
			m.rebuild()
			return m, nil
		}
		return m.resolveHumanText(value)
	case msg.Code == tea.KeyLeft, msg.String() == "ctrl+b":
		m.humanText.input.MoveLeft()
	case msg.Code == tea.KeyRight, msg.String() == "ctrl+f":
		m.humanText.input.MoveRight()
	case msg.Code == tea.KeyHome, msg.String() == "ctrl+a":
		m.humanText.input.MoveHome()
	case msg.Code == tea.KeyEnd, msg.String() == "ctrl+e":
		m.humanText.input.MoveEnd()
	case msg.Code == tea.KeyBackspace, msg.String() == "ctrl+h":
		m.humanText.input.Backspace()
	case isCtrlWKey(msg):
		m.humanText.input.DeleteWordBackward()
	case msg.Code == tea.KeyDelete:
		m.humanText.input.DeleteForward()
	case msg.Text != "":
		m.humanText.input.Insert(msg.Text)
	}
	m.rebuild()
	return m, nil
}

func (m Model) resolveHumanText(value string) (tea.Model, tea.Cmd) {
	if m.humanText == nil {
		return m, nil
	}
	prompt := m.humanText
	m.humanText = nil
	if strings.TrimSpace(value) == "" {
		m.status = "input cancelled"
	} else {
		m.status = "input received"
	}
	m.rebuild()
	return m, responseCmd(prompt.respond, value)
}

func humanPromptOptionIndex(msg tea.KeyPressMsg) (int, bool) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.String())
	}
	if len(text) != 1 || text[0] < '1' || text[0] > '9' {
		return 0, false
	}
	return int(text[0] - '1'), true
}

func (m Model) defaultHumanPromptValue() string {
	if m.humanPrompt == nil {
		return ""
	}
	req := m.humanPrompt.request
	if req.DefaultIndex >= 0 && req.DefaultIndex < len(req.Options) {
		return req.Options[req.DefaultIndex].Value
	}
	return ""
}

func (m Model) selectedHumanPromptValue() string {
	if m.humanPrompt == nil {
		return ""
	}
	req := m.humanPrompt.request
	idx := m.humanPrompt.selected
	if idx >= 0 && idx < len(req.Options) {
		return req.Options[idx].Value
	}
	return m.defaultHumanPromptValue()
}

func isCtrlCKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	return msg.String() == "ctrl+c" || key.Text == "\x03" || (key.Code == 'c' && key.Mod.Contains(tea.ModCtrl))
}

func isCtrlWKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	return msg.String() == "ctrl+w" || key.Text == "\x17" || (key.Code == 'w' && key.Mod.Contains(tea.ModCtrl))
}

func isCtrlVKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	return msg.String() == "ctrl+v" || key.Text == "\x16" || (key.Code == 'v' && key.Mod.Contains(tea.ModCtrl))
}

func isEscKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	return msg.String() == "esc" || key.Text == "\x1b" || key.Code == tea.KeyEsc || key.Code == tea.KeyEscape || (key.Code == '[' && key.Mod.Contains(tea.ModCtrl))
}

func (m Model) resolveApproval(decision ToolApprovalDecision) (tea.Model, tea.Cmd) {
	if m.approval == nil {
		return m, nil
	}
	approval := m.approval
	result := ToolApprovalResult{Request: approval.request, Decision: decision}
	m.status = "approval: " + string(decision)
	m.approval = nil
	m.rebuild()
	return m, batchCmds(
		responseCmd(approval.respond, decision),
		m.emitIntent(ToolApprovalDecisionIntent{Result: result}),
	)
}

func (m Model) resolveHumanPrompt(value string) (tea.Model, tea.Cmd) {
	if m.humanPrompt == nil {
		return m, nil
	}
	prompt := m.humanPrompt
	if value == "" {
		m.status = "input cancelled"
	} else {
		m.status = "input received"
	}
	m.humanPrompt = nil
	m.rebuild()
	return m, responseCmd(prompt.respond, value)
}

func (m *Model) startStream() {
	m.streaming = true
	m.streamRunes = []rune(m.streamReply)
	m.streamIdx = 0
}

func (m *Model) finishStream() {
	m.streaming = false
	m.todosCurrent = false
	m.entries = append(m.entries, Entry{
		Role:  RoleAssistant,
		Nodes: []Node{MarkdownNode{Text: m.streamReply}},
	})
	m.streamRunes, m.streamIdx = nil, 0
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return streamTickMsg{} })
}
