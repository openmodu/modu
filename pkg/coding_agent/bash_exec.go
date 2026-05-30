package coding_agent

import (
	"context"

	"github.com/openmodu/modu/pkg/coding_agent/services/bash"
)

// BashResult aliases the bash service result so host/RPC callers keep working.
type BashResult = bash.Result

// CodingSession implements bash.Host.
var _ bash.Host = (*CodingSession)(nil)

// Cwd returns the session's current working directory. It is a kernel
// capability (it follows worktree switches) and satisfies bash.Host.
func (s *CodingSession) Cwd() string { return s.cwd }

// ExecuteBash runs a shell command in the session cwd.
func (s *CodingSession) ExecuteBash(ctx context.Context, command string, timeoutMs int) (*BashResult, error) {
	return s.bash.Execute(ctx, command, timeoutMs)
}

// AbortBash cancels the currently running bash command, if any.
func (s *CodingSession) AbortBash() { s.bash.Abort() }
