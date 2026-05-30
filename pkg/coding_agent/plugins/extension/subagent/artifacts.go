package subagent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// artifactRun captures the on-disk debug artifacts for one subagent
// dispatch. When the caller sets `artifacts: true`, we write
//
//	input.json    — the args that drove the dispatch
//	output.json   — the final result text + any error
//	metadata.json — timing + mode + run id
//
// under tool-results/<project>/subagents/artifacts/<runID>/.
//
// The artifact set mirrors pi-subagents' per-run debug layout but uses our
// runtime dir convention so the harness's existing tool-results helpers
// don't collide.
type artifactRun struct {
	id        string
	dir       string
	mode      string
	startedAt time.Time
}

// isArtifactsRequested returns true when the top-level call set
// artifacts: true. Missing / non-boolean values default to false so the
// extra IO only happens on opt-in.
func isArtifactsRequested(args map[string]any) bool {
	v, _ := args["artifacts"].(bool)
	return v
}

// startArtifactRun reserves a fresh artifact directory and writes
// input.json immediately. runIDHint is used verbatim when non-empty (the
// batch async path passes its synthetic batch id so caller-visible and
// on-disk identifiers line up); otherwise a timestamp+random id is
// generated.
func startArtifactRun(ext *Extension, runIDHint string, mode string, args map[string]any) (*artifactRun, error) {
	id := strings.TrimSpace(runIDHint)
	if id == "" {
		id = generateArtifactRunID()
	}
	dir, err := artifactRunDir(ext, id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact dir %s: %w", dir, err)
	}
	run := &artifactRun{
		id:        id,
		dir:       dir,
		mode:      mode,
		startedAt: time.Now(),
	}
	if err := writeJSONFile(filepath.Join(dir, "input.json"), map[string]any{
		"runID": id,
		"mode":  mode,
		"args":  redactArgsForArtifact(args),
		"at":    run.startedAt.UnixMilli(),
	}); err != nil {
		return nil, fmt.Errorf("write input.json: %w", err)
	}
	return run, nil
}

// complete finalises the artifact set by writing output.json and
// metadata.json. A nil run is a no-op so callers can guard with `if run !=
// nil` only where they care to.
func (a *artifactRun) complete(text string, dispatchErr error) error {
	if a == nil {
		return nil
	}
	finishedAt := time.Now()
	out := map[string]any{
		"runID":  a.id,
		"output": text,
		"at":     finishedAt.UnixMilli(),
	}
	if dispatchErr != nil {
		out["error"] = dispatchErr.Error()
	}
	if err := writeJSONFile(filepath.Join(a.dir, "output.json"), out); err != nil {
		return fmt.Errorf("write output.json: %w", err)
	}
	meta := map[string]any{
		"runID":      a.id,
		"mode":       a.mode,
		"startedAt":  a.startedAt.UnixMilli(),
		"finishedAt": finishedAt.UnixMilli(),
		"durationMs": finishedAt.Sub(a.startedAt).Milliseconds(),
		"status":     statusFromError(dispatchErr),
	}
	if err := writeJSONFile(filepath.Join(a.dir, "metadata.json"), meta); err != nil {
		return fmt.Errorf("write metadata.json: %w", err)
	}
	return nil
}

// path returns the absolute artifact directory for this run, useful when
// the tool result wants to point the caller at the on-disk debug bundle.
func (a *artifactRun) path() string {
	if a == nil {
		return ""
	}
	return a.dir
}

// artifactRunDir resolves the parent directory for a run id. Mirrors
// progressFilePath's host fallback so the artifacts dir lands under the
// same project key.
func artifactRunDir(ext *Extension, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("artifact run id required")
	}
	if ext == nil || ext.api == nil {
		return "", fmt.Errorf("artifact dir requires the host API")
	}
	return filepath.Join(ext.api.AgentDir(), "tool-results", projectKey(ext.api.Cwd()), "subagents", "artifacts", id), nil
}

// generateArtifactRunID picks a unique id with both a timestamp prefix
// (so directory listings sort by time) and a short random suffix (so two
// runs in the same millisecond don't collide).
func generateArtifactRunID() string {
	ts := time.Now().Format("20060102T150405.000")
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand should never fail on a sane OS; if it does, fall
		// back to a nanosecond suffix so the id is still unique enough.
		return fmt.Sprintf("run-%s-%d", strings.ReplaceAll(ts, ".", "-"), time.Now().UnixNano())
	}
	return fmt.Sprintf("run-%s-%s", strings.ReplaceAll(ts, ".", "-"), hex.EncodeToString(raw[:]))
}

// redactArgsForArtifact strips the fields the artifact writer is not
// interested in re-serialising (already-decoded callbacks, etc.). Today
// it's a shallow copy with no real redaction — kept as a seam in case we
// need to scrub secrets later.
func redactArgsForArtifact(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

func statusFromError(err error) string {
	if err == nil {
		return "completed"
	}
	return "failed"
}

func writeJSONFile(path string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
