package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/openmodu/modu/pkg/tokenkit"
)

func (s *Server) requireTokenkit(w http.ResponseWriter) (*tokenkit.Store, bool) {
	if s.tokenkit == nil {
		http.Error(w, "tokenkit is disabled because gateway persistence is disabled", http.StatusServiceUnavailable)
		return nil, false
	}
	return s.tokenkit, true
}

func (s *Server) handleTokenkitScan(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTokenkit(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	target := q.Get("target")
	if target == "" {
		target = "all"
	}
	loc := time.Local
	if tz := q.Get("timezone"); tz != "" {
		loaded, err := time.LoadLocation(tz)
		if err != nil {
			http.Error(w, "invalid timezone", http.StatusBadRequest)
			return
		}
		loc = loaded
	}
	home, _ := os.UserHomeDir()
	opts := tokenkit.ScannerOptions{
		CodexHome:          firstNonEmpty(q.Get("codexHome"), filepath.Join(home, ".codex")),
		ClaudeHome:         firstNonEmpty(q.Get("claudeHome"), filepath.Join(home, ".claude")),
		GeminiTelemetryLog: q.Get("geminiTelemetryLog"),
		Location:           loc,
	}
	scanner := tokenkit.NewScanner(store, opts)

	ctx := r.Context()
	stats := map[string]tokenkit.ScanStats{}
	var err error
	switch target {
	case "all":
		stats["codex"], err = scanner.ScanCodex(ctx)
		if err == nil {
			stats["claude-code"], err = scanner.ScanClaudeCode(ctx)
		}
		if err == nil {
			stats["gemini"], err = scanner.ScanGemini(ctx)
		}
	case tokenkit.AppCodex:
		stats["codex"], err = scanner.ScanCodex(ctx)
	case tokenkit.AppClaudeCode, "claude":
		stats["claude-code"], err = scanner.ScanClaudeCode(ctx)
	case tokenkit.AppGemini:
		stats["gemini"], err = scanner.ScanGemini(ctx)
	default:
		http.Error(w, "target must be all, codex, claude-code, or gemini", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("scan tokenkit: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": target, "stats": stats})
}

func (s *Server) handleTokenkitRecords(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTokenkit(w)
	if !ok {
		return
	}
	records, err := store.UsageRecords(r.Context(), tokenkit.UsageRecordFilter{
		StartDate: q(r, "start"),
		EndDate:   q(r, "end"),
		App:       q(r, "app"),
		Source:    q(r, "source"),
		Model:     q(r, "model"),
		Workspace: q(r, "workspace"),
		Limit:     intQuery(r, "limit", 100),
		Offset:    intQuery(r, "offset", 0),
		Ascending: boolQuery(r, "asc"),
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("query tokenkit records: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records})
}

func (s *Server) handleTokenkitTotals(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTokenkit(w)
	if !ok {
		return
	}
	totals, err := store.Totals(r.Context(), tokenkit.SummaryFilter{
		StartDate: q(r, "start"),
		EndDate:   q(r, "end"),
		App:       q(r, "app"),
		Source:    q(r, "source"),
		Model:     q(r, "model"),
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("query tokenkit totals: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, totals)
}

func (s *Server) handleTokenkitSummaries(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireTokenkit(w)
	if !ok {
		return
	}
	summaries, err := store.Summaries(r.Context(), tokenkit.SummaryFilter{
		StartDate: q(r, "start"),
		EndDate:   q(r, "end"),
		App:       q(r, "app"),
		Source:    q(r, "source"),
		Model:     q(r, "model"),
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("query tokenkit summaries: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summaries": summaries})
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
