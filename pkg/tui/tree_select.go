package tui

import (
	"fmt"
	"strings"

	gotui "github.com/grindlemire/go-tui"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
)

const treeSelectVisibleRows = 12

func (r *goTUIRoot) openTreeSelect() {
	if r.session == nil {
		r.model.statusMsg = "no session"
		r.bump()
		return
	}
	nodes := r.session.GetSessionTreeNodes()
	if len(nodes) == 0 {
		r.model.statusMsg = "session tree: empty"
		r.bump()
		return
	}
	r.treeAllNodes = nodes
	r.treeSearch = ""
	r.treeShowSummary = false
	r.filterTreeNodes()
	r.treeSelectIdx = currentTreeNodeIndex(r.treeNodes)
	r.adjustTreeSelectScroll()
	r.model.state = uiStateTreeSelect
	r.slashMatches = nil
	r.model.statusMsg = ""
	r.bump()
}

func currentTreeNodeIndex(nodes []coding_agent.SessionTreeNode) int {
	for i, node := range nodes {
		if node.Current {
			return i
		}
	}
	return max(0, len(nodes)-1)
}

func (r *goTUIRoot) filterTreeNodes() {
	query := strings.ToLower(strings.TrimSpace(r.treeSearch))
	nodes := r.treeAllNodes
	if query != "" {
		filtered := make([]coding_agent.SessionTreeNode, 0, len(nodes))
		for _, node := range nodes {
			if treeNodeMatches(node, query) {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
	}
	r.treeNodes = nodes
	if r.treeSelectIdx >= len(r.treeNodes) {
		r.treeSelectIdx = max(0, len(r.treeNodes)-1)
	}
	r.adjustTreeSelectScroll()
}

func treeNodeMatches(node coding_agent.SessionTreeNode, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		node.ID,
		node.ParentID,
		node.Type,
		node.Role,
		node.Label,
		node.Preview,
	}, " "))
	for _, part := range strings.Fields(query) {
		if !strings.Contains(haystack, part) {
			return false
		}
	}
	return true
}

func (r *goTUIRoot) treeSelectKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.closeTreeSelect("tree closed") }),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.closeTreeSelect("tree closed") }),
		gotui.OnStop(gotui.KeyUp, func(ke gotui.KeyEvent) { r.moveTreeSelect(-1) }),
		gotui.OnStop(gotui.KeyDown, func(ke gotui.KeyEvent) { r.moveTreeSelect(1) }),
		gotui.OnStop(gotui.KeyPageUp, func(ke gotui.KeyEvent) { r.moveTreeSelect(-treeSelectVisibleRows) }),
		gotui.OnStop(gotui.KeyPageDown, func(ke gotui.KeyEvent) { r.moveTreeSelect(treeSelectVisibleRows) }),
		gotui.OnStop(gotui.KeyHome, func(ke gotui.KeyEvent) { r.jumpTreeSelect(0) }),
		gotui.OnStop(gotui.KeyEnd, func(ke gotui.KeyEvent) { r.jumpTreeSelect(len(r.treeNodes) - 1) }),
		gotui.OnStop(gotui.KeyBackspace, func(ke gotui.KeyEvent) { r.backspaceTreeSearch() }),
		gotui.OnStop(gotui.KeyCtrlF, func(ke gotui.KeyEvent) { r.branchTreeSelect() }),
		gotui.OnStop(gotui.KeyCtrlS, func(ke gotui.KeyEvent) { r.toggleTreeSummary() }),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) { r.confirmTreeSelect() }),
		gotui.OnStop(gotui.AnyRune, func(ke gotui.KeyEvent) { r.appendTreeSearch(ke.Rune) }),
	}
}

func (r *goTUIRoot) moveTreeSelect(delta int) {
	if len(r.treeNodes) == 0 {
		return
	}
	r.treeSelectIdx = clampInt(r.treeSelectIdx+delta, 0, len(r.treeNodes)-1)
	r.adjustTreeSelectScroll()
	r.bump()
}

func (r *goTUIRoot) jumpTreeSelect(idx int) {
	if len(r.treeNodes) == 0 {
		return
	}
	r.treeSelectIdx = clampInt(idx, 0, len(r.treeNodes)-1)
	r.adjustTreeSelectScroll()
	r.bump()
}

func (r *goTUIRoot) appendTreeSearch(ch rune) {
	if ch == 0 {
		return
	}
	r.treeSearch += string(ch)
	r.treeSelectIdx = 0
	r.filterTreeNodes()
	r.bump()
}

func (r *goTUIRoot) backspaceTreeSearch() {
	rs := []rune(r.treeSearch)
	if len(rs) == 0 {
		return
	}
	r.treeSearch = string(rs[:len(rs)-1])
	r.treeSelectIdx = 0
	r.filterTreeNodes()
	r.bump()
}

func (r *goTUIRoot) toggleTreeSummary() {
	r.treeShowSummary = !r.treeShowSummary
	r.bump()
}

func (r *goTUIRoot) adjustTreeSelectScroll() {
	if len(r.treeNodes) <= treeSelectVisibleRows {
		r.treeSelectScroll = 0
		return
	}
	if r.treeSelectIdx < r.treeSelectScroll {
		r.treeSelectScroll = r.treeSelectIdx
	}
	if r.treeSelectIdx >= r.treeSelectScroll+treeSelectVisibleRows {
		r.treeSelectScroll = r.treeSelectIdx - treeSelectVisibleRows + 1
	}
}

func (r *goTUIRoot) selectedTreeNode() (coding_agent.SessionTreeNode, bool) {
	if len(r.treeNodes) == 0 || r.treeSelectIdx < 0 || r.treeSelectIdx >= len(r.treeNodes) {
		return coding_agent.SessionTreeNode{}, false
	}
	return r.treeNodes[r.treeSelectIdx], true
}

func (r *goTUIRoot) confirmTreeSelect() {
	node, ok := r.selectedTreeNode()
	if r.session == nil || !ok {
		return
	}
	if err := r.session.NavigateTree(node.ID); err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	r.closeTreeSelect("jumped to tree entry")
}

func (r *goTUIRoot) branchTreeSelect() {
	node, ok := r.selectedTreeNode()
	if r.session == nil || !ok {
		return
	}
	if _, err := r.session.CreateBranchedSession(node.ID); err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	r.closeTreeSelect("created branched session")
}

func (r *goTUIRoot) closeTreeSelect(status string) {
	r.model.state = uiStateInput
	r.treeNodes = nil
	r.treeAllNodes = nil
	r.treeSelectIdx = 0
	r.treeSelectScroll = 0
	r.treeSearch = ""
	r.treeShowSummary = false
	r.model.statusMsg = status
	r.bump()
}

func (r *goTUIRoot) renderTreeSelectWidget() *gotui.Element {
	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)
	mode := "compact"
	if r.treeShowSummary {
		mode = "summary"
	}
	container.AddChild(gotui.New(
		gotui.WithText("  Session tree  view="+mode),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
		gotui.WithFlexShrink(0),
	))
	search := r.treeSearch
	if search == "" {
		search = "type to search"
	}
	container.AddChild(gotui.New(
		gotui.WithText("  search: "+search),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))

	end := r.treeSelectScroll + treeSelectVisibleRows
	if end > len(r.treeNodes) {
		end = len(r.treeNodes)
	}
	if len(r.treeNodes) == 0 {
		container.AddChild(gotui.New(
			gotui.WithText("  no matching entries"),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
	}
	for i := r.treeSelectScroll; i < end; i++ {
		node := r.treeNodes[i]
		line := treeNodeLine(node, i == r.treeSelectIdx, r.treeShowSummary)
		if i == r.treeSelectScroll && r.treeSelectScroll > 0 {
			line += "  ↑"
		} else if i == end-1 && end < len(r.treeNodes) {
			line += "  ↓"
		}
		style := gotui.NewStyle().Dim()
		if node.InCurrentPath {
			style = gotui.NewStyle()
		}
		if i == r.treeSelectIdx {
			style = gotui.NewStyle().Foreground(gotui.Cyan).Bold()
		}
		container.AddChild(gotui.New(
			gotui.WithText(line),
			gotui.WithTextStyle(style),
			gotui.WithFlexShrink(0),
		))
	}
	container.AddChild(gotui.New(
		gotui.WithText("  ↑/↓ select  enter jump  ctrl+f branch-session  ctrl+s summary  esc close"),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))
	return container
}

func treeNodeLine(node coding_agent.SessionTreeNode, selected, showSummary bool) string {
	prefix := "  "
	if selected {
		prefix = "❯ "
	}
	pathMarker := " "
	if node.InCurrentPath {
		pathMarker = "•"
	}
	if node.Current {
		pathMarker = "*"
	}
	indent := strings.Repeat("  ", node.Depth)
	kind := node.Type
	if node.Role != "" {
		kind = node.Role
	}
	preview := strings.ReplaceAll(strings.TrimSpace(node.Preview), "\n", " ")
	limit := 72 - len(indent)
	if showSummary {
		limit = 110 - len(indent)
	}
	if limit < 24 {
		limit = 24
	}
	if len(preview) > limit {
		preview = preview[:limit-3] + "..."
	}
	if preview == "" {
		preview = "(no preview)"
	}
	branch := ""
	if node.ChildCount > 1 {
		branch = fmt.Sprintf(" branches=%d", node.ChildCount)
	}
	return fmt.Sprintf("%s%s%s %-9s %-15s%s %s%s", prefix, indent, pathMarker, "#"+shortTreeNodeID(node.ID), treeNodeKind(kind), treeNodeLabel(node.Label), preview, branch)
}

func shortTreeNodeID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func treeNodeKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "branch_summary":
		return "branch"
	case "model_change":
		return "model"
	case "":
		return "entry"
	default:
		return kind
	}
}

func treeNodeLabel(label string) string {
	label = strings.ReplaceAll(strings.TrimSpace(label), "\n", " ")
	if label == "" {
		return ""
	}
	if len(label) > 18 {
		label = label[:15] + "..."
	}
	return " [" + label + "]"
}
