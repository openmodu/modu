package tokenkit

import (
	"strings"
	"time"
)

const (
	AppCodex      = "codex"
	AppClaudeCode = "claude-code"
	AppGemini     = "gemini"

	MethodExact     = "exact"
	MethodPartial   = "partial"
	MethodEstimated = "estimated"
)

type UsageRecord struct {
	ID                int64          `json:"id,omitempty"`
	Source            string         `json:"source"`
	App               string         `json:"app"`
	ExternalID        string         `json:"externalId"`
	StartedAt         time.Time      `json:"startedAt"`
	LocalDate         string         `json:"localDate"`
	MeasurementMethod string         `json:"measurementMethod"`
	Model             string         `json:"model,omitempty"`
	InputTokens       int            `json:"inputTokens"`
	OutputTokens      int            `json:"outputTokens"`
	CachedInputTokens int            `json:"cachedInputTokens"`
	ReasoningTokens   int            `json:"reasoningTokens"`
	ToolTokens        int            `json:"toolTokens"`
	UnsplitTokens     int            `json:"unsplitTokens"`
	TotalTokens       int            `json:"totalTokens"`
	Credits           float64        `json:"credits,omitempty"`
	Category          string         `json:"category,omitempty"`
	Workspace         string         `json:"workspace,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	CreatedAt         time.Time      `json:"createdAt,omitempty"`
}

type FileScanState struct {
	StateKey      string         `json:"stateKey"`
	App           string         `json:"app"`
	FilePath      string         `json:"filePath"`
	Offset        int64          `json:"offset"`
	FileSize      int64          `json:"fileSize"`
	ModTimeUnixNS int64          `json:"modTimeUnixNs"`
	LastScannedAt time.Time      `json:"lastScannedAt"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type ScanStats struct {
	FilesScanned int `json:"filesScanned"`
	RecordsSeen  int `json:"recordsSeen"`
}

func (s ScanStats) Add(other ScanStats) ScanStats {
	s.FilesScanned += other.FilesScanned
	s.RecordsSeen += other.RecordsSeen
	return s
}

type ScannerOptions struct {
	CodexHome          string
	ClaudeHome         string
	GeminiTelemetryLog string
	Location           *time.Location
}

type SummaryFilter struct {
	StartDate string
	EndDate   string
	App       string
	Source    string
	Model     string
}

type UsageRecordFilter struct {
	StartDate string
	EndDate   string
	App       string
	Source    string
	Model     string
	Workspace string
	Limit     int
	Offset    int
	Ascending bool
}

type CodexStatusSnapshot struct {
	ID                int64              `json:"id,omitempty"`
	CapturedAt        time.Time          `json:"capturedAt"`
	Version           string             `json:"version,omitempty"`
	UsageURL          string             `json:"usageUrl,omitempty"`
	Model             string             `json:"model,omitempty"`
	Reasoning         string             `json:"reasoning,omitempty"`
	Summaries         string             `json:"summaries,omitempty"`
	Directory         string             `json:"directory,omitempty"`
	Permissions       string             `json:"permissions,omitempty"`
	AgentsFile        string             `json:"agentsFile,omitempty"`
	AccountEmail      string             `json:"accountEmail,omitempty"`
	AccountPlan       string             `json:"accountPlan,omitempty"`
	CollaborationMode string             `json:"collaborationMode,omitempty"`
	SessionID         string             `json:"sessionId,omitempty"`
	ContextWindow     CodexContextWindow `json:"contextWindow"`
	Limits            []CodexLimit       `json:"limits,omitempty"`
	Warning           string             `json:"warning,omitempty"`
	RawText           string             `json:"rawText,omitempty"`
}

type CodexContextWindow struct {
	PercentLeft float64 `json:"percentLeft"`
	UsedTokens  int     `json:"usedTokens"`
	MaxTokens   int     `json:"maxTokens"`
	Raw         string  `json:"raw,omitempty"`
}

type CodexLimit struct {
	Model       string  `json:"model,omitempty"`
	Window      string  `json:"window"`
	PercentLeft float64 `json:"percentLeft"`
	ResetRaw    string  `json:"resetRaw,omitempty"`
	Raw         string  `json:"raw,omitempty"`
}

func (r UsageRecord) SessionID() string {
	if r.Metadata == nil {
		return ""
	}
	for _, key := range []string{"gateway_session_id", "session_id"} {
		if v, ok := r.Metadata[key]; ok {
			if s, ok := v.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					return s
				}
			}
		}
	}
	return ""
}

type SummaryRow struct {
	LocalDate         string   `json:"localDate,omitempty"`
	App               string   `json:"app,omitempty"`
	Source            string   `json:"source,omitempty"`
	Model             string   `json:"model,omitempty"`
	MeasurementMethod string   `json:"measurementMethod,omitempty"`
	InputTokens       int      `json:"inputTokens"`
	OutputTokens      int      `json:"outputTokens"`
	CachedInputTokens int      `json:"cachedInputTokens"`
	ReasoningTokens   int      `json:"reasoningTokens"`
	ToolTokens        int      `json:"toolTokens"`
	UnsplitTokens     int      `json:"unsplitTokens"`
	TotalTokens       int      `json:"totalTokens"`
	Credits           float64  `json:"credits,omitempty"`
	Records           int      `json:"records"`
	EstimatedCostUSD  *float64 `json:"estimatedCostUsd,omitempty"`
}

func (r *SummaryRow) Accumulate(rec UsageRecord) {
	r.InputTokens += rec.InputTokens
	r.OutputTokens += rec.OutputTokens
	r.CachedInputTokens += rec.CachedInputTokens
	r.ReasoningTokens += rec.ReasoningTokens
	r.ToolTokens += rec.ToolTokens
	r.UnsplitTokens += rec.UnsplitTokens
	r.TotalTokens += rec.TotalTokens
	r.Credits += rec.Credits
	r.Records++
}

type TimeBucketRow struct {
	LocalDate         string   `json:"localDate"`
	App               string   `json:"app,omitempty"`
	InputTokens       int      `json:"inputTokens"`
	OutputTokens      int      `json:"outputTokens"`
	CachedInputTokens int      `json:"cachedInputTokens"`
	ReasoningTokens   int      `json:"reasoningTokens"`
	ToolTokens        int      `json:"toolTokens"`
	UnsplitTokens     int      `json:"unsplitTokens"`
	TotalTokens       int      `json:"totalTokens"`
	Credits           float64  `json:"credits,omitempty"`
	Records           int      `json:"records"`
	EstimatedCostUSD  *float64 `json:"estimatedCostUsd,omitempty"`
}
