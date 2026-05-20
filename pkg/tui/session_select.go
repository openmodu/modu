package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	gotui "github.com/grindlemire/go-tui"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	sessionpkg "github.com/openmodu/modu/pkg/coding_agent/session"
)

const sessionSelectVisibleRows = 10

func (r *goTUIRoot) openSessionSelect(all bool) {
	if r.session == nil {
		r.model.statusMsg = "no session"
		r.bump()
		return
	}
	r.sessionAllScope = all
	r.sessionSortMode = "threaded"
	r.sessionNamedOnly = false
	r.sessionShowPath = false
	r.sessionConfirmDelete = ""
	r.sessionRenameMode = false
	r.sessionRenameText = ""
	r.sessionSearch = ""
	if err := r.loadSessionChoices(); err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	if len(r.sessionAllChoices) == 0 {
		r.model.statusMsg = "no sessions found"
		r.bump()
		return
	}
	r.filterSessionChoices()
	r.sessionSelectIdx = currentSessionChoiceIndex(r.sessionChoices, r.session.GetSessionFile())
	r.adjustSessionSelectScroll()
	r.model.state = uiStateSessionSelect
	r.model.statusMsg = ""
	r.slashMatches = nil
	r.bump()
}

func (r *goTUIRoot) loadSessionChoices() error {
	var (
		choices []coding_agent.SessionInfo
		err     error
	)
	if r.sessionAllScope {
		choices, err = r.session.ListAllSessionInfos()
	} else {
		choices, err = r.session.ListSessionInfos()
	}
	if err != nil {
		return err
	}
	r.sessionAllChoices = choices
	return nil
}

func (r *goTUIRoot) filterSessionChoices() {
	choices := append([]coding_agent.SessionInfo(nil), r.sessionAllChoices...)
	if r.sessionNamedOnly {
		filtered := choices[:0]
		for _, choice := range choices {
			if strings.TrimSpace(choice.Name) != "" {
				filtered = append(filtered, choice)
			}
		}
		choices = filtered
	}
	query := strings.TrimSpace(r.sessionSearch)
	if query != "" {
		filtered := make([]coding_agent.SessionInfo, 0, len(choices))
		for _, choice := range choices {
			if sessionMatchesQuery(choice, query) {
				filtered = append(filtered, choice)
			}
		}
		choices = filtered
	}
	sortSessionChoices(choices, r.sessionSortMode, query)
	r.sessionChoices = choices
	if r.sessionSelectIdx >= len(r.sessionChoices) {
		r.sessionSelectIdx = max(0, len(r.sessionChoices)-1)
	}
	r.adjustSessionSelectScroll()
}

func sessionMatchesQuery(info coding_agent.SessionInfo, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		info.Name,
		info.FirstMessage,
		info.AllMessagesText,
		info.Cwd,
		info.Path,
	}, " "))
	if strings.HasPrefix(query, "re:") {
		re, err := regexp.Compile("(?i)" + strings.TrimPrefix(query, "re:"))
		return err == nil && re.MatchString(haystack)
	}
	if strings.HasPrefix(query, "\"") && strings.HasSuffix(query, "\"") && len(query) >= 2 {
		return strings.Contains(haystack, strings.ToLower(strings.Trim(query, "\"")))
	}
	for _, part := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, part) {
			return false
		}
	}
	return true
}

func sortSessionChoices(choices []coding_agent.SessionInfo, mode, query string) {
	if mode == "relevance" && strings.TrimSpace(query) != "" {
		q := strings.ToLower(strings.Trim(query, "\""))
		sort.SliceStable(choices, func(i, j int) bool {
			return sessionRelevance(choices[i], q) < sessionRelevance(choices[j], q)
		})
		return
	}
	if mode == "threaded" {
		sort.SliceStable(choices, func(i, j int) bool {
			ip, jp := choices[i].ParentSession, choices[j].ParentSession
			if ip == "" && jp != "" {
				return true
			}
			if ip != "" && jp == "" {
				return false
			}
			return choices[i].Modified.After(choices[j].Modified)
		})
		return
	}
	sort.SliceStable(choices, func(i, j int) bool {
		return choices[i].Modified.After(choices[j].Modified)
	})
}

func sessionRelevance(info coding_agent.SessionInfo, query string) int {
	if query == "" {
		return 0
	}
	haystacks := []string{info.Name, info.FirstMessage, info.AllMessagesText, info.Cwd, info.Path}
	best := 1_000_000
	for _, haystack := range haystacks {
		if idx := strings.Index(strings.ToLower(haystack), query); idx >= 0 && idx < best {
			best = idx
		}
	}
	return best
}

func currentSessionChoiceIndex(choices []coding_agent.SessionInfo, current string) int {
	currentAbs, _ := filepath.Abs(current)
	for i, choice := range choices {
		choiceAbs, _ := filepath.Abs(choice.Path)
		if choice.Path == current || (currentAbs != "" && choiceAbs == currentAbs) {
			return i
		}
	}
	return 0
}

func (r *goTUIRoot) sessionSelectKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.closeSessionSelect("session unchanged") }),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.cancelSessionSelectMode() }),
		gotui.OnStop(gotui.KeyUp, func(ke gotui.KeyEvent) { r.moveSessionSelect(-1) }),
		gotui.OnStop(gotui.KeyDown, func(ke gotui.KeyEvent) { r.moveSessionSelect(1) }),
		gotui.OnStop(gotui.KeyPageUp, func(ke gotui.KeyEvent) { r.moveSessionSelect(-sessionSelectVisibleRows) }),
		gotui.OnStop(gotui.KeyPageDown, func(ke gotui.KeyEvent) { r.moveSessionSelect(sessionSelectVisibleRows) }),
		gotui.OnStop(gotui.KeyHome, func(ke gotui.KeyEvent) { r.jumpSessionSelect(0) }),
		gotui.OnStop(gotui.KeyEnd, func(ke gotui.KeyEvent) { r.jumpSessionSelect(len(r.sessionChoices) - 1) }),
		gotui.OnStop(gotui.KeyBackspace, func(ke gotui.KeyEvent) { r.backspaceSessionText() }),
		gotui.OnStop(gotui.KeyTab, func(ke gotui.KeyEvent) { r.toggleSessionScope() }),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) { r.confirmSessionSelectAction() }),
		gotui.OnStop(gotui.KeyCtrlF, func(ke gotui.KeyEvent) { r.forkSessionSelect() }),
		gotui.OnStop(gotui.KeyCtrlD, func(ke gotui.KeyEvent) { r.startDeleteSessionSelect() }),
		gotui.OnStop(gotui.KeyCtrlN, func(ke gotui.KeyEvent) { r.toggleSessionNamedFilter() }),
		gotui.OnStop(gotui.KeyCtrlP, func(ke gotui.KeyEvent) { r.toggleSessionPath() }),
		gotui.OnStop(gotui.KeyCtrlS, func(ke gotui.KeyEvent) { r.toggleSessionSort() }),
		gotui.OnStop(gotui.KeyCtrlE, func(ke gotui.KeyEvent) { r.startSessionRename() }),
		gotui.OnStop(gotui.AnyRune, func(ke gotui.KeyEvent) { r.appendSessionText(ke.Rune) }),
	}
}

func (r *goTUIRoot) moveSessionSelect(delta int) {
	if r.sessionRenameMode || r.sessionConfirmDelete != "" || len(r.sessionChoices) == 0 {
		return
	}
	r.sessionSelectIdx = clampInt(r.sessionSelectIdx+delta, 0, len(r.sessionChoices)-1)
	r.adjustSessionSelectScroll()
	r.bump()
}

func (r *goTUIRoot) jumpSessionSelect(idx int) {
	if r.sessionRenameMode || r.sessionConfirmDelete != "" || len(r.sessionChoices) == 0 {
		return
	}
	r.sessionSelectIdx = clampInt(idx, 0, len(r.sessionChoices)-1)
	r.adjustSessionSelectScroll()
	r.bump()
}

func (r *goTUIRoot) appendSessionText(ch rune) {
	if ch == 0 || r.sessionConfirmDelete != "" {
		return
	}
	if r.sessionRenameMode {
		r.sessionRenameText += string(ch)
	} else {
		r.sessionSearch += string(ch)
		r.sessionSelectIdx = 0
		r.filterSessionChoices()
	}
	r.bump()
}

func (r *goTUIRoot) backspaceSessionText() {
	if r.sessionConfirmDelete != "" {
		return
	}
	if r.sessionRenameMode {
		rs := []rune(r.sessionRenameText)
		if len(rs) > 0 {
			r.sessionRenameText = string(rs[:len(rs)-1])
		}
	} else {
		rs := []rune(r.sessionSearch)
		if len(rs) > 0 {
			r.sessionSearch = string(rs[:len(rs)-1])
			r.sessionSelectIdx = 0
			r.filterSessionChoices()
		}
	}
	r.bump()
}

func (r *goTUIRoot) toggleSessionScope() {
	if r.sessionRenameMode || r.sessionConfirmDelete != "" {
		return
	}
	r.sessionAllScope = !r.sessionAllScope
	if err := r.loadSessionChoices(); err != nil {
		r.model.errMsg = err.Error()
		return
	}
	r.sessionSelectIdx = 0
	r.filterSessionChoices()
	r.bump()
}

func (r *goTUIRoot) toggleSessionSort() {
	if r.sessionRenameMode || r.sessionConfirmDelete != "" {
		return
	}
	switch r.sessionSortMode {
	case "threaded":
		r.sessionSortMode = "recent"
	case "recent":
		r.sessionSortMode = "relevance"
	default:
		r.sessionSortMode = "threaded"
	}
	r.filterSessionChoices()
	r.bump()
}

func (r *goTUIRoot) toggleSessionNamedFilter() {
	if r.sessionRenameMode || r.sessionConfirmDelete != "" {
		return
	}
	r.sessionNamedOnly = !r.sessionNamedOnly
	r.filterSessionChoices()
	r.bump()
}

func (r *goTUIRoot) toggleSessionPath() {
	if r.sessionRenameMode || r.sessionConfirmDelete != "" {
		return
	}
	r.sessionShowPath = !r.sessionShowPath
	r.bump()
}

func (r *goTUIRoot) adjustSessionSelectScroll() {
	if len(r.sessionChoices) <= sessionSelectVisibleRows {
		r.sessionSelectScroll = 0
		return
	}
	if r.sessionSelectIdx < r.sessionSelectScroll {
		r.sessionSelectScroll = r.sessionSelectIdx
	} else if r.sessionSelectIdx >= r.sessionSelectScroll+sessionSelectVisibleRows {
		r.sessionSelectScroll = r.sessionSelectIdx - sessionSelectVisibleRows + 1
	}
	if r.sessionSelectScroll < 0 {
		r.sessionSelectScroll = 0
	}
	if maxOffset := len(r.sessionChoices) - sessionSelectVisibleRows; r.sessionSelectScroll > maxOffset {
		r.sessionSelectScroll = maxOffset
	}
}

func (r *goTUIRoot) selectedSessionChoice() (coding_agent.SessionInfo, bool) {
	if len(r.sessionChoices) == 0 || r.sessionSelectIdx < 0 || r.sessionSelectIdx >= len(r.sessionChoices) {
		return coding_agent.SessionInfo{}, false
	}
	return r.sessionChoices[r.sessionSelectIdx], true
}

func (r *goTUIRoot) confirmSessionSelectAction() {
	switch {
	case r.sessionRenameMode:
		r.confirmSessionRename()
	case r.sessionConfirmDelete != "":
		r.deleteSessionSelect()
	default:
		r.confirmSessionSelect()
	}
}

func (r *goTUIRoot) confirmSessionSelect() {
	choice, ok := r.selectedSessionChoice()
	if r.session == nil || !ok || r.sessionRenameMode || r.sessionConfirmDelete != "" {
		return
	}
	if err := r.session.SwitchSession(choice.Path); err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	r.closeSessionSelect("resumed session")
}

func (r *goTUIRoot) forkSessionSelect() {
	choice, ok := r.selectedSessionChoice()
	if r.session == nil || !ok || r.sessionRenameMode || r.sessionConfirmDelete != "" {
		return
	}
	if err := r.session.ForkFromSession(choice.Path); err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	r.closeSessionSelect("forked session")
}

func (r *goTUIRoot) startDeleteSessionSelect() {
	choice, ok := r.selectedSessionChoice()
	if !ok || r.sessionRenameMode {
		return
	}
	if r.session != nil && sameSessionPath(choice.Path, r.session.GetSessionFile()) {
		r.model.errMsg = "cannot delete the active session"
		r.bump()
		return
	}
	r.sessionConfirmDelete = choice.Path
	r.bump()
}

func (r *goTUIRoot) deleteSessionSelect() {
	if r.session == nil || r.sessionConfirmDelete == "" {
		return
	}
	target := r.sessionConfirmDelete
	r.sessionConfirmDelete = ""
	if err := r.session.DeleteSession(target); err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	_ = r.loadSessionChoices()
	r.filterSessionChoices()
	r.model.statusMsg = "deleted session"
	r.bump()
}

func (r *goTUIRoot) startSessionRename() {
	choice, ok := r.selectedSessionChoice()
	if !ok || r.sessionConfirmDelete != "" {
		return
	}
	r.sessionRenameMode = true
	r.sessionRenameText = choice.Name
	r.bump()
}

func (r *goTUIRoot) confirmSessionRename() {
	choice, ok := r.selectedSessionChoice()
	if !ok || r.session == nil {
		return
	}
	next := strings.TrimSpace(r.sessionRenameText)
	if sameSessionPath(choice.Path, r.session.GetSessionFile()) {
		r.session.SetSessionName(next)
	} else if mgr, err := sessionpkg.NewManagerFromFile(choice.Path); err == nil {
		if err := mgr.AppendSessionInfo(next); err != nil {
			r.model.errMsg = err.Error()
			r.bump()
			return
		}
	} else {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	r.sessionRenameMode = false
	r.sessionRenameText = ""
	_ = r.loadSessionChoices()
	r.filterSessionChoices()
	r.model.statusMsg = "renamed session"
	r.bump()
}

func (r *goTUIRoot) cancelSessionSelectMode() {
	if r.sessionRenameMode {
		r.sessionRenameMode = false
		r.sessionRenameText = ""
		r.bump()
		return
	}
	if r.sessionConfirmDelete != "" {
		r.sessionConfirmDelete = ""
		r.bump()
		return
	}
	r.closeSessionSelect("session unchanged")
}

func (r *goTUIRoot) closeSessionSelect(status string) {
	r.model.state = uiStateInput
	r.sessionChoices = nil
	r.sessionAllChoices = nil
	r.sessionSelectIdx = 0
	r.sessionSelectScroll = 0
	r.sessionSearch = ""
	r.sessionAllScope = false
	r.sessionSortMode = ""
	r.sessionNamedOnly = false
	r.sessionShowPath = false
	r.sessionConfirmDelete = ""
	r.sessionRenameMode = false
	r.sessionRenameText = ""
	r.model.statusMsg = status
	r.bump()
}

func (r *goTUIRoot) renderSessionSelectWidget() *gotui.Element {
	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)
	scope := "current"
	if r.sessionAllScope {
		scope = "all"
	}
	filter := "all"
	if r.sessionNamedOnly {
		filter = "named"
	}
	mode := fmt.Sprintf("scope=%s sort=%s filter=%s", scope, r.sessionSortMode, filter)
	headerQuery := r.sessionSearch
	if r.sessionRenameMode || r.sessionConfirmDelete != "" {
		headerQuery = ""
	}
	container.AddChild(gotui.New(
		gotui.WithText(selectorHeaderLine(selectorHeaderOptions{
			Title:    "Select session",
			Selected: r.sessionSelectIdx,
			Visible:  len(r.sessionChoices),
			Total:    len(r.sessionAllChoices),
			Query:    headerQuery,
			Mode:     mode,
		})),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
		gotui.WithFlexShrink(0),
	))
	search := r.sessionSearch
	if search == "" {
		search = "type to search"
	}
	if r.sessionRenameMode {
		search = "rename: " + r.sessionRenameText
	} else if r.sessionConfirmDelete != "" {
		search = "delete selected session? enter confirm, esc cancel"
	} else {
		search = "search: " + search
	}
	container.AddChild(gotui.New(
		gotui.WithText("  "+search),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))

	end := r.sessionSelectScroll + sessionSelectVisibleRows
	if end > len(r.sessionChoices) {
		end = len(r.sessionChoices)
	}
	current := ""
	if r.session != nil {
		current = r.session.GetSessionFile()
	}
	if len(r.sessionChoices) == 0 {
		container.AddChild(gotui.New(
			gotui.WithText("  no matching sessions"),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
	}
	for i := r.sessionSelectScroll; i < end; i++ {
		choice := r.sessionChoices[i]
		selected := i == r.sessionSelectIdx
		active := sameSessionPath(choice.Path, current)
		confirming := choice.Path == r.sessionConfirmDelete
		line := sessionChoiceLine(choice, selected, active, confirming, r.sessionShowPath, r.sessionAllScope)
		if i == r.sessionSelectScroll && r.sessionSelectScroll > 0 {
			line += "  ↑"
		} else if i == end-1 && end < len(r.sessionChoices) {
			line += "  ↓"
		}
		style := gotui.NewStyle().Dim()
		if confirming {
			style = gotui.NewStyle().Foreground(gotui.Red).Bold()
		} else if selected {
			style = gotui.NewStyle().Foreground(gotui.Cyan).Bold()
		}
		container.AddChild(gotui.New(
			gotui.WithText(line),
			gotui.WithTextStyle(style),
			gotui.WithFlexShrink(0),
		))
	}
	container.AddChild(gotui.New(
		gotui.WithText("  ↑/↓ select  tab scope  ctrl+s sort  ctrl+n named  ctrl+p path  ctrl+e rename  ctrl+d delete"),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))
	return container
}

func sessionChoiceLine(
	info coding_agent.SessionInfo,
	selected bool,
	active bool,
	confirming bool,
	showPath bool,
	showCwd bool,
) string {
	prefix := "  "
	if selected {
		prefix = "❯ "
	}
	marker := " "
	if active {
		marker = "*"
	}
	label := strings.TrimSpace(info.Name)
	if label == "" {
		label = strings.TrimSpace(info.FirstMessage)
	}
	if label == "" {
		label = filepath.Base(info.Path)
	}
	if len(label) > 48 {
		label = label[:45] + "..."
	}
	right := fmt.Sprintf("messages=%d  %s", info.MessageCount, formatSessionAge(info.Modified))
	if showCwd && strings.TrimSpace(info.Cwd) != "" {
		right = filepath.Base(info.Cwd) + "  " + right
	}
	if showPath {
		right = shortenSessionPath(info.Path) + "  " + right
	}
	if confirming {
		right = "delete?  " + right
	}
	return fmt.Sprintf("%s%s %s  %s", prefix, marker, label, right)
}

func formatSessionAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	diff := time.Since(t)
	switch {
	case diff < time.Minute:
		return "now"
	case diff < time.Hour:
		return fmt.Sprintf("%dm", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh", int(diff.Hours()))
	case diff < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(diff.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func shortenSessionPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
	}
	return path
}

func sameSessionPath(a, b string) bool {
	if a == b {
		return true
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && aa == bb
}
