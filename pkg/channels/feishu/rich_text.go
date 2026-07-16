package feishu

import (
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var (
	feishuMarkdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM))
	htmlTagPattern       = regexp.MustCompile(`<[^>]*>`)
)

// postElement is one element in a Feishu post paragraph. Feishu post messages
// support structured text and links, but do not accept Markdown directly.
type postElement struct {
	Tag  string `json:"tag"`
	Text string `json:"text,omitempty"`
	Href string `json:"href,omitempty"`
}

type postDocument struct {
	ZhCN postBody `json:"zh_cn"`
}

type postBody struct {
	Title   string          `json:"title"`
	Content [][]postElement `json:"content"`
}

// buildPostContent converts common Markdown constructs into Feishu's post
// message schema. Unsupported presentation details are reduced to readable
// text; links remain structured clickable elements.
func buildPostContent(markdown string) (string, error) {
	source := []byte(strings.ReplaceAll(markdown, "\r\n", "\n"))
	document := feishuMarkdownParser.Parser().Parse(text.NewReader(source))
	builder := &postBuilder{source: source}
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		builder.appendBlock(node, "", 0)
	}
	if len(builder.paragraphs) == 0 {
		builder.paragraphs = [][]postElement{{{Tag: "text", Text: " "}}}
	}

	content, err := json.Marshal(postDocument{ZhCN: postBody{
		Title:   builder.title,
		Content: builder.paragraphs,
	}})
	if err != nil {
		return "", err
	}
	return string(content), nil
}

type postBuilder struct {
	source     []byte
	title      string
	paragraphs [][]postElement
}

func (b *postBuilder) appendBlock(node ast.Node, prefix string, listDepth int) {
	switch current := node.(type) {
	case *ast.Heading:
		elements := b.inlineElements(current)
		if b.title == "" && len(b.paragraphs) == 0 {
			b.title = strings.TrimSpace(postElementsText(elements))
			return
		}
		b.addParagraph(prefix, elements)
	case *ast.Paragraph, *ast.TextBlock:
		b.addParagraph(prefix, b.inlineElements(current))
	case *ast.List:
		b.appendList(current, prefix, listDepth)
	case *ast.Blockquote:
		for child := current.FirstChild(); child != nil; child = child.NextSibling() {
			b.appendBlock(child, prefix+"│ ", listDepth)
		}
	case *ast.FencedCodeBlock:
		b.addCodeBlock(prefix, current.Language(b.source), current.Lines().Value(b.source))
	case *ast.CodeBlock:
		b.addCodeBlock(prefix, nil, current.Lines().Value(b.source))
	case *ast.ThematicBreak:
		b.addParagraph(prefix, []postElement{{Tag: "text", Text: "────────"}})
	case *extensionast.Table:
		b.appendTable(current, prefix)
	case *ast.HTMLBlock:
		raw := current.Lines().Value(b.source)
		if current.HasClosure() {
			raw = append(raw, current.ClosureLine.Value(b.source)...)
		}
		plain := strings.TrimSpace(html.UnescapeString(htmlTagPattern.ReplaceAllString(string(raw), "")))
		if plain != "" {
			b.addParagraph(prefix, []postElement{{Tag: "text", Text: plain}})
		}
	default:
		for child := current.FirstChild(); child != nil; child = child.NextSibling() {
			b.appendBlock(child, prefix, listDepth)
		}
	}
}

func (b *postBuilder) appendList(list *ast.List, prefix string, depth int) {
	index := list.Start
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "• "
		if list.IsOrdered() {
			marker = strconv.Itoa(index) + ". "
			index++
		}
		itemPrefix := prefix + strings.Repeat("  ", depth) + marker
		continuationPrefix := prefix + strings.Repeat("  ", depth+1)
		firstBlock := true
		for child := item.FirstChild(); child != nil; child = child.NextSibling() {
			if nested, ok := child.(*ast.List); ok {
				b.appendList(nested, prefix, depth+1)
				continue
			}
			blockPrefix := continuationPrefix
			if firstBlock {
				blockPrefix = itemPrefix
				firstBlock = false
			}
			b.appendBlock(child, blockPrefix, depth)
		}
	}
}

func (b *postBuilder) addCodeBlock(prefix string, language, code []byte) {
	label := "代码"
	languageName := strings.TrimSpace(string(language))
	if languageName != "" {
		label += " (" + languageName + ")"
	}
	value := strings.TrimRight(string(code), "\n")
	if value != "" {
		label += "\n" + value
	}
	b.addParagraph(prefix, []postElement{{Tag: "text", Text: label}})
}

func (b *postBuilder) appendTable(table *extensionast.Table, prefix string) {
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		b.addTableRow(row, prefix)
	}
}

func (b *postBuilder) addTableRow(row ast.Node, prefix string) {
	var elements []postElement
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		if len(elements) > 0 {
			elements = appendTextElement(elements, " | ")
		}
		elements = append(elements, b.inlineElements(cell)...)
	}
	if len(elements) > 0 {
		b.addParagraph(prefix, elements)
	}
}

func (b *postBuilder) addParagraph(prefix string, elements []postElement) {
	paragraph := make([]postElement, 0, len(elements)+1)
	paragraph = appendTextElement(paragraph, prefix)
	for _, element := range elements {
		paragraph = appendPostElement(paragraph, element)
	}
	if strings.TrimSpace(postElementsText(paragraph)) == "" {
		return
	}
	b.paragraphs = append(b.paragraphs, paragraph)
}

func (b *postBuilder) inlineElements(parent ast.Node) []postElement {
	var elements []postElement
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		switch current := child.(type) {
		case *ast.Text:
			elements = appendTextElement(elements, resolveMarkdownText(current.Value(b.source)))
			if current.HardLineBreak() {
				elements = appendTextElement(elements, "\n")
			} else if current.SoftLineBreak() {
				elements = appendTextElement(elements, " ")
			}
		case *ast.String:
			elements = appendTextElement(elements, resolveMarkdownText(current.Value))
		case *ast.CodeSpan:
			var code strings.Builder
			for textNode := current.FirstChild(); textNode != nil; textNode = textNode.NextSibling() {
				value := string(textNode.(*ast.Text).Value(b.source))
				code.WriteString(strings.TrimSuffix(value, "\n"))
				if strings.HasSuffix(value, "\n") {
					code.WriteByte(' ')
				}
			}
			elements = appendTextElement(elements, code.String())
		case *ast.Link:
			label := postElementsText(b.inlineElements(current))
			elements = appendLinkOrText(elements, label, current.Destination)
		case *ast.Image:
			label := strings.TrimSpace(postElementsText(b.inlineElements(current)))
			if label == "" {
				label = "图片"
			}
			elements = appendLinkOrText(elements, label, current.Destination)
		case *ast.AutoLink:
			label := string(current.Label(b.source))
			href := current.URL(b.source)
			if current.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(string(href)), "mailto:") {
				href = append([]byte("mailto:"), href...)
			}
			elements = appendLinkOrText(elements, label, href)
		case *extensionast.TaskCheckBox:
			checkbox := "☐ "
			if current.IsChecked {
				checkbox = "☑ "
			}
			elements = appendTextElement(elements, checkbox)
		case *ast.RawHTML:
			raw := strings.ToLower(strings.TrimSpace(string(current.Segments.Value(b.source))))
			if strings.HasPrefix(raw, "<br") {
				elements = appendTextElement(elements, "\n")
			}
		default:
			for _, element := range b.inlineElements(current) {
				elements = appendPostElement(elements, element)
			}
		}
	}
	return elements
}

func appendLinkOrText(elements []postElement, label string, destination []byte) []postElement {
	href := string(util.URLEscape(destination, true))
	if validPostLink(href) {
		return appendPostElement(elements, postElement{Tag: "a", Text: label, Href: href})
	}
	return appendTextElement(elements, label)
}

func validPostLink(href string) bool {
	parsed, err := url.Parse(href)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.Host != ""
	case "mailto":
		return parsed.Opaque != "" || parsed.Path != ""
	default:
		return false
	}
}

func appendTextElement(elements []postElement, value string) []postElement {
	if value == "" {
		return elements
	}
	return appendPostElement(elements, postElement{Tag: "text", Text: value})
}

func appendPostElement(elements []postElement, element postElement) []postElement {
	if element.Text == "" {
		return elements
	}
	if element.Tag == "text" && len(elements) > 0 && elements[len(elements)-1].Tag == "text" {
		elements[len(elements)-1].Text += element.Text
		return elements
	}
	return append(elements, element)
}

func postElementsText(elements []postElement) string {
	var text strings.Builder
	for _, element := range elements {
		text.WriteString(element.Text)
	}
	return text.String()
}

func resolveMarkdownText(value []byte) string {
	value = util.UnescapePunctuations(value)
	value = util.ResolveNumericReferences(value)
	value = util.ResolveEntityNames(value)
	return string(value)
}
