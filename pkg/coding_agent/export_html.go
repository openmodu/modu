package coding_agent

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"path/filepath"

	"github.com/openmodu/modu/pkg/types"
)

// ExportHTML writes the session messages as a simple HTML file.
func (s *CodingSession) ExportHTML(path string) error {
	msgs := s.agent.GetState().Messages
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	var buf bytes.Buffer
	buf.WriteString("<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"><title>Session Export</title></head><body>\n")

	for _, msg := range msgs {
		role := "unknown"
		content := ""

		switch m := msg.(type) {
		case types.UserMessage:
			role = "user"
			if str, ok := m.Content.(string); ok {
				content = str
			}
		case *types.UserMessage:
			role = "user"
			if str, ok := m.Content.(string); ok {
				content = str
			}
		case types.AssistantMessage:
			role = "assistant"
			for _, block := range m.Content {
				if tc, ok := block.(*types.TextContent); ok && tc != nil {
					content += tc.Text
				}
			}
		case *types.AssistantMessage:
			role = "assistant"
			for _, block := range m.Content {
				if tc, ok := block.(*types.TextContent); ok && tc != nil {
					content += tc.Text
				}
			}
		}

		buf.WriteString(fmt.Sprintf("<div class=\"message %s\"><strong>%s:</strong><pre>%s</pre></div>\n",
			role, role, html.EscapeString(content)))
	}

	buf.WriteString("</body></html>\n")
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
