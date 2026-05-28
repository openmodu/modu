package tools

import (
	"github.com/openmodu/modu/pkg/agent"
)

// CodingTools returns the core coding tools: read, bash, edit, write.
func CodingTools(cwd string) []agent.Tool {
	return []agent.Tool{
		NewReadTool(cwd),
		NewBashTool(cwd),
		NewEditTool(cwd),
		NewWriteTool(cwd),
	}
}

// ReadOnlyTools returns read-only tools: read, grep, find, ls.
func ReadOnlyTools(cwd string) []agent.Tool {
	return []agent.Tool{
		NewReadTool(cwd),
		NewGrepTool(cwd),
		NewFindTool(cwd),
		NewLsTool(cwd),
	}
}

// AllTools returns all available built-in coding tools.
func AllTools(cwd string) []agent.Tool {
	return []agent.Tool{
		NewReadTool(cwd),
		NewWriteTool(cwd),
		NewEditTool(cwd),
		NewBashTool(cwd),
		NewGrepTool(cwd),
		NewFindTool(cwd),
		NewLsTool(cwd),
	}
}
