package modutui

func defaultBlockFromMessage(m Message) Block {
	if m.Tool {
		call := ToolCall{ID: m.ToolID, Name: m.ToolName, Summary: m.Summary, Detail: m.Detail}
		return ToolCallBlock{
			CollapsibleBlock: CollapsibleBlock{
				Summary:  m.Summary,
				Detail:   m.Detail,
				Expanded: m.Expanded,
			},
			Call: call,
		}
	}
	marker := botStyle.Render("● ")
	if m.Role == RoleUser {
		marker = youStyle.Render("❯ ")
	}
	if m.Code != "" {
		return CodeBlock{Marker: marker, Language: m.Language, Code: m.Code}
	}
	if m.Role == RoleAssistant {
		return MarkdownBlock{Marker: marker, Text: m.Text}
	}
	return TextBlock{Marker: marker, Text: m.Text}
}
