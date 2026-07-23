package providers

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// ContentPart is the provider-neutral view of the OpenAI-compatible content
// parts carried by Message.Content.
type ContentPart struct {
	Text     string
	MIMEType string
	Data     []byte
}

// ParseContentParts decodes text and base64 image_url parts. The bool is false
// when content is not a multipart value, allowing providers to retain their
// existing scalar-content fallback.
func ParseContentParts(content any) ([]ContentPart, bool, error) {
	raw, ok := content.([]any)
	if !ok {
		return nil, false, nil
	}
	parts := make([]ContentPart, 0, len(raw))
	for _, value := range raw {
		part, ok := value.(map[string]any)
		if !ok {
			continue
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "text":
			text, _ := part["text"].(string)
			parts = append(parts, ContentPart{Text: text})
		case "image_url":
			imageURL, _ := part["image_url"].(map[string]any)
			dataURL, _ := imageURL["url"].(string)
			mimeType, data, err := decodeImageDataURL(dataURL)
			if err != nil {
				return nil, true, err
			}
			parts = append(parts, ContentPart{MIMEType: mimeType, Data: data})
		}
	}
	return parts, true, nil
}

func decodeImageDataURL(value string) (string, []byte, error) {
	header, encoded, ok := strings.Cut(value, ",")
	if !ok || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(header, ";base64") {
		return "", nil, fmt.Errorf("invalid base64 image data URL")
	}
	mimeType := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	if !strings.HasPrefix(mimeType, "image/") {
		return "", nil, fmt.Errorf("unsupported image data URL MIME type %q", mimeType)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("decode image data URL: %w", err)
	}
	return mimeType, data, nil
}
