package modutui

func defaultBlockFromMessage(m Message) Block {
	if m.Thinking {
		return ThinkingBlock{
			Text:     m.Text,
			Expanded: m.Expanded,
		}
	}
	if m.Tool {
		call := ToolCall{
			ID:           m.ToolID,
			Name:         m.ToolName,
			Summary:      m.Summary,
			Detail:       m.Detail,
			Input:        m.ToolInput,
			Output:       m.ToolOutput,
			ArtifactID:   m.ToolArtifactID,
			ArtifactPath: m.ToolArtifactPath,
			ArtifactText: m.ToolArtifactText,
			ArtifactErr:  m.ToolArtifactErr,
			ArtifactRead: m.ToolArtifactRead,
			Truncated:    m.ToolTruncated,
			BatchSize:    m.ToolBatchSize,
			Code:         m.ToolCode,
			Language:     m.ToolLanguage,
			Error:        m.ToolError,
			Done:         m.ToolDone,
			NoCollapse:   m.ToolNoCollapse,
		}
		return ToolCallBlock{
			CollapsibleBlock: CollapsibleBlock{
				Summary:  m.Summary,
				Detail:   m.Detail,
				Expanded: m.Expanded,
			},
			Call: call,
		}
	}
	marker := assistantMarkerStyle.Render("● ")
	if m.Role == RoleUser {
		marker = youStyle.Render("❯ ")
	}
	if m.Plain {
		marker = ""
	}
	if m.Code != "" {
		return CodeBlock{Marker: marker, Language: m.Language, Code: m.Code}
	}
	if m.Preformatted {
		return TextBlock{Marker: marker, Text: m.Text}
	}
	if m.Role == RoleAssistant {
		return MarkdownBlock{Marker: marker, Text: m.Text}
	}
	return TextBlock{Marker: marker, Text: m.Text}
}
