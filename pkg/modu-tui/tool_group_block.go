package modutui

import (
	"fmt"
	"strings"
)

// ToolGroupBlock renders a parallel tool batch (several tool calls that share
// one BatchID) as a single collapsible entry so a fan-out of N tools does not
// flood the transcript with N separate blocks. Collapsed it is one summary
// line; expanded it shows the batch header plus a one-line summary per child.
type ToolGroupBlock struct {
	Calls    []ToolCall
	Expanded bool
}

func (b ToolGroupBlock) Render(ctx RenderContext) BlockRender {
	out := BlockRender{}
	n := len(b.Calls)

	errors, done := 0, 0
	for _, c := range b.Calls {
		if c.Error {
			errors++
		}
		if c.Done {
			done++
		}
	}

	header := fmt.Sprintf("%d tools · parallel", n)
	if done < n {
		header += fmt.Sprintf(" · %d/%d done", done, n)
	}
	if errors > 0 {
		header += fmt.Sprintf(" · %d error", errors)
	}

	if !b.Expanded {
		out.Add(toolExpandedLine(ctx.ContentWidth, "  "+header), 0)
		return out
	}

	out.Add(toolExpandedLine(ctx.ContentWidth, "  "+header), 0)
	for i, c := range b.Calls {
		branch := "├ "
		if i == n-1 {
			branch = "└ "
		}
		out.Add(toolExpandedLine(ctx.ContentWidth, "    "+branch+toolGroupChildLabel(c)), 0)
	}
	return out
}

// toolGroupChildLabel names one child in an expanded batch. It prefers the
// invocation (e.g. "Read(alpha.txt)") so a fan-out shows which target each call
// hit, falling back to the tool's own summary when there is no headline
// argument. Errors are surfaced inline.
func toolGroupChildLabel(c ToolCall) string {
	label := strings.TrimSpace(toolInvocationLine(c))
	if c.Error {
		return label + " · error"
	}
	if strings.TrimSpace(c.Input) == "" && strings.TrimSpace(c.Detail) == "" {
		if s := strings.TrimSpace(c.Summary); s != "" {
			return s
		}
	}
	return label
}
