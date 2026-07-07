package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type agentTUIModel struct {
	results      []agentRunResult
	filtered     []agentRunResult
	tasks        []agentTask
	runningTask  string
	completed    bool
	runError     string
	outputDir    string
	cursor       int
	mode         viewMode
	showFailures bool
	width        int
	height       int
	viewport     viewport.Model
	ready        bool
}

type agentMetricGroup struct {
	Name         string
	TotalTasks   int
	PassedTasks  int
	TotalChecks  int
	PassedChecks int
	TotalSeconds float64
}

type agentCoreMetrics struct {
	TotalTasks    int
	PassedTasks   int
	TotalChecks   int
	PassedChecks  int
	TotalSeconds  float64
	ByCategory    []agentMetricGroup
	ByGradingType []agentMetricGroup
}

type agentTUITaskRow struct {
	Task   agentTask
	Result *agentRunResult
	Status string
}

type agentTaskStartedMsg struct {
	Task  agentTask
	Index int
	Total int
}

type agentTaskResultMsg struct {
	Result agentRunResult
	Index  int
	Total  int
}

type agentRunFinishedMsg struct {
	Results   []agentRunResult
	Failed    int
	Error     string
	OutputDir string
}

func initialAgentModel(results []agentRunResult) agentTUIModel {
	sorted := sortedAgentResults(results)
	return agentTUIModel{
		results:   sorted,
		filtered:  sorted,
		completed: true,
		mode:      listView,
	}
}

func initialAgentLiveModel(tasks []agentTask, outputDir string) agentTUIModel {
	return agentTUIModel{
		tasks:     append([]agentTask(nil), tasks...),
		filtered:  []agentRunResult{},
		outputDir: outputDir,
		mode:      listView,
	}
}

func (m agentTUIModel) Init() tea.Cmd {
	return nil
}

func (m agentTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case agentTaskStartedMsg:
		m.runningTask = msg.Task.ID
		return m, nil

	case agentTaskResultMsg:
		m.results = upsertAgentResult(m.results, msg.Result)
		m.results = sortedAgentResults(m.results)
		m.filtered = m.filterResults()
		m.runningTask = ""
		m.clampCursor()
		if m.mode == detailView {
			m.viewport.SetContent(m.buildDetailContent())
		}
		return m, nil

	case agentRunFinishedMsg:
		m.results = sortedAgentResults(msg.Results)
		m.filtered = m.filterResults()
		m.runningTask = ""
		m.completed = true
		m.runError = msg.Error
		m.outputDir = msg.OutputDir
		m.clampCursor()
		if m.mode == detailView {
			m.viewport.SetContent(m.buildDetailContent())
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-3)
			m.viewport.YPosition = 2
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - 3
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.completed {
				return m, tea.Quit
			}
			return m, nil
		case "up", "k":
			if m.mode == listView && m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.mode == listView && m.cursor < m.rowCount()-1 {
				m.cursor++
			}
		case "enter":
			if m.mode == listView && m.rowCount() > 0 {
				m.mode = detailView
				m.viewport.SetContent(m.buildDetailContent())
				m.viewport.GotoTop()
			}
		case "esc":
			if m.mode == detailView {
				m.mode = listView
			}
		case "f":
			m.showFailures = !m.showFailures
			m.filtered = m.filterResults()
			m.clampCursor()
		}
	}

	if m.mode == detailView && m.ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m agentTUIModel) View() string {
	if m.mode == detailView {
		return m.renderDetailView()
	}
	return m.renderListView()
}

func (m agentTUIModel) filterResults() []agentRunResult {
	if !m.showFailures {
		return m.results
	}
	var out []agentRunResult
	for _, result := range m.results {
		if !agentTaskPassed(result) {
			out = append(out, result)
		}
	}
	return out
}

func (m *agentTUIModel) clampCursor() {
	if m.cursor >= m.rowCount() {
		m.cursor = m.rowCount() - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m agentTUIModel) rowCount() int {
	return len(m.taskRows())
}

func (m agentTUIModel) taskRows() []agentTUITaskRow {
	if len(m.tasks) == 0 {
		rows := make([]agentTUITaskRow, 0, len(m.filtered))
		for _, result := range m.filtered {
			resultCopy := result
			rows = append(rows, agentTUITaskRow{
				Task:   taskFromAgentResult(result),
				Result: &resultCopy,
				Status: result.Status,
			})
		}
		return rows
	}

	resultsByID := map[string]agentRunResult{}
	for _, result := range m.results {
		resultsByID[result.TaskID] = result
	}
	rows := make([]agentTUITaskRow, 0, len(m.tasks))
	for _, task := range m.tasks {
		if result, ok := resultsByID[task.ID]; ok {
			if m.showFailures && agentTaskPassed(result) {
				continue
			}
			resultCopy := result
			rows = append(rows, agentTUITaskRow{Task: task, Result: &resultCopy, Status: result.Status})
			continue
		}
		if m.showFailures {
			continue
		}
		status := "pending"
		if task.ID == m.runningTask {
			status = "running"
		} else if m.completed {
			status = "skipped"
		}
		rows = append(rows, agentTUITaskRow{Task: task, Status: status})
	}
	return rows
}

func (m agentTUIModel) renderListView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}

	metrics := summarizeAgentResults(m.results)
	failedTasks := metrics.TotalTasks - metrics.PassedTasks
	expectedTasks := len(m.tasks)
	if expectedTasks == 0 {
		expectedTasks = metrics.TotalTasks
	}
	header := fmt.Sprintf("modu agent evals - %d/%d complete | %d passed | %d failed | %.1f%% task pass | %.1f%% check pass",
		metrics.TotalTasks, expectedTasks, metrics.PassedTasks, failedTasks,
		percent(metrics.PassedTasks, metrics.TotalTasks),
		percent(metrics.PassedChecks, metrics.TotalChecks))
	if m.showFailures {
		header += " (failures only)"
	}
	if !m.completed && m.runningTask != "" {
		header += " | running " + m.runningTask
	}

	var b strings.Builder
	b.WriteString(headerStyle(width).Render(header))
	b.WriteString("\n\n")
	if m.outputDir != "" {
		b.WriteString("Results: " + m.outputDir + "\n")
	}
	if m.runError != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("Run error: " + m.runError))
		b.WriteString("\n")
	}
	if !m.completed {
		b.WriteString("Running evaluations. Results update as each task completes.\n")
	}
	if m.outputDir != "" || m.runError != "" || !m.completed {
		b.WriteString("\n")
	}
	b.WriteString(renderAgentMetrics(metrics, width))
	b.WriteString("\n")

	rows := m.taskRows()
	if len(rows) == 0 {
		b.WriteString("No results to display.\n\n")
		b.WriteString(footerStyle(width).Render(agentFooter(m.completed)))
		return b.String()
	}

	statusWidth := 12
	checksWidth := 9
	scoreWidth := 7
	timeWidth := 8
	categoryWidth := clamp(width/6, 10, 20)
	taskWidth := width - statusWidth - checksWidth - scoreWidth - timeWidth - categoryWidth - 7
	if taskWidth < 24 {
		taskWidth = 24
	}

	headerLine := " " + cell("TASK", taskWidth) + " " + cell("CATEGORY", categoryWidth) +
		" " + cell("STATUS", statusWidth) + " " + cell("CHECKS", checksWidth) +
		" " + cell("SCORE", scoreWidth) + " TIME"
	b.WriteString(tableHeaderStyle().Render(headerLine))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", width))
	b.WriteString("\n")

	for i, row := range rows {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}
		checkPassed, totalChecks := 0, 0
		score := "-"
		elapsed := "-"
		if row.Result != nil {
			checkPassed, totalChecks = agentCheckCounts(*row.Result)
			score = fmt.Sprintf("%.2f", agentAverageScore(*row.Result))
			elapsed = fmt.Sprintf("%6.1fs", row.Result.ExecutionTimeSeconds)
		}
		line := cursor +
			cell(agentTaskRowTitle(row), taskWidth) + " " +
			cell(agentTaskRowCategory(row), categoryWidth) + " " +
			cell(agentRowStatusLabel(row), statusWidth) + " " +
			cell(fmt.Sprintf("%d/%d", checkPassed, totalChecks), checksWidth) + " " +
			cell(score, scoreWidth) + " " +
			elapsed
		style := lipgloss.NewStyle().Width(width)
		if i == m.cursor {
			style = style.Background(lipgloss.Color("237"))
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(footerStyle(width).Render(agentFooter(m.completed)))
	return b.String()
}

func renderAgentMetrics(metrics agentCoreMetrics, width int) string {
	var b strings.Builder
	avgTime := 0.0
	if metrics.TotalTasks > 0 {
		avgTime = metrics.TotalSeconds / float64(metrics.TotalTasks)
	}
	b.WriteString(sectionTitleStyle().Render("Core Metrics"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  Task pass rate:  %.1f%% (%d/%d)\n", percent(metrics.PassedTasks, metrics.TotalTasks), metrics.PassedTasks, metrics.TotalTasks))
	b.WriteString(fmt.Sprintf("  Check pass rate: %.1f%% (%d/%d)\n", percent(metrics.PassedChecks, metrics.TotalChecks), metrics.PassedChecks, metrics.TotalChecks))
	b.WriteString(fmt.Sprintf("  Avg task time:   %.1fs\n", avgTime))

	if len(metrics.ByCategory) > 0 {
		b.WriteString("\n")
		b.WriteString(renderAgentMetricGroups("By Category", metrics.ByCategory, width))
	}
	if len(metrics.ByGradingType) > 0 {
		b.WriteString("\n")
		b.WriteString(renderAgentMetricGroups("By Grading Type", metrics.ByGradingType, width))
	}
	return b.String()
}

func renderAgentMetricGroups(title string, groups []agentMetricGroup, width int) string {
	nameWidth := clamp(width-44, 14, 40)
	var b strings.Builder
	b.WriteString(sectionTitleStyle().Render(title))
	b.WriteString("\n")
	b.WriteString("  " + cell("NAME", nameWidth) + " " + cell("TASKS", 11) + " " + cell("CHECKS", 11) + " AVG TIME\n")
	for _, group := range groups {
		avgTime := 0.0
		if group.TotalTasks > 0 {
			avgTime = group.TotalSeconds / float64(group.TotalTasks)
		}
		b.WriteString("  " + cell(group.Name, nameWidth) + " " +
			cell(fmt.Sprintf("%d/%d %.0f%%", group.PassedTasks, group.TotalTasks, percent(group.PassedTasks, group.TotalTasks)), 11) + " " +
			cell(fmt.Sprintf("%d/%d %.0f%%", group.PassedChecks, group.TotalChecks, percent(group.PassedChecks, group.TotalChecks)), 11) + " " +
			fmt.Sprintf("%6.1fs\n", avgTime))
	}
	return b.String()
}

func (m agentTUIModel) buildDetailContent() string {
	rows := m.taskRows()
	if len(rows) == 0 || m.cursor >= len(rows) {
		return "No results to display"
	}
	row := rows[m.cursor]
	width := m.width
	if width <= 0 {
		width = 100
	}

	var b strings.Builder
	writeDetailSection(&b, "Task", agentTaskRowTitle(row), width)
	writeDetailSection(&b, "Status", agentRowStatusLabel(row), width)
	writeDetailSection(&b, "Category", agentTaskRowCategory(row), width)
	writeDetailSection(&b, "Grading Type", agentTaskRowGradingType(row), width)
	if row.Result == nil {
		writeDetailSection(&b, "Checks", renderAgentTaskCheckPlan(row.Task), width)
		writeDetailSection(&b, "Source", row.Task.SourcePath, width)
		return b.String()
	}

	result := *row.Result
	writeDetailSection(&b, "Checks", renderAgentCheckDetails(result), width)
	if result.Error != "" {
		writeDetailSection(&b, "Error", result.Error, width)
	}
	if result.AssistantText != "" {
		writeDetailSection(&b, "Assistant", result.AssistantText, width)
	}
	if result.Stderr != "" {
		writeDetailSection(&b, "Stderr", result.Stderr, width)
	}
	writeDetailSection(&b, "Workspace", result.WorkspacePath, width)
	writeDetailSection(&b, "Source", result.SourceTask, width)
	return b.String()
}

func (m agentTUIModel) renderDetailView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	rows := m.taskRows()
	if len(rows) == 0 || m.cursor >= len(rows) {
		return "No results to display"
	}
	row := rows[m.cursor]

	var b strings.Builder
	b.WriteString(headerStyle(width).Render("Agent Eval Detail - " + truncateDisplay(agentTaskRowTitle(row), width-22)))
	b.WriteString("\n")
	if m.ready {
		b.WriteString(m.viewport.View())
	}
	b.WriteString("\n")
	b.WriteString(footerStyle(width).Render("[esc] Back  [up/down j/k pageup/pagedown home/end] Scroll  [f] Toggle Failures" + agentQuitHint(m.completed)))
	return b.String()
}

func renderAgentTaskCheckPlan(task agentTask) string {
	if len(task.Checks) == 0 && task.GradeCode == "" {
		return "No checks declared."
	}
	var b strings.Builder
	for _, check := range task.Checks {
		name := check.Name
		if name == "" {
			name = check.Type
		}
		b.WriteString("- [PENDING] " + name)
		if check.Type != "" {
			b.WriteString(" (" + check.Type + ")")
		}
		b.WriteString("\n")
	}
	if task.GradeCode != "" {
		b.WriteString("- [PENDING] python_grade\n")
	}
	return strings.TrimSpace(b.String())
}

func renderAgentCheckDetails(result agentRunResult) string {
	if len(result.CheckResults) == 0 {
		if result.Status == "success" {
			return "No checks recorded."
		}
		return "No checks ran."
	}
	var b strings.Builder
	for _, check := range result.CheckResults {
		status := "PASS"
		if !check.Pass {
			status = "FAIL"
		}
		b.WriteString(fmt.Sprintf("- [%s] %.2f %s", status, check.Score, check.Name))
		if check.Type != "" {
			b.WriteString(" (" + check.Type + ")")
		}
		detail := check.Reason
		if detail == "" {
			detail = check.ErrorText
		}
		if detail != "" {
			b.WriteString(": " + oneLine(detail))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func summarizeAgentResults(results []agentRunResult) agentCoreMetrics {
	metrics := agentCoreMetrics{TotalTasks: len(results)}
	byCategory := map[string]*agentMetricGroup{}
	byGradingType := map[string]*agentMetricGroup{}

	for _, result := range results {
		passedChecks, totalChecks := agentCheckCounts(result)
		if agentTaskPassed(result) {
			metrics.PassedTasks++
		}
		metrics.TotalChecks += totalChecks
		metrics.PassedChecks += passedChecks
		metrics.TotalSeconds += result.ExecutionTimeSeconds

		addAgentMetricGroup(byCategory, agentCategory(result), result, passedChecks, totalChecks)
		addAgentMetricGroup(byGradingType, agentGradingType(result), result, passedChecks, totalChecks)
	}
	metrics.ByCategory = sortedAgentMetricGroups(byCategory)
	metrics.ByGradingType = sortedAgentMetricGroups(byGradingType)
	return metrics
}

func addAgentMetricGroup(groups map[string]*agentMetricGroup, name string, result agentRunResult, passedChecks, totalChecks int) {
	group := groups[name]
	if group == nil {
		group = &agentMetricGroup{Name: name}
		groups[name] = group
	}
	group.TotalTasks++
	if agentTaskPassed(result) {
		group.PassedTasks++
	}
	group.TotalChecks += totalChecks
	group.PassedChecks += passedChecks
	group.TotalSeconds += result.ExecutionTimeSeconds
}

func sortedAgentMetricGroups(groups map[string]*agentMetricGroup) []agentMetricGroup {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]agentMetricGroup, 0, len(names))
	for _, name := range names {
		out = append(out, *groups[name])
	}
	return out
}

func sortedAgentResults(results []agentRunResult) []agentRunResult {
	out := append([]agentRunResult(nil), results...)
	sort.SliceStable(out, func(i, j int) bool {
		leftPassed := agentTaskPassed(out[i])
		rightPassed := agentTaskPassed(out[j])
		if leftPassed != rightPassed {
			return !leftPassed
		}
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		if out[i].TaskID != out[j].TaskID {
			return out[i].TaskID < out[j].TaskID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func upsertAgentResult(results []agentRunResult, result agentRunResult) []agentRunResult {
	out := append([]agentRunResult(nil), results...)
	for i := range out {
		if out[i].TaskID == result.TaskID {
			out[i] = result
			return out
		}
	}
	return append(out, result)
}

func taskFromAgentResult(result agentRunResult) agentTask {
	return agentTask{
		ID:          result.TaskID,
		Name:        result.Name,
		Category:    result.Category,
		GradingType: result.GradingType,
		SourcePath:  result.SourceTask,
	}
}

func agentTaskPassed(result agentRunResult) bool {
	return result.Status == "success"
}

func agentCheckCounts(result agentRunResult) (passed, total int) {
	for _, check := range result.CheckResults {
		total++
		if check.Pass {
			passed++
		}
	}
	return passed, total
}

func agentAverageScore(result agentRunResult) float64 {
	if len(result.CheckResults) == 0 {
		if agentTaskPassed(result) {
			return 1.0
		}
		return 0
	}
	total := 0.0
	for _, check := range result.CheckResults {
		total += check.Score
	}
	return total / float64(len(result.CheckResults))
}

func agentTaskTitle(result agentRunResult) string {
	if result.TaskID == "" {
		return result.Name
	}
	if result.Name == "" || result.Name == result.TaskID {
		return result.TaskID
	}
	return result.TaskID + " - " + result.Name
}

func agentTaskRowTitle(row agentTUITaskRow) string {
	if row.Result != nil {
		return agentTaskTitle(*row.Result)
	}
	if row.Task.ID == "" {
		return row.Task.Name
	}
	if row.Task.Name == "" || row.Task.Name == row.Task.ID {
		return row.Task.ID
	}
	return row.Task.ID + " - " + row.Task.Name
}

func agentTaskRowCategory(row agentTUITaskRow) string {
	if row.Result != nil {
		return agentCategory(*row.Result)
	}
	if row.Task.Category == "" {
		return "uncategorized"
	}
	return row.Task.Category
}

func agentTaskRowGradingType(row agentTUITaskRow) string {
	if row.Result != nil {
		return agentGradingType(*row.Result)
	}
	if row.Task.GradingType == "" {
		return "deterministic"
	}
	return row.Task.GradingType
}

func agentCategory(result agentRunResult) string {
	if result.Category == "" {
		return "uncategorized"
	}
	return result.Category
}

func agentGradingType(result agentRunResult) string {
	if result.GradingType == "" {
		return "deterministic"
	}
	return result.GradingType
}

func agentStatusLabel(result agentRunResult) string {
	label := strings.ToUpper(result.Status)
	if label == "" {
		label = "UNKNOWN"
	}
	if agentTaskPassed(result) {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(label)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(label)
}

func agentRowStatusLabel(row agentTUITaskRow) string {
	if row.Result != nil {
		return agentStatusLabel(*row.Result)
	}
	label := strings.ToUpper(row.Status)
	if label == "" {
		label = "PENDING"
	}
	color := lipgloss.Color("240")
	switch row.Status {
	case "running":
		color = lipgloss.Color("3")
	case "skipped":
		color = lipgloss.Color("8")
	}
	return lipgloss.NewStyle().Foreground(color).Render(label)
}

func agentFooter(completed bool) string {
	return "[up/down j/k] Navigate  [enter] Details  [f] Toggle Failures" + agentQuitHint(completed)
}

func agentQuitHint(completed bool) string {
	if completed {
		return "  [q] Quit"
	}
	return "  [q] Quit after run"
}

func sectionTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
}

func runAgentEvalCommandWithTUI(tasks []agentTask, opts agentEvalOptions) error {
	program := tea.NewProgram(initialAgentLiveModel(tasks, opts.OutputDir), tea.WithAltScreen(), tea.WithMouseCellMotion())
	runDone := make(chan error, 1)

	go func() {
		results, failed, err := runAgentEvalTasks(tasks, opts, agentRunCallbacks{
			Start: func(task agentTask, index, total int) {
				program.Send(agentTaskStartedMsg{Task: task, Index: index, Total: total})
			},
			Result: func(result agentRunResult, index, total int) {
				program.Send(agentTaskResultMsg{Result: result, Index: index, Total: total})
			},
		})
		if err == nil {
			err = writeAgentEvalSummaryIfNeeded(opts, results)
		}

		runErrText := ""
		if err != nil {
			runErrText = err.Error()
		}
		program.Send(agentRunFinishedMsg{
			Results:   results,
			Failed:    failed,
			Error:     runErrText,
			OutputDir: opts.OutputDir,
		})

		if err != nil {
			runDone <- err
			return
		}
		if failed > 0 {
			runDone <- fmt.Errorf("%d agent eval task(s) failed", failed)
			return
		}
		runDone <- nil
	}()

	_, programErr := program.Run()
	runErr := <-runDone
	fmt.Printf("agent eval results: %s\n", opts.OutputDir)
	if programErr != nil {
		return programErr
	}
	return runErr
}

func runAgentTUI(results []agentRunResult) error {
	if len(results) == 0 {
		fmt.Println("No agent evaluation results found.")
		return nil
	}
	p := tea.NewProgram(initialAgentModel(results), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
