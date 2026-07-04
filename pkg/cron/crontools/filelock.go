package crontools

import (
	"os"
	"path/filepath"
	"syscall"
)

// acquireFileLock takes an exclusive advisory flock on path (created if
// missing) and returns a release func. It serializes read-modify-write
// cycles on the task file across processes — the `modu_code cron daemon`
// process and any modu_code session with the cron extension all write the
// same YAML.
//
// Unix-only (uses syscall.Flock). The rest of this module (via
// pkg/coding_agent/tools/bash) already doesn't build on Windows, so there's
// no cross-platform fallback to maintain here.
func acquireFileLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
