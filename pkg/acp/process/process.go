// Package process manages the lifecycle of an ACP agent subprocess.
//
// A Process wraps an os/exec.Cmd and exposes line-oriented channels for
// stdout (LDJSON frames) and stderr (arbitrary log lines). It does NOT
// parse JSON-RPC — that is the responsibility of pkg/acp/client.
//
// Design notes:
//   - stdout is read with bufio.Scanner using a 10 MB token buffer, because
//     ACP responses (tool results, large file reads) can exceed the default
//     64 KB cap.
//   - stderr is drained on a separate goroutine; otherwise a chatty agent
//     can fill the pipe buffer and block stdout indirectly.
//   - Stop() is graceful: close stdin → signal interrupt → 3s timeout →
//     SIGKILL. The process goroutine reports final exit via Done().
package process

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Status is the lifecycle state of an ACP subprocess.
type Status string

const (
	StatusIdle     Status = "idle"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusError    Status = "error"
	StatusStopped  Status = "stopped"
)

const (
	// ScannerBufferMax caps a single LDJSON line at 10 MB. Large enough for
	// realistic tool results (including images base64-encoded) without being
	// unbounded.
	ScannerBufferMax = 10 * 1024 * 1024

	// StopGracePeriod is how long we wait after SIGINT before SIGKILL.
	StopGracePeriod = 3 * time.Second
)

// Config describes how to launch an ACP subprocess.
type Config struct {
	ID      string            // logical agent id, used for logging
	Command string            // executable (e.g. "npx")
	Args    []string          // arguments
	Env     map[string]string // extra env appended to os.Environ()
	Dir     string            // working directory; empty = current
}

// Process is an ACP agent subprocess.
type Process struct {
	cfg Config

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	status Status

	lines  chan []byte
	stderr chan []byte
	done   chan struct{}
	// wg tracks the stdout/stderr reader goroutines so Stop() can wait for
	// clean channel closure.
	wg sync.WaitGroup
}

// New builds a Process. Start must be called before Write / Lines produce
// useful output.
func New(cfg Config) *Process {
	return &Process{
		cfg:    cfg,
		status: StatusIdle,
	}
}

// Status returns the current lifecycle status.
func (p *Process) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Start launches the subprocess and begins reading stdout/stderr. If the
// process is already running, Start is a no-op.
func (p *Process) Start() error {
	p.mu.Lock()
	if p.status == StatusRunning || p.status == StatusStarting {
		p.mu.Unlock()
		return nil
	}
	p.status = StatusStarting
	p.mu.Unlock()

	cmd := exec.Command(p.cfg.Command, p.cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range p.cfg.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	if p.cfg.Dir != "" {
		cmd.Dir = p.cfg.Dir
	}
	hideWindow(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		p.setStatus(StatusError)
		return fmt.Errorf("acp/process: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		p.setStatus(StatusError)
		return fmt.Errorf("acp/process: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		p.setStatus(StatusError)
		return fmt.Errorf("acp/process: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		p.setStatus(StatusError)
		return fmt.Errorf("acp/process: start %s: %w", p.cfg.Command, err)
	}

	lines := make(chan []byte, 64)
	errCh := make(chan []byte, 16)
	done := make(chan struct{})

	p.mu.Lock()
	p.cmd = cmd
	p.stdin = stdin
	p.lines = lines
	p.stderr = errCh
	p.done = done
	p.status = StatusRunning
	p.mu.Unlock()

	p.wg.Add(2)
	go p.readStdout(stdout, lines)
	go p.readStderr(stderr, errCh)

	go func() {
		_ = cmd.Wait()
		p.wg.Wait()
		close(lines)
		close(errCh)
		p.mu.Lock()
		if p.status != StatusStopped {
			p.status = StatusStopped
		}
		p.mu.Unlock()
		close(done)
	}()

	return nil
}

// Write sends one LDJSON line to the subprocess stdin. The caller is
// responsible for JSON-encoding; Write appends the newline terminator.
func (p *Process) Write(line []byte) error {
	p.mu.Lock()
	stdin := p.stdin
	status := p.status
	p.mu.Unlock()

	if status != StatusRunning {
		return errors.New("acp/process: not running")
	}
	if stdin == nil {
		return errors.New("acp/process: stdin closed")
	}
	buf := make([]byte, 0, len(line)+1)
	buf = append(buf, line...)
	buf = append(buf, '\n')
	_, err := stdin.Write(buf)
	return err
}

// Lines returns a receive-only channel of stdout lines. Closed when the
// subprocess exits. Lines have the trailing newline stripped.
func (p *Process) Lines() <-chan []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lines
}

// Stderr returns a receive-only channel of stderr lines.
func (p *Process) Stderr() <-chan []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr
}

// Done returns a channel closed when the subprocess has fully exited AND
// both reader goroutines have finished.
func (p *Process) Done() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

// Stop attempts a graceful shutdown: close stdin, send SIGINT, and if the
// process hasn't exited within StopGracePeriod, SIGKILL.
func (p *Process) Stop() error {
	p.mu.Lock()
	cmd := p.cmd
	stdin := p.stdin
	done := p.done
	if cmd == nil {
		p.mu.Unlock()
		return nil
	}
	p.stdin = nil
	p.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}

	select {
	case <-done:
		// Exited gracefully.
	case <-time.After(StopGracePeriod):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}

	p.mu.Lock()
	p.status = StatusStopped
	p.cmd = nil
	p.mu.Unlock()
	return nil
}

func (p *Process) setStatus(s Status) {
	p.mu.Lock()
	p.status = s
	p.mu.Unlock()
}

func (p *Process) readStdout(r io.ReadCloser, out chan<- []byte) {
	defer p.wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), ScannerBufferMax)
	for scanner.Scan() {
		// Copy because scanner reuses its buffer.
		b := scanner.Bytes()
		cp := make([]byte, len(b))
		copy(cp, b)
		out <- cp
	}
}

func (p *Process) readStderr(r io.ReadCloser, out chan<- []byte) {
	defer p.wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 8*1024), 1024*1024)
	for scanner.Scan() {
		b := scanner.Bytes()
		cp := make([]byte, len(b))
		copy(cp, b)
		// Non-blocking send: if nobody is reading stderr, drop rather than
		// stall the subprocess.
		select {
		case out <- cp:
		default:
		}
	}
}
