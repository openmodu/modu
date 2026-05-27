package subagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/extension"
)

// staleStatus is the status string we write to a reconciled status.json. It
// signals "the prior session was running this task when it terminated".
const staleStatus = "stale"

// staleReason is the error string we attach when reconciling, so callers
// looking at task.Error get a hint about what happened.
const staleReason = "abandoned (session restart or process exit)"

// reconcileStaleTasks scans the host's recovered background tasks at Init
// time. Any subagent task that still reports `running` is by definition
// stale — the goroutine that drove it died with the previous session, and
// the new host process has nothing keeping it alive.
//
// For each stale task, we rewrite its status.json so the next session
// startup sees it as `stale` (the in-memory copy in the current session
// keeps its loaded status — we overlay it via the returned set when
// formatting status / doctor).
//
// Returns the set of task IDs that were reconciled.
func reconcileStaleTasks(api extension.ExtensionAPI) map[string]bool {
	if api == nil {
		return nil
	}
	stale := map[string]bool{}
	for _, task := range api.BackgroundTasks() {
		if task.Kind != "subagent" {
			continue
		}
		if !isRunningStatus(task.Status) {
			continue
		}
		stale[task.ID] = true
		_ = writeStaleStatusFile(task)
	}
	return stale
}

func isRunningStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "pending", "starting":
		return true
	default:
		return false
	}
}

// writeStaleStatusFile rewrites the on-disk status.json with status=stale.
// It preserves every other field the host already knows about so a later
// reader still sees the agent name, task text, parent linkage, and so on.
// Best effort: a write failure is ignored — the in-memory staleSet still
// keeps the current session honest, and the next attempt at reconciliation
// will try again.
func writeStaleStatusFile(task extension.TaskSnapshot) error {
	if strings.TrimSpace(task.StatusFile) == "" {
		return nil
	}
	existing := map[string]any{}
	if data, err := os.ReadFile(task.StatusFile); err == nil {
		_ = json.Unmarshal(data, &existing)
	}
	// Seed the document if it's empty so we still produce a valid record
	// even when the original file is missing or unreadable.
	if existing["id"] == nil {
		existing["id"] = task.ID
	}
	if existing["kind"] == nil {
		existing["kind"] = task.Kind
	}
	if existing["agent"] == nil && task.Agent != "" {
		existing["agent"] = task.Agent
	}
	if existing["task"] == nil && task.Task != "" {
		existing["task"] = task.Task
	}
	if existing["runDir"] == nil && task.RunDir != "" {
		existing["runDir"] = task.RunDir
	}
	if existing["statusFile"] == nil {
		existing["statusFile"] = task.StatusFile
	}
	if existing["sessionFile"] == nil && task.SessionFile != "" {
		existing["sessionFile"] = task.SessionFile
	}
	existing["status"] = staleStatus
	existing["error"] = staleReason
	existing["updatedAt"] = time.Now().UnixMilli()
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(task.StatusFile), 0o755); err != nil {
		return err
	}
	tmp := task.StatusFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, task.StatusFile)
}
