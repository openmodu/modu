package tui

import (
	"strings"
	"testing"
)

// A "notify" block (used for extension/workflow completion reports) must render
// its content through the markdown pipeline, not as plain text. So markdown
// structure (headings, tables) should be transformed, not shown literally.
func TestNotifyBlockRendersMarkdown(t *testing.T) {
	m := &uiModel{width: 100}
	content := "Workflow deep_research completed with 6 agent(s).\n\n" +
		"## report\n\nLead answer here.\n\n" +
		"| Driver | Detail |\n| --- | --- |\n| Demand | AI buildout |\n"
	block := uiBlock{Kind: "notify", Title: "workflow", Content: content}

	out := m.renderSingleBlock(block)

	if !strings.Contains(out, "workflow") {
		t.Fatalf("title header missing:\n%s", out)
	}
	// The heading marker must be consumed by glamour, not printed verbatim.
	if strings.Contains(out, "## report") {
		t.Errorf("heading rendered literally (not markdown):\n%s", out)
	}
	// The markdown table must become a drawn box, not literal pipes.
	if !strings.ContainsAny(out, "┌─└│├┼") {
		t.Errorf("table not rendered as box:\n%s", out)
	}
}
