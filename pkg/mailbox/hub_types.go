package mailbox

import (
	"errors"
	"time"
)

// PipelineStep defines one step in a Pipeline.
// DescriptionTemplate may contain {{.PrevResult}} which will be replaced with
// the output of the preceding step at runtime.
type PipelineStep struct {
	DescriptionTemplate string   `json:"description_template"`
	RequiredCaps        []string `json:"required_caps,omitempty"`
}

// Pipeline represents an ordered chain of tasks where each step's result is
// automatically injected into the next step's description.
type Pipeline struct {
	ID          string         `json:"id"`
	CreatorID   string         `json:"creator_id"`
	Steps       []PipelineStep `json:"steps"`
	CurrentStep int            `json:"current_step"` // 0-based index of the currently executing step
	Status      string         `json:"status"`       // "running" | "completed" | "failed"
	Results     []string       `json:"results,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

var (
	ErrAgentNotFound   = errors.New("agent not found")
	ErrTaskNotFound    = errors.New("task not found")
	ErrProjectNotFound = errors.New("project not found")
)

// TaskStatus 表示任务的生命周期状态
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusRunning    TaskStatus = "running"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusValidating TaskStatus = "validating" // work submitted, awaiting adversarial validator
	TaskStatusValidated  TaskStatus = "validated"  // passed adversarial validation
)

// ValidationAttempt records one worker-submission + validator-judgment cycle.
type ValidationAttempt struct {
	AttemptNum  int       `json:"attempt_num"`
	WorkerID    string    `json:"worker_id"`
	Result      string    `json:"result"`
	ValidatorID string    `json:"validator_id,omitempty"`
	Score       float64   `json:"score,omitempty"`
	Feedback    string    `json:"feedback,omitempty"`
	At          time.Time `json:"at"`
}

// AgentInfo 包含 Agent 的角色、状态和当前任务信息
type AgentInfo struct {
	ID           string    `json:"id"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`                 // "idle" | "busy"
	CurrentTask  string    `json:"current_task"`           // task ID，空表示空闲
	LastSeen     time.Time `json:"last_seen"`
	Capabilities []string  `json:"capabilities,omitempty"` // swarm: capabilities declared by the agent
}

// Project 表示一次多任务协作的集合（一次创作运行）
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"created_by"`
	TaskIDs   []string  `json:"task_ids"`
	Status    string    `json:"status"` // "active" | "completed"
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Task 表示一个可追踪的工作单元，支持多 Agent 并行执行
type Task struct {
	ID                  string             `json:"id"`
	ProjectID           string             `json:"project_id,omitempty"`
	Description         string             `json:"description"`
	CreatedBy           string             `json:"created_by"`
	OwnerID             string             `json:"owner_id"`
	AssignedTo          string             `json:"assigned_to"` // 向后兼容，= Assignees[0]
	Assignees           []string           `json:"assignees"`   // 全部参与该任务的 agent（owner + collaborators）
	Collaborators       []string           `json:"collaborators,omitempty"`
	Status              TaskStatus         `json:"status"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
	Summary             string             `json:"summary,omitempty"`
	Resolution          string             `json:"resolution,omitempty"`
	ArtifactPath        string             `json:"artifact_path,omitempty"`
	Result              string             `json:"result"`
	AgentResults        map[string]string  `json:"agent_results,omitempty"` // 每个 agent 的成果
	Error               string             `json:"error"`
	DiscussionClosedAt  *time.Time         `json:"discussion_closed_at,omitempty"`
	SwarmOrigin         bool               `json:"swarm_origin,omitempty"`         // true when created via PublishTask / PublishValidatedTask
	RequiredCaps        []string           `json:"required_caps,omitempty"`        // swarm: capabilities an agent must have to claim this task
	// Adversarial validation fields
	ValidationRequired  bool               `json:"validation_required,omitempty"`
	ValidationStatus    string             `json:"validation_status,omitempty"`    // ""|"passed"|"failed"
	ValidationScore     float64            `json:"validation_score,omitempty"`
	ValidationFeedback  string             `json:"validation_feedback,omitempty"`
	ValidationHistory   []ValidationAttempt `json:"validation_history,omitempty"`
	SourceTaskID        string             `json:"source_task_id,omitempty"`       // for validate tasks: the task under review
	OriginalDescription string             `json:"original_description,omitempty"` // preserved across retries
	RetryCount          int                `json:"retry_count,omitempty"`
	MaxRetries          int                `json:"max_retries,omitempty"`
	PassThreshold       float64            `json:"pass_threshold,omitempty"` // default 0.7
	RecoveryCount       int                `json:"recovery_count,omitempty"` // number of times this task has been automatically re-queued after an agent eviction
	// Pipeline fields — set when this task is part of a Pipeline
	PipelineID       string   `json:"pipeline_id,omitempty"`        // owning pipeline ID
	PipelineStepIdx  int      `json:"pipeline_step_idx,omitempty"`  // 0-based index within the pipeline
	NextStepTemplate string   `json:"next_step_template,omitempty"` // description template for the next step (empty = this is the last step)
	NextStepCaps     []string `json:"next_step_caps,omitempty"`     // required capabilities for the next step
}
