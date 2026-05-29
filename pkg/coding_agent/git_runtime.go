package coding_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func (s *CodingSession) gitRuntimeState() map[string]any {
	return s.gitRuntimeStateForCwd(s.cwd)
}

func (s *CodingSession) gitRuntimeStateForCwd(cwd string) map[string]any {
	state, err := inspectGitRuntimeState(context.Background(), cwd)
	if err != nil {
		return map[string]any{"available": false}
	}
	data, _ := json.Marshal(state)
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return map[string]any{"available": false}
	}
	payload["available"] = true
	return payload
}

type gitRuntimeState struct {
	RepoRoot        string            `json:"repoRoot"`
	InGitRepository bool              `json:"inGitRepository"`
	StagedFiles     []gitStatusEntry  `json:"stagedFiles,omitempty"`
	UnstagedFiles   []gitStatusEntry  `json:"unstagedFiles,omitempty"`
	UntrackedFiles  []string          `json:"untrackedFiles,omitempty"`
	StagedStats     gitDiffStats      `json:"stagedStats"`
	UnstagedStats   gitDiffStats      `json:"unstagedStats"`
	LastCommit      *gitCommitSummary `json:"lastCommit,omitempty"`
}

type gitStatusEntry struct {
	Path     string `json:"path"`
	X        string `json:"x"`
	Y        string `json:"y"`
	Staged   bool   `json:"staged"`
	Unstaged bool   `json:"unstaged"`
}

type gitDiffStats struct {
	Files      int `json:"files"`
	Insertions int `json:"insertions"`
	Deletions  int `json:"deletions"`
}

type gitCommitSummary struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

func inspectGitRuntimeState(ctx context.Context, cwd string) (gitRuntimeState, error) {
	state := gitRuntimeState{}
	root, err := gitRuntimeOutput(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return state, err
	}
	state.InGitRepository = true
	state.RepoRoot = strings.TrimSpace(root)

	if out, err := gitRuntimeOutput(ctx, cwd, "status", "--short"); err == nil {
		parseGitRuntimeStatus(strings.TrimSpace(out), &state)
	}
	if out, err := gitRuntimeOutput(ctx, cwd, "diff", "--numstat"); err == nil {
		state.UnstagedStats = parseGitRuntimeDiffStats(strings.TrimSpace(out))
	}
	if out, err := gitRuntimeOutput(ctx, cwd, "diff", "--cached", "--numstat"); err == nil {
		state.StagedStats = parseGitRuntimeDiffStats(strings.TrimSpace(out))
	}
	if out, err := gitRuntimeOutput(ctx, cwd, "log", "-1", "--pretty=format:%H%x09%s"); err == nil && strings.TrimSpace(out) != "" {
		parts := strings.SplitN(strings.TrimSpace(out), "\t", 2)
		commit := &gitCommitSummary{Hash: parts[0]}
		if len(parts) > 1 {
			commit.Subject = parts[1]
		}
		state.LastCommit = commit
	}
	return state, nil
}

func gitRuntimeOutput(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func parseGitRuntimeStatus(out string, state *gitRuntimeState) {
	if state == nil || strings.TrimSpace(out) == "" {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 3 {
			continue
		}
		x := string(line[0])
		y := string(line[1])
		path := strings.TrimSpace(line[3:])
		if x == "?" && y == "?" {
			state.UntrackedFiles = append(state.UntrackedFiles, path)
			continue
		}
		entry := gitStatusEntry{
			Path:     path,
			X:        x,
			Y:        y,
			Staged:   x != " " && x != "?",
			Unstaged: y != " " && y != "?",
		}
		if entry.Staged {
			state.StagedFiles = append(state.StagedFiles, entry)
		}
		if entry.Unstaged {
			state.UnstagedFiles = append(state.UnstagedFiles, entry)
		}
	}
}

func parseGitRuntimeDiffStats(out string) gitDiffStats {
	stats := gitDiffStats{}
	if strings.TrimSpace(out) == "" {
		return stats
	}
	seen := make(map[string]struct{})
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if _, ok := seen[fields[2]]; !ok {
			seen[fields[2]] = struct{}{}
			stats.Files++
		}
		if fields[0] != "-" {
			if n, err := strconv.Atoi(fields[0]); err == nil {
				stats.Insertions += n
			}
		}
		if fields[1] != "-" {
			if n, err := strconv.Atoi(fields[1]); err == nil {
				stats.Deletions += n
			}
		}
	}
	return stats
}
