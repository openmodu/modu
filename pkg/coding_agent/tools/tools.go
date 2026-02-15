package tools

import (
	"github.com/crosszan/modu/pkg/agent"
)

// CodingTools returns the core coding tools: read, bash, edit, write.
func CodingTools(cwd string) []agent.AgentTool {
	return []agent.AgentTool{
		NewReadTool(cwd),
		NewBashTool(cwd),
		NewEditTool(cwd),
		NewWriteTool(cwd),
	}
}

// ReadOnlyTools returns read-only tools: read, grep, find, ls.
func ReadOnlyTools(cwd string) []agent.AgentTool {
	return []agent.AgentTool{
		NewReadTool(cwd),
		NewGrepTool(cwd),
		NewFindTool(cwd),
		NewLsTool(cwd),
	}
}

// AllTools returns all available tools.
func AllTools(cwd string) []agent.AgentTool {
	return []agent.AgentTool{
		NewReadTool(cwd),
		NewWriteTool(cwd),
		NewEditTool(cwd),
		NewBashTool(cwd),
		NewGrepTool(cwd),
		NewFindTool(cwd),
		NewLsTool(cwd),
	}
}
