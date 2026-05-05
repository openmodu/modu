package tokenkit

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
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
	var mu sync.Mutex
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		stats, err := s.ScanCodex(ctx)
		if err == nil {
			mu.Lock()
			total = total.Add(stats)
			mu.Unlock()
		}
		return err
	})

	g.Go(func() error {
		stats, err := s.ScanClaudeCode(ctx)
		if err == nil {
			mu.Lock()
			total = total.Add(stats)
			mu.Unlock()
		}
		return err
	})

	g.Go(func() error {
		stats, err := s.ScanGemini(ctx)
		if err == nil {
			mu.Lock()
			total = total.Add(stats)
			mu.Unlock()
		}
		return err
	})

	if err := g.Wait(); err != nil {
		return total, err
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
	home := filepath.Join(userHomeDir(), ".gemini")
	path := s.opts.GeminiTelemetryLog
	if path == "" {
		path = firstExistingPath(
			filepath.Join(".gemini", "telemetry.log"),
			filepath.Join(home, "telemetry.log"),
		)
	}
	return ScanGemini(ctx, s.store, home, path, defaultLocation(s.opts.Location))
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
