package bash

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

const (
	maxBackgroundOutputBytes  = 1024 * 1024
	maxRetainedBackgroundJobs = 64
)

type JobStatus string

const (
	JobRunning JobStatus = "running"
	JobExited  JobStatus = "exited"
)

type JobSnapshot struct {
	ID        string
	Command   string
	PID       int
	Status    JobStatus
	ExitCode  int
	Error     string
	StartedAt time.Time
	Finished  time.Time
}

type Job struct {
	mu sync.Mutex

	id        string
	command   string
	pid       int
	status    JobStatus
	exitCode  int
	errText   string
	startedAt time.Time
	finished  time.Time
	timer     *time.Timer
	killAsked bool

	output     []byte
	baseOffset int64
	readOffset int64
}

func (j *Job) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.output = append(j.output, p...)
	if overflow := len(j.output) - maxBackgroundOutputBytes; overflow > 0 {
		j.output = append([]byte(nil), j.output[overflow:]...)
		j.baseOffset += int64(overflow)
	}
	return len(p), nil
}

func (j *Job) drain() (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	truncated := j.readOffset < j.baseOffset
	if truncated {
		j.readOffset = j.baseOffset
	}
	start := int(j.readOffset - j.baseOffset)
	if start < 0 || start > len(j.output) {
		start = len(j.output)
	}
	out := string(j.output[start:])
	j.readOffset = j.baseOffset + int64(len(j.output))
	j.output = nil
	j.baseOffset = j.readOffset
	return out, truncated
}

func (j *Job) snapshot() JobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	return JobSnapshot{
		ID:        j.id,
		Command:   j.command,
		PID:       j.pid,
		Status:    j.status,
		ExitCode:  j.exitCode,
		Error:     j.errText,
		StartedAt: j.startedAt,
		Finished:  j.finished,
	}
}

func (j *Job) finish(exitCode int, errText string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.timer != nil {
		j.timer.Stop()
	}
	j.status = JobExited
	j.exitCode = exitCode
	j.errText = errText
	j.finished = time.Now()
}

func (j *Job) kill() (bool, error) {
	j.mu.Lock()
	if j.status != JobRunning {
		j.mu.Unlock()
		return false, nil
	}
	if j.killAsked {
		j.mu.Unlock()
		return false, nil
	}
	j.killAsked = true
	pid := j.pid
	j.mu.Unlock()
	if pid <= 0 {
		return false, fmt.Errorf("background process has no pid")
	}
	if err := killProcessGroup(pid); err != nil {
		j.mu.Lock()
		j.killAsked = false
		j.mu.Unlock()
		return false, err
	}
	return true, nil
}

type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*Job
	seq  int64
}

func NewJobStore() *JobStore {
	return &JobStore{jobs: make(map[string]*Job)}
}

func (s *JobStore) create(command string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.jobs) >= maxRetainedBackgroundJobs {
		var oldest *Job
		for _, candidate := range s.jobs {
			snapshot := candidate.snapshot()
			if snapshot.Status != JobExited {
				continue
			}
			if oldest == nil || snapshot.StartedAt.Before(oldest.snapshot().StartedAt) {
				oldest = candidate
			}
		}
		if oldest == nil {
			break
		}
		delete(s.jobs, oldest.snapshot().ID)
	}
	s.seq++
	job := &Job{
		id:        fmt.Sprintf("bash_%d", s.seq),
		command:   command,
		status:    JobRunning,
		exitCode:  -1,
		startedAt: time.Now(),
	}
	s.jobs[job.id] = job
	return job
}

func (s *JobStore) remove(id string) {
	s.mu.Lock()
	delete(s.jobs, id)
	s.mu.Unlock()
}

func (s *JobStore) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}

func (s *JobStore) KillAll() {
	if s == nil {
		return
	}
	s.mu.RLock()
	jobs := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	s.mu.RUnlock()
	for _, job := range jobs {
		_, _ = job.kill()
	}
}

func startBackgroundCommand(store *JobStore, cmd *exec.Cmd, command string, timeout time.Duration) (*Job, error) {
	if store == nil {
		return nil, fmt.Errorf("background jobs are not available")
	}
	job := store.create(command)
	cmd.Stdout = job
	cmd.Stderr = job
	if err := cmd.Start(); err != nil {
		store.remove(job.id)
		return nil, err
	}
	job.mu.Lock()
	job.pid = cmd.Process.Pid
	if timeout > 0 {
		job.timer = time.AfterFunc(timeout, func() {
			_, _ = job.kill()
		})
	}
	job.mu.Unlock()

	go func() {
		err := cmd.Wait()
		exitCode := 0
		errText := ""
		if err != nil {
			exitCode = -1
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
			errText = err.Error()
		}
		job.finish(exitCode, errText)
	}()
	return job, nil
}

type BashOutputTool struct {
	jobs *JobStore
}

func NewBashOutputTool(jobs *JobStore) types.Tool {
	return &BashOutputTool{jobs: jobs}
}

func (t *BashOutputTool) Name() string  { return "bash_output" }
func (t *BashOutputTool) Label() string { return "Background Bash Output" }
func (t *BashOutputTool) Description() string {
	return "Read output produced since the previous read from a background bash command, and report whether it is still running."
}
func (t *BashOutputTool) Parameters() any {
	return bashJobParameters()
}
func (t *BashOutputTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	return executeBashOutput(t.jobs, args), nil
}

type KillBashTool struct {
	jobs *JobStore
}

func NewKillBashTool(jobs *JobStore) types.Tool {
	return &KillBashTool{jobs: jobs}
}

func (t *KillBashTool) Name() string  { return "kill_bash" }
func (t *KillBashTool) Label() string { return "Kill Background Bash" }
func (t *KillBashTool) Description() string {
	return "Terminate a background bash command by the bash_id returned from bash with run_in_background=true."
}
func (t *KillBashTool) Parameters() any {
	return bashJobParameters()
}
func (t *KillBashTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	return executeKillBash(t.jobs, args), nil
}

func bashJobParameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bash_id": map[string]any{
				"type":        "string",
				"description": "The bash_id returned by a background bash command.",
			},
		},
		"required": []string{"bash_id"},
	}
}

func executeBashOutput(store *JobStore, args map[string]any) types.ToolResult {
	id, _ := args["bash_id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return common.ErrorResult("bash_id is required")
	}
	if store == nil {
		return common.ErrorResult("background jobs are not available")
	}
	job, ok := store.Get(id)
	if !ok {
		return common.ErrorResult(fmt.Sprintf("no background bash command with id %q", id))
	}
	output, truncated := job.drain()
	snapshot := job.snapshot()
	status := fmt.Sprintf("[%s: %s]", snapshot.ID, snapshot.Status)
	if snapshot.Status == JobExited {
		status = fmt.Sprintf("[%s: exited code %d]", snapshot.ID, snapshot.ExitCode)
	}
	if truncated {
		output = "[earlier output truncated]\n" + output
	}
	text := status
	if output != "" {
		text = output
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += status
	}
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		Details: map[string]any{
			"bash_id":   snapshot.ID,
			"pid":       snapshot.PID,
			"status":    snapshot.Status,
			"exitCode":  snapshot.ExitCode,
			"error":     snapshot.Error,
			"truncated": truncated,
		},
	}
}

func executeKillBash(store *JobStore, args map[string]any) types.ToolResult {
	id, _ := args["bash_id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return common.ErrorResult("bash_id is required")
	}
	if store == nil {
		return common.ErrorResult("background jobs are not available")
	}
	job, ok := store.Get(id)
	if !ok {
		return common.ErrorResult(fmt.Sprintf("no background bash command with id %q", id))
	}
	killed, err := job.kill()
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to kill background command %s: %v", id, err))
	}
	text := fmt.Sprintf("background command %s was not running", id)
	if killed {
		text = fmt.Sprintf("killed background command %s", id)
	}
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		Details: map[string]any{"bash_id": id, "killed": killed},
	}
}
