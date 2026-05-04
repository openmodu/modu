package tokenkit

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

type Scanner struct {
	store *Store
	opts  ScannerOptions
}

func NewScanner(store *Store, opts ScannerOptions) *Scanner {
	return &Scanner{store: store, opts: opts}
}

func (s *Scanner) ScanAll(ctx context.Context) (ScanStats, error) {
	var total ScanStats
	if stats, err := s.ScanCodex(ctx); err != nil {
		return total, err
	} else {
		total = total.Add(stats)
	}
	if stats, err := s.ScanClaudeCode(ctx); err != nil {
		return total, err
	} else {
		total = total.Add(stats)
	}
	if stats, err := s.ScanGemini(ctx); err != nil {
		return total, err
	} else {
		total = total.Add(stats)
	}
	return total, nil
}

func (s *Scanner) ScanCodex(ctx context.Context) (ScanStats, error) {
	home := s.opts.CodexHome
	if home == "" {
		home = filepath.Join(userHomeDir(), ".codex")
	}
	return ScanCodex(ctx, s.store, home, defaultLocation(s.opts.Location))
}

func (s *Scanner) ScanClaudeCode(ctx context.Context) (ScanStats, error) {
	home := s.opts.ClaudeHome
	if home == "" {
		home = filepath.Join(userHomeDir(), ".claude")
	}
	return ScanClaudeCode(ctx, s.store, home, defaultLocation(s.opts.Location))
}

func (s *Scanner) ScanGemini(ctx context.Context) (ScanStats, error) {
	path := s.opts.GeminiTelemetryLog
	if path == "" {
		path = firstExistingPath(
			filepath.Join(".gemini", "telemetry.log"),
			filepath.Join(userHomeDir(), ".gemini", "telemetry.log"),
		)
	}
	if path == "" {
		return ScanStats{}, nil
	}
	return ScanGeminiTelemetry(ctx, s.store, path, defaultLocation(s.opts.Location))
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func modTimeUnixNS(info os.FileInfo) int64 {
	return info.ModTime().UnixNano()
}

func sameFileState(state *FileScanState, info os.FileInfo) bool {
	return state != nil && state.FileSize == info.Size() && state.ModTimeUnixNS == modTimeUnixNS(info)
}

func canContinueFromOffset(state *FileScanState, info os.FileInfo) bool {
	return state != nil && info.Size() > state.FileSize && state.Offset <= state.FileSize
}

func newFileState(app, path string, offset int64, info os.FileInfo, metadata map[string]any) FileScanState {
	return FileScanState{
		StateKey:      stateKeyForFile(app, path),
		App:           app,
		FilePath:      resolvePath(path),
		Offset:        offset,
		FileSize:      info.Size(),
		ModTimeUnixNS: modTimeUnixNS(info),
		LastScannedAt: time.Now().UTC(),
		Metadata:      metadata,
	}
}
