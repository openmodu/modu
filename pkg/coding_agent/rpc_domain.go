package coding_agent

// ForkMessage represents a user message available for forking.
type ForkMessage struct {
	EntryID string `json:"entryId"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SessionStats contains aggregate statistics for the current session.
type SessionStats struct {
	TotalTokens    int   `json:"totalTokens"`
	MessageCount   int   `json:"messageCount"`
	SessionStarted int64 `json:"sessionStarted"`
	DurationMs     int64 `json:"durationMs"`
}

// BashResult contains the result of executing a shell command.
type BashResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}
