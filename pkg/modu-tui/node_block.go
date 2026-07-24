package modutui

import (
	"fmt"
	"strings"
)

type nodeGroupBlock struct {
	Marker string
	Nodes  []Node
}

func (b nodeGroupBlock) Render(ctx RenderContext) BlockRender {
	var out BlockRender
	marker := b.Marker
	for _, node := range b.Nodes {
		block := blockFromNode(node, marker)
		if block == nil {
			continue
		}
		rendered := block.Render(ctx)
		if len(rendered.Lines) == 0 {
			continue
		}
		if len(out.Lines) > 0 {
			out.Add("", 0)
		}
		out.Lines = append(out.Lines, rendered.Lines...)
		marker = ""
	}
	return out
}

func blockFromNode(node Node, marker string) Block {
	switch node := node.(type) {
	case TextNode:
		return TextBlock{Marker: marker, Text: node.Text}
	case MarkdownNode:
		return MarkdownBlock{Marker: marker, Text: node.Text}
	case CodeNode:
		return CodeBlock{Marker: marker, Language: node.Language, Code: node.Code}
	case ThinkingNode:
		return ThinkingBlock{Text: node.Text, Expanded: node.Expanded}
	case ToolNode:
		return ToolCallBlock{
			CollapsibleBlock: CollapsibleBlock{
				Summary:  node.Call.Summary,
				Detail:   node.Call.Detail,
				Expanded: node.Expanded,
			},
			Call:       node.Call,
			Permission: node.Permission,
		}
	case TableNode:
		return TableBlock{Marker: marker, Rows: node.Rows}
	case ListNode:
		return TextBlock{Marker: marker, Text: listNodeText(node)}
	case KeyValueNode:
		return TextBlock{Marker: marker, Text: keyValueNodeText(node)}
	case ProgressNode:
		return TextBlock{Marker: marker, Text: progressNodeText(node)}
	default:
		return nil
	}
}

func listNodeText(node ListNode) string {
	lines := make([]string, 0, len(node.Items))
	for _, item := range node.Items {
		label := strings.TrimSpace(item.Label)
		detail := strings.TrimSpace(item.Detail)
		if label == "" && detail == "" {
			continue
		}
		prefix := "•"
		if node.Ordered {
			prefix = fmt.Sprintf("%d.", len(lines)+1)
		}
		line := strings.TrimSpace(prefix + " " + label)
		if detail != "" {
			line += "  " + detail
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func keyValueNodeText(node KeyValueNode) string {
	lines := make([]string, 0, len(node.Items))
	for _, item := range node.Items {
		key := strings.TrimSpace(item.Key)
		value := strings.TrimSpace(item.Value)
		if key == "" && value == "" {
			continue
		}
		if key == "" {
			lines = append(lines, value)
			continue
		}
		lines = append(lines, key+": "+value)
	}
	return strings.Join(lines, "\n")
}

func progressNodeText(node ProgressNode) string {
	label := strings.TrimSpace(node.Label)
	status := strings.TrimSpace(node.Status)
	if node.Total <= 0 {
		return strings.TrimSpace(strings.Join(nonEmptyStrings(label, status), "  "))
	}
	current := clamp(node.Current, 0, node.Total)
	const cells = 10
	filled := current * cells / node.Total
	bar := "[" + strings.Repeat("=", filled) + strings.Repeat("-", cells-filled) + "]"
	count := fmt.Sprintf("%d/%d", current, node.Total)
	return strings.TrimSpace(strings.Join(nonEmptyStrings(label, bar, count, status), "  "))
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
