package coding_agent

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
	"time"
)

// bashRunner owns the inline `!command` execution state (the cancel func for an
// in-flight run and its lock). It reads the live cwd from the session so it
// follows worktree changes.
type bashRunner struct {
	s      *CodingSession
	mu     sync.Mutex
	cancel context.CancelFunc
}

func newBashRunner(s *CodingSession) *bashRunner { return &bashRunner{s: s} }

func (b *bashRunner) execute(ctx context.Context, command string, timeoutMs int) (*BashResult, error) {
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}

	bashCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)

	b.mu.Lock()
	b.cancel = cancel
	b.mu.Unlock()

	defer func() {
		cancel()
		b.mu.Lock()
		b.cancel = nil
		b.mu.Unlock()
	}()

	cmd := exec.CommandContext(bashCtx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = b.s.cwd

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, err
		}
	}

	return &BashResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

func (b *bashRunner) abort() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
	}
}

// ExecuteBash executes a shell command and returns the result.
func (s *CodingSession) ExecuteBash(ctx context.Context, command string, timeoutMs int) (*BashResult, error) {
	return s.bash.execute(ctx, command, timeoutMs)
}

// AbortBash cancels the currently running bash command, if any.
func (s *CodingSession) AbortBash() { s.bash.abort() }
