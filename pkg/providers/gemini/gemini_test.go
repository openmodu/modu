package gemini

import (
	"bytes"
	"testing"

	"github.com/openmodu/modu/pkg/providers"
)

func TestMessagesToContentsPreservesTextAndInlineImageParts(t *testing.T) {
	contents := messagesToContents([]providers.Message{{
		Role: providers.RoleUser,
		Content: []any{
			map[string]any{"type": "text", "text": "inspect"},
			map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": "data:image/png;base64,cG5n",
				},
			},
		},
	}})

	if len(contents) != 1 || len(contents[0].Parts) != 2 {
		t.Fatalf("contents = %#v", contents)
	}
	if contents[0].Parts[0].Text != "inspect" {
		t.Fatalf("text part = %#v", contents[0].Parts[0])
	}
	image := contents[0].Parts[1].InlineData
	if image == nil || image.MIMEType != "image/png" || !bytes.Equal(image.Data, []byte("png")) {
		t.Fatalf("image part = %#v", contents[0].Parts[1])
	}
}
