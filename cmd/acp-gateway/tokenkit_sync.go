package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/openmodu/modu/pkg/tokenkit"
)

type TokenkitSyncStatus struct {
	Enabled         bool                          `json:"enabled"`
	Running         bool                          `json:"running"`
	IntervalSeconds float64                       `json:"intervalSeconds,omitempty"`
	LastStartedAt   string                        `json:"lastStartedAt,omitempty"`
	LastFinishedAt  string                        `json:"lastFinishedAt,omitempty"`
	LastError       string                        `json:"lastError,omitempty"`
	LastErrors      map[string]string             `json:"lastErrors,omitempty"`
	LastStats       map[string]tokenkit.ScanStats `json:"lastStats,omitempty"`
}

func (s *Server) runTokenkitSyncLoop(ctx context.Context, interval time.Duration) {
	s.scanTokenkitBackground(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanTokenkitBackground(ctx)
		}
	}
}

func (s *Server) scanTokenkitBackground(ctx context.Context) {
	stats, err := s.runTokenkitScan(ctx, "all", s.tokenkitScannerOptions)
	if err != nil && ctx.Err() == nil {
		log.Printf("[acp-gateway] tokenkit sync failed: %v", err)
		return
	}
	if ctx.Err() == nil {
		log.Printf("[acp-gateway] tokenkit sync complete: codex=%+v claude-code=%+v gemini=%+v",
			stats[tokenkit.AppCodex],
			stats[tokenkit.AppClaudeCode],
			stats[tokenkit.AppGemini],
		)
	}
}

func (s *Server) runTokenkitScan(ctx context.Context, target string, opts tokenkit.ScannerOptions) (map[string]tokenkit.ScanStats, error) {
	if s.tokenkit == nil {
		return nil, fmt.Errorf("tokenkit is disabled")
	}

	s.tokenkitScanMu.Lock()
	defer s.tokenkitScanMu.Unlock()

	startedAt := time.Now().UTC()
	s.updateTokenkitSyncStatus(func(st *TokenkitSyncStatus) {
		st.Running = true
		st.LastStartedAt = startedAt.Format(time.RFC3339Nano)
		st.LastError = ""
	})

	stats, scanErrors, err := s.scanTokenkitTarget(ctx, target, opts)

	finishedAt := time.Now().UTC()
	s.updateTokenkitSyncStatus(func(st *TokenkitSyncStatus) {
		st.Running = false
		st.LastFinishedAt = finishedAt.Format(time.RFC3339Nano)
		st.LastStats = stats
		st.LastErrors = scanErrors
		if err != nil {
			st.LastError = err.Error()
		}
	})
	return stats, err
}

func (s *Server) scanTokenkitTarget(ctx context.Context, target string, opts tokenkit.ScannerOptions) (map[string]tokenkit.ScanStats, map[string]string, error) {
	scanner := tokenkit.NewScanner(s.tokenkit, opts)
	stats := map[string]tokenkit.ScanStats{}
	scanErrors := map[string]string{}
	var err error
	switch target {
	case "", "all":
		stats[tokenkit.AppCodex], err = scanner.ScanCodex(ctx)
		if err != nil {
			scanErrors[tokenkit.AppCodex] = err.Error()
		}
		stats[tokenkit.AppClaudeCode], err = scanner.ScanClaudeCode(ctx)
		if err != nil {
			scanErrors[tokenkit.AppClaudeCode] = err.Error()
		}
		stats[tokenkit.AppGemini], err = scanner.ScanGemini(ctx)
		if err != nil {
			scanErrors[tokenkit.AppGemini] = err.Error()
		}
		err = nil
	case tokenkit.AppCodex:
		stats[tokenkit.AppCodex], err = scanner.ScanCodex(ctx)
	case tokenkit.AppClaudeCode, "claude":
		stats[tokenkit.AppClaudeCode], err = scanner.ScanClaudeCode(ctx)
	case tokenkit.AppGemini:
		stats[tokenkit.AppGemini], err = scanner.ScanGemini(ctx)
	default:
		err = fmt.Errorf("target must be all, codex, claude-code, or gemini")
	}
	if len(scanErrors) == 0 {
		scanErrors = nil
	}
	return stats, scanErrors, err
}

func (s *Server) updateTokenkitSyncStatus(update func(*TokenkitSyncStatus)) {
	s.tokenkitStatusMu.Lock()
	defer s.tokenkitStatusMu.Unlock()
	if s.tokenkitSyncStatus.LastStats == nil {
		s.tokenkitSyncStatus.LastStats = map[string]tokenkit.ScanStats{}
	}
	update(&s.tokenkitSyncStatus)
}

func (s *Server) currentTokenkitSyncStatus() TokenkitSyncStatus {
	s.tokenkitStatusMu.RLock()
	defer s.tokenkitStatusMu.RUnlock()
	out := s.tokenkitSyncStatus
	if out.LastStats != nil {
		out.LastStats = cloneTokenkitStats(out.LastStats)
	}
	if out.LastErrors != nil {
		out.LastErrors = cloneStringMap(out.LastErrors)
	}
	return out
}

func cloneTokenkitStats(in map[string]tokenkit.ScanStats) map[string]tokenkit.ScanStats {
	out := make(map[string]tokenkit.ScanStats, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
