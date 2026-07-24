package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

func moduTUIImages(attachments []modutui.ImageAttachment) []types.ImageContent {
	images := make([]types.ImageContent, 0, len(attachments))
	for _, attachment := range attachments {
		images = append(images, types.ImageContent{
			Type:     "image",
			MimeType: attachment.MimeType,
			Data:     base64.StdEncoding.EncodeToString(attachment.Data),
		})
	}
	return images
}

func contentBlocksText(blocks []types.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	imageIndex := 0
	for _, block := range blocks {
		switch block := block.(type) {
		case *types.TextContent:
			if block != nil && block.Text != "" {
				parts = append(parts, block.Text)
			}
		case *types.ThinkingContent:
			if block != nil && block.Thinking != "" {
				parts = append(parts, block.Thinking)
			}
		case *types.ImageContent:
			if block != nil {
				imageIndex++
				parts = append(parts, fmt.Sprintf("[Image #%d]", imageIndex))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func formatJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}
