package feishu

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type decodedPostContent struct {
	ZhCN struct {
		Title   string          `json:"title"`
		Content [][]postElement `json:"content"`
	} `json:"zh_cn"`
}

func TestBuildPostContentConvertsMarkdown(t *testing.T) {
	markdown := `# Release result

**Done** via [pull request](https://example.com/pr/1).

- changed ` + "`bot.go`" + `
- [x] tests pass

> Keep the Feishu adapter isolated.

` + "```go" + `
fmt.Println("ok")
` + "```" + `

| Item | State |
| --- | --- |
| Feishu | ready |
`

	content, err := buildPostContent(markdown)
	if err != nil {
		t.Fatalf("buildPostContent() error = %v", err)
	}

	post := decodePostContent(t, content)
	if post.ZhCN.Title != "Release result" {
		t.Fatalf("title = %q, want %q", post.ZhCN.Title, "Release result")
	}

	wantParagraphs := []string{
		"Done via pull request.",
		"• changed bot.go",
		"• ☑ tests pass",
		"│ Keep the Feishu adapter isolated.",
		"代码 (go)\nfmt.Println(\"ok\")",
		"Item | State",
		"Feishu | ready",
	}
	if got := postParagraphTexts(post); !reflect.DeepEqual(got, wantParagraphs) {
		t.Fatalf("paragraphs = %#v, want %#v", got, wantParagraphs)
	}

	var links []postElement
	for _, paragraph := range post.ZhCN.Content {
		for _, element := range paragraph {
			if element.Tag == "a" {
				links = append(links, element)
			}
		}
	}
	wantLinks := []postElement{{Tag: "a", Text: "pull request", Href: "https://example.com/pr/1"}}
	if !reflect.DeepEqual(links, wantLinks) {
		t.Fatalf("links = %#v, want %#v", links, wantLinks)
	}

	for _, marker := range []string{"**", "```", "[pull request]", "| --- |"} {
		if strings.Contains(content, marker) {
			t.Errorf("post content still contains raw Markdown marker %q: %s", marker, content)
		}
	}
}

func TestBuildPostContentKeepsUnsupportedLinksAsText(t *testing.T) {
	content, err := buildPostContent("[relative](/settings) and [unsafe](javascript:alert(1))")
	if err != nil {
		t.Fatalf("buildPostContent() error = %v", err)
	}

	post := decodePostContent(t, content)
	if got, want := postParagraphTexts(post), []string{"relative and unsafe"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paragraphs = %#v, want %#v", got, want)
	}
	for _, paragraph := range post.ZhCN.Content {
		for _, element := range paragraph {
			if element.Tag == "a" {
				t.Fatalf("unsupported URL rendered as link: %#v", element)
			}
		}
	}
}

func TestBuildPostContentEmptyMessage(t *testing.T) {
	content, err := buildPostContent(" \n\t")
	if err != nil {
		t.Fatalf("buildPostContent() error = %v", err)
	}

	post := decodePostContent(t, content)
	if got, want := postParagraphTexts(post), []string{" "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paragraphs = %#v, want %#v", got, want)
	}
}

func decodePostContent(t *testing.T, content string) decodedPostContent {
	t.Helper()
	var post decodedPostContent
	if err := json.Unmarshal([]byte(content), &post); err != nil {
		t.Fatalf("decode post content: %v\n%s", err, content)
	}
	return post
}

func postParagraphTexts(post decodedPostContent) []string {
	paragraphs := make([]string, 0, len(post.ZhCN.Content))
	for _, paragraph := range post.ZhCN.Content {
		var text strings.Builder
		for _, element := range paragraph {
			text.WriteString(element.Text)
		}
		paragraphs = append(paragraphs, text.String())
	}
	return paragraphs
}
