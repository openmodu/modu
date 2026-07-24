package modutui

import "time"

// transcriptModel owns the conversation viewport and its rendered/copyable
// representation. It does not own input, overlays, or host callbacks.
type transcriptModel struct {
	entries []Entry
	lines   []string
	gutters []int
	headers map[int]int
	yOffset int
	follow  bool
	unseen  int

	selecting        bool
	selStart, selEnd cell
	dragCol          int
	autoScroll       int
	autoScrolling    bool
	autoScrollTicks  int

	infoCardLines       []string
	blockFactories      []EntryBlockFactory
	blockGap            int
	toolArtifactCache   map[string]toolArtifactCacheEntry
	toolArtifactLoading map[string]bool
	loadToolArtifact    func(string) (string, error)
}

// composerModel owns editable input and command completion state.
type composerModel struct {
	input        InputBlock
	inputHistory []string
	historyIdx   int
	historyHold  string
	imeTail      string
	imeActive    bool

	arrowKeysScroll       bool
	slashCommands         []SlashCommand
	slashCommandsProvider func() []SlashCommand
	slashMatches          []SlashCommand
	slashIndex            int
}

// overlayModel owns the single focused surface above the normal composer.
// All transitions are centralized here so only one overlay can be active.
type overlayModel struct {
	panel         *Panel
	panelLines    []string
	panelRowLines []int
	panelOffset   int
	panelSelected int
	approval      *pendingApproval
	humanPrompt   *pendingHumanPrompt
	humanText     *pendingHumanText
}

// chromeModel owns fixed status/footer/todo state and simulated streaming.
type chromeModel struct {
	streaming   bool
	streamRunes []rune
	streamIdx   int
	streamReply string
	busy        bool

	todos        []TodoItem
	todosCurrent bool

	status            string
	statusExpiresAt   time.Time
	statusExpiresText string
	statusHint        string
	footer            string
}
