package tools

import (
	"github.com/openmodu/modu/pkg/agent"
)

// CodingTools returns the core coding tools: read, bash, edit, write.
func CodingTools(cwd string) []agent.AgentTool {
	return []agent.AgentTool{
		NewReadTool(cwd),
		NewGitPreflightTool(cwd),
		NewBashTool(cwd),
		NewEditTool(cwd),
		NewWriteTool(cwd),
	}
}

// ReadOnlyTools returns read-only tools: read, grep, find, ls.
func ReadOnlyTools(cwd string) []agent.AgentTool {
	return []agent.AgentTool{
		NewReadTool(cwd),
		NewGitPreflightTool(cwd),
		NewGrepTool(cwd),
		NewFindTool(cwd),
		NewLsTool(cwd),
	}
}

// AllTools returns all available tools.
// find and ls are intentionally excluded: bash covers both, and
// removing them keeps the tool list lean for better model latency.
func AllTools(cwd string) []agent.AgentTool {
	return []agent.AgentTool{
		NewReadTool(cwd),
		NewGitPreflightTool(cwd),
		NewWriteTool(cwd),
		NewEditTool(cwd),
		NewBashTool(cwd),
		NewGrepTool(cwd),
	}
}
