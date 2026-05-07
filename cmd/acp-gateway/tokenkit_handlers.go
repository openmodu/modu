package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/openmodu/modu/pkg/tokenkit"
)

var tokenkitNow = time.Now

func (s *Server) requireTokenkit(w http.ResponseWriter) (*tokenkit.Store, bool) {
	if s.tokenkit == nil {
		http.Error(w, "tokenkit is disabled because gateway persistence is disabled", http.StatusServiceUnavailable)
		return nil, false
	}
	return s.tokenkit, true
}

func (s *Server) handleTokenkitOverview(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTokenkit(w)
	if !ok {
		return
	}
	apps := []string{tokenkit.AppCodex, tokenkit.AppClaudeCode, tokenkit.AppGemini}
	totals := make(map[string]tokenkit.SummaryRow, len(apps))
	startDate, endDate := tokenkitOverviewDateRange(r)
	for _, app := range apps {
		total, err := store.Totals(r.Context(), tokenkit.SummaryFilter{StartDate: startDate, EndDate: endDate, App: app})
		if err != nil {
			http.Error(w, fmt.Sprintf("query tokenkit totals: %v", err), http.StatusInternalServerError)
			return
		}
		totals[app] = total
	}

	records, err := store.UsageRecords(r.Context(), tokenkit.UsageRecordFilter{
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("query tokenkit records: %v", err), http.StatusInternalServerError)
		return
	}
	projects, sessions := buildTokenkitScopedUsage(records, s.store.TokenkitScopeSnapshot())
	timeline, err := store.DailyUsage(r.Context(), tokenkit.SummaryFilter{
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("query tokenkit timeline: %v", err), http.StatusInternalServerError)
		return
	}
	limit := intQuery(r, "limit", 20)
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	codexStatus, err := store.LatestCodexStatus(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("query codex status: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sync":        s.currentTokenkitSyncStatus(),
		"totals":      totals,
		"projects":    projects,
		"sessions":    sessions,
		"timeline":    timeline,
		"records":     records,
		"codexStatus": codexStatus,
	})
}

func tokenkitOverviewDateRange(r *http.Request) (string, string) {
	startDate := q(r, "start")
	endDate := q(r, "end")
	days := intQuery(r, "days", 0)
	if days <= 0 {
		return startDate, endDate
	}
	if days > 366 {
		days = 366
	}
	today := tokenkitNow()
	if endDate == "" {
		endDate = today.Format(time.DateOnly)
	}
	if startDate == "" {
		startDate = today.AddDate(0, 0, -(days - 1)).Format(time.DateOnly)
	}
	return startDate, endDate
}

func (s *Server) handleTokenkitScan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireTokenkit(w); !ok {
		return
	}
	q := r.URL.Query()
	target := q.Get("target")
	if target == "" {
		target = "all"
	}
	opts := s.tokenkitScannerOptions
	if tz := q.Get("timezone"); tz != "" {
		loaded, err := time.LoadLocation(tz)
		if err != nil {
			http.Error(w, "invalid timezone", http.StatusBadRequest)
			return
		}
		opts.Location = loaded
	}
	if value := q.Get("codexHome"); value != "" {
		opts.CodexHome = value
	}
	if value := q.Get("claudeHome"); value != "" {
		opts.ClaudeHome = value
	}
	if value := q.Get("geminiTelemetryLog"); value != "" {
		opts.GeminiTelemetryLog = value
	}

	stats, err := s.runTokenkitScan(r.Context(), target, opts)
	if err != nil {
		if err.Error() == "target must be all, codex, claude-code, or gemini" {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, fmt.Sprintf("scan tokenkit: %v", err), http.StatusInternalServerError)
		return
	}
	if len(stats) == 0 {
		http.Error(w, "target must be all, codex, claude-code, or gemini", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": target, "stats": stats})
}

func (s *Server) handleTokenkitRecords(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTokenkit(w)
	if !ok {
		return
	}
	records, err := store.UsageRecords(r.Context(), tokenkitUsageRecordFilter(r))
	if err != nil {
		http.Error(w, fmt.Sprintf("query tokenkit records: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records})
}

func (s *Server) handleTokenkitSaveCodexStatus(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTokenkit(w)
	if !ok {
		return
	}
	var body struct {
		Text       string `json:"text"`
		Raw        string `json:"raw"`
		CapturedAt string `json:"capturedAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	text := firstNonEmpty(body.Text, body.Raw)
	if text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	capturedAt := time.Now()
	if body.CapturedAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, body.CapturedAt)
		if err != nil {
			http.Error(w, "invalid capturedAt", http.StatusBadRequest)
			return
		}
		capturedAt = parsed
	}
	snapshot := tokenkit.ParseCodexStatus(text, capturedAt)
	if err := store.SaveCodexStatus(r.Context(), snapshot); err != nil {
		http.Error(w, fmt.Sprintf("save codex status: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, snapshot)
}

func (s *Server) handleTokenkitLatestCodexStatus(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTokenkit(w)
	if !ok {
		return
	}
	snapshot, err := store.LatestCodexStatus(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("query codex status: %v", err), http.StatusInternalServerError)
		return
	}
	if snapshot == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": snapshot})
}

func tokenkitSummaryFilter(r *http.Request) tokenkit.SummaryFilter {
	return tokenkit.SummaryFilter{
		StartDate: q(r, "start"),
		EndDate:   q(r, "end"),
		App:       q(r, "app"),
		Source:    q(r, "source"),
		Model:     q(r, "model"),
	}
}

func tokenkitUsageRecordFilter(r *http.Request) tokenkit.UsageRecordFilter {
	return tokenkit.UsageRecordFilter{
		StartDate: q(r, "start"),
		EndDate:   q(r, "end"),
		App:       q(r, "app"),
		Source:    q(r, "source"),
		Model:     q(r, "model"),
		Workspace: q(r, "workspace"),
		Limit:     intQuery(r, "limit", 100),
		Offset:    intQuery(r, "offset", 0),
		Ascending: boolQuery(r, "asc"),
	}
}

func q(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

func intQuery(r *http.Request, key string, fallback int) int {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func boolQuery(r *http.Request, key string) bool {
	value := r.URL.Query().Get(key)
	return value == "1" || value == "true" || value == "yes"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
