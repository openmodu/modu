package moms

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// SandboxType determines where bash commands are executed.
type SandboxType string

const (
	SandboxHost   SandboxType = "host"
	SandboxDocker SandboxType = "docker"
)

// SandboxConfig holds the sandbox configuration.
type SandboxConfig struct {
	Type      SandboxType
	Container string // Container name, only used when Type == SandboxDocker
}

// ParseSandboxArg parses a --sandbox=... CLI argument value.
func ParseSandboxArg(value string) (SandboxConfig, error) {
	if value == "host" {
		return SandboxConfig{Type: SandboxHost}, nil
	}
	if after, ok := strings.CutPrefix(value, "docker:"); ok {
		if after == "" {
			return SandboxConfig{}, fmt.Errorf("docker sandbox requires container name (e.g., docker:moms-sandbox)")
		}
		return SandboxConfig{Type: SandboxDocker, Container: after}, nil
	}
	return SandboxConfig{}, fmt.Errorf("invalid sandbox type %q, use 'host' or 'docker:<container>'", value)
}

// ValidateSandbox returns an error if the sandbox is not ready.
func ValidateSandbox(cfg SandboxConfig) error {
	if cfg.Type == SandboxHost {
		return nil
	}
	// Check Docker is available
	if err := execSimple("docker", []string{"--version"}); err != nil {
		return fmt.Errorf("docker is not available: %w", err)
	}
	// Check container is running
	out, err := execSimpleOutput("docker", []string{"inspect", "-f", "{{.State.Running}}", cfg.Container})
	if err != nil {
		return fmt.Errorf("container %q does not exist, create it first", cfg.Container)
	}
	if strings.TrimSpace(out) != "true" {
		return fmt.Errorf("container %q is not running, start it with: docker start %s", cfg.Container, cfg.Container)
	}
	return nil
}

// ExecResult holds the output of a sandbox exec call.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// Sandbox executes commands in the configured environment.
type Sandbox struct {
	cfg SandboxConfig
}

// NewSandbox creates a new Sandbox.
func NewSandbox(cfg SandboxConfig) *Sandbox {
	return &Sandbox{cfg: cfg}
}

// Exec runs a bash command in the sandbox with an optional timeout (seconds, 0 = default 120).
func (s *Sandbox) Exec(ctx context.Context, command string, timeoutSecs int) ExecResult {
	if timeoutSecs <= 0 {
		timeoutSecs = 120
	}
	if timeoutSecs > 600 {
		timeoutSecs = 600
	}

	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if s.cfg.Type == SandboxDocker {
		// Escape single quotes in command
		escaped := strings.ReplaceAll(command, "'", "'\\''")
		dockerCmd := fmt.Sprintf("docker exec %s sh -c '%s'", s.cfg.Container, escaped)
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", dockerCmd)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	res := ExecResult{
		Stdout: limitStr(stdout.String(), 10*1024*1024),
		Stderr: limitStr(stderr.String(), 10*1024*1024),
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		}
	}

	return res
}

// GetWorkspacePath returns the path to the workspace directory as seen by the sandbox.
// For host sandboxes, this is the actual path. For Docker, it is /workspace.
func (s *Sandbox) GetWorkspacePath(hostPath string) string {
	if s.cfg.Type == SandboxDocker {
		return "/workspace"
	}
	return hostPath
}

// -----------------------------------------------------------------------
// helpers

func execSimple(name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func execSimpleOutput(name string, args []string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func limitStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
