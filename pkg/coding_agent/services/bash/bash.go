// Package bash is the inline `!command` execution service. It runs a shell
// command in the session's current working directory and supports cancellation
// of an in-flight run. It depends on the kernel only through the one-method Host
// interface.
package bash

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
	"time"
)

// Host is the kernel capability the runner needs: the live working directory
// (which follows worktree changes).
type Host interface {
	Cwd() string
}

// Result is the outcome of a shell command.
type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// Runner owns the cancel func for an in-flight run.
type Runner struct {
	host   Host
	mu     sync.Mutex
	cancel context.CancelFunc
}

func New(host Host) *Runner { return &Runner{host: host} }

// Execute runs command with a timeout (default 30s) in the host's cwd.
func (r *Runner) Execute(ctx context.Context, command string, timeoutMs int) (*Result, error) {
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)

	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	defer func() {
		cancel()
		r.mu.Lock()
		r.cancel = nil
		r.mu.Unlock()
	}()

	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = r.host.Cwd()

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, err
		}
	}

	return &Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, nil
}

// Abort cancels the currently running command, if any.
func (r *Runner) Abort() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}
