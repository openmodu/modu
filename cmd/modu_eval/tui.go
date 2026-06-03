package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/openmodu/modu/pkg/evals"
)

type viewMode int

const (
	listView viewMode = iota
	detailView
)

type tuiModel struct {
	results      []evals.EvalLogLine
	filtered     []evals.EvalLogLine
	cursor       int
	mode         viewMode
	showFailures bool
	width        int
	height       int
	viewport     viewport.Model
	ready        bool
}

func initialModel(results []evals.EvalLogLine) tuiModel {
	return tuiModel{
		results:  sortedResults(results),
		filtered: sortedResults(results),
		mode:     listView,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
			return m, tea.Quit
		case "up", "k":
			if m.mode == listView && m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.mode == listView && m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
		case "enter":
			if m.mode == listView && len(m.filtered) > 0 {
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
			if m.cursor >= len(m.filtered) {
				m.cursor = len(m.filtered) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
	}

	if m.mode == detailView && m.ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m tuiModel) View() string {
	if m.mode == detailView {
		return m.renderDetailView()
	}
	return m.renderListView()
}

func (m tuiModel) filterResults() []evals.EvalLogLine {
	if !m.showFailures {
		return m.results
	}
	var out []evals.EvalLogLine
	for _, result := range m.results {
		if !result.Pass {
			out = append(out, result)
		}
	}
	return out
}

func (m tuiModel) renderListView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}

	passed, failed := countResults(m.results)
	header := fmt.Sprintf("modu evals - %d rubrics | %d passed | %d failed", len(m.results), passed, failed)
	if m.showFailures {
		header += " (failures only)"
	}

	var b strings.Builder
	b.WriteString(headerStyle(width).Render(header))
	b.WriteString("\n\n")

	if len(m.filtered) == 0 {
		b.WriteString("No results to display.\n\n")
		b.WriteString(footerStyle(width).Render("[f] Toggle Failures  [q] Quit"))
		return b.String()
	}

	resultWidth := 9
	scoreWidth := 7
	providerWidth := clamp(width/5, 16, 32)
	testWidth := clamp((width-providerWidth-resultWidth-scoreWidth-8)/2, 20, 48)
	rubricWidth := width - providerWidth - testWidth - resultWidth - scoreWidth - 8
	if rubricWidth < 20 {
		rubricWidth = 20
	}

	headerLine := " " + cell("PROVIDER", providerWidth) + " " + cell("TEST", testWidth) +
		" " + cell("RUBRIC", rubricWidth) + " " + cell("SCORE", scoreWidth) + " RESULT"
	b.WriteString(tableHeaderStyle().Render(headerLine))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", width))
	b.WriteString("\n")

	for i, result := range m.filtered {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}
		status := passLabel(result.Pass)
		line := cursor +
			cell(providerLabel(result), providerWidth) + " " +
			cell(result.Name, testWidth) + " " +
			cell(oneLine(result.Rubric), rubricWidth) + " " +
			cell(fmt.Sprintf("%.2f", result.Score), scoreWidth) + " " +
			status
		style := lipgloss.NewStyle().Width(width)
		if i == m.cursor {
			style = style.Background(lipgloss.Color("237"))
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(footerStyle(width).Render("[up/down j/k] Navigate  [enter] Details  [f] Toggle Failures  [q] Quit"))
	return b.String()
}

func (m tuiModel) buildDetailContent() string {
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return "No results to display"
	}
	result := m.filtered[m.cursor]
	width := m.width
	if width <= 0 {
		width = 100
	}

	var b strings.Builder
	writeDetailSection(&b, "Provider", providerLabel(result), width)
	if result.Grader != "" {
		writeDetailSection(&b, "Grader", result.Grader, width)
	}
	writeDetailSection(&b, "Rubric", result.Rubric, width)
	writeDetailSection(&b, "Output", result.Output, width)
	writeDetailSection(&b, "Reasoning", result.Reasoning, width)

	resultText := fmt.Sprintf("%s (score %.2f)", passLabel(result.Pass), result.Score)
	writeDetailSection(&b, "Result", resultText, width)
	return b.String()
}

func (m tuiModel) renderDetailView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return "No results to display"
	}
	result := m.filtered[m.cursor]

	var b strings.Builder
	b.WriteString(headerStyle(width).Render("Eval Detail - " + truncateDisplay(result.Name, width-16)))
	b.WriteString("\n")
	if m.ready {
		b.WriteString(m.viewport.View())
	}
	b.WriteString("\n")
	b.WriteString(footerStyle(width).Render("[esc] Back  [up/down j/k pageup/pagedown home/end] Scroll  [f] Toggle Failures  [q] Quit"))
	return b.String()
}

func writeDetailSection(b *strings.Builder, title, content string, width int) {
	section := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	body := lipgloss.NewStyle().Foreground(lipgloss.Color("255")).MarginLeft(2)
	b.WriteString(section.Render(title + ":"))
	b.WriteString("\n")
	b.WriteString(body.Render(wrapTextPreserveNewlines(content, width-4)))
	b.WriteString("\n\n")
}

func countResults(results []evals.EvalLogLine) (passed, failed int) {
	for _, result := range results {
		if result.Pass {
			passed++
		} else {
			failed++
		}
	}
	return passed, failed
}

func passLabel(pass bool) string {
	if pass {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("PASS")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("FAIL")
}

func headerStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("62")).
		Padding(0, 1).
		Width(width)
}

func footerStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Background(lipgloss.Color("236")).
		Padding(0, 1).
		Width(width)
}

func tableHeaderStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("240"))
}

func wrapTextPreserveNewlines(text string, width int) string {
	if width <= 0 {
		width = 80
	}
	paragraphs := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if strings.TrimSpace(paragraph) == "" {
			wrapped = append(wrapped, "")
			continue
		}
		words := strings.Fields(paragraph)
		var lines []string
		line := ""
		for _, word := range words {
			if runewidth.StringWidth(line)+runewidth.StringWidth(word)+1 <= width {
				if line == "" {
					line = word
				} else {
					line += " " + word
				}
				continue
			}
			if line != "" {
				lines = append(lines, line)
			}
			line = word
		}
		if line != "" {
			lines = append(lines, line)
		}
		wrapped = append(wrapped, strings.Join(lines, "\n"))
	}
	return strings.Join(wrapped, "\n")
}

// truncateDisplay shortens s to at most max terminal cells, cutting on rune
// boundaries (never mid-rune) and accounting for wide CJK characters. A trailing
// "…" marks truncation. Byte slicing here would corrupt multibyte UTF-8.
func truncateDisplay(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	var b strings.Builder
	limit := max - 1 // reserve one cell for the ellipsis
	width := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if width+rw > limit {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String() + "…"
}

// cell truncates s to width terminal cells and right-pads it to exactly that
// width, so columns line up even with wide CJK content (fmt's %-*s pads by rune
// count, which mismeasures wide runes).
func cell(s string, width int) string {
	s = truncateDisplay(s, width)
	if pad := width - runewidth.StringWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func runTUI(results []evals.EvalLogLine) error {
	if len(results) == 0 {
		fmt.Println("No evaluation results found.")
		return nil
	}
	p := tea.NewProgram(initialModel(results), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
