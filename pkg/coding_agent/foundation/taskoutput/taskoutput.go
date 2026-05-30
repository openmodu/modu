package taskoutput

// Task tracks an asynchronous background run.
type Task struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Summary     string `json:"summary"`
	Agent       string `json:"agent,omitempty"`
	Task        string `json:"task,omitempty"`
	ParentID    string `json:"parentId,omitempty"`
	RunDir      string `json:"runDir,omitempty"`
	StatusFile  string `json:"statusFile,omitempty"`
	SessionFile string `json:"sessionFile,omitempty"`
	OutputFile  string `json:"outputFile,omitempty"`
	Output      string `json:"output,omitempty"`
	Error       string `json:"error,omitempty"`
	CreatedAt   int64  `json:"createdAt,omitempty"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
}

// Store manages background task lifecycle and snapshots.
type Store interface {
	Create(kind, summary string) string
	Complete(id, output string)
	Fail(id, errMsg string)
	Get(id string) (Task, bool)
	List() []Task
}
