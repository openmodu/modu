package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

type GitPreflightTool struct {
	cwd string
}

func NewGitPreflightTool(cwd string) *GitPreflightTool {
	return &GitPreflightTool{cwd: cwd}
}

func (t *GitPreflightTool) Name() string  { return "git_preflight" }
func (t *GitPreflightTool) Label() string { return "Git Preflight" }
func (t *GitPreflightTool) Description() string {
	return "Inspect the current git repository and return structured staged/unstaged status, diff stats, and last commit information. Use this before claiming files are staged, committed, or unchanged."
}
func (t *GitPreflightTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

type GitPreflightState struct {
	RepoRoot        string            `json:"repoRoot"`
	InGitRepository bool              `json:"inGitRepository"`
	StagedFiles     []GitStatusEntry  `json:"stagedFiles,omitempty"`
	UnstagedFiles   []GitStatusEntry  `json:"unstagedFiles,omitempty"`
	UntrackedFiles  []string          `json:"untrackedFiles,omitempty"`
	StagedStats     GitDiffStats      `json:"stagedStats"`
	UnstagedStats   GitDiffStats      `json:"unstagedStats"`
	LastCommit      *GitCommitSummary `json:"lastCommit,omitempty"`
}

type GitStatusEntry struct {
	Path     string `json:"path"`
	X        string `json:"x"`
	Y        string `json:"y"`
	Staged   bool   `json:"staged"`
	Unstaged bool   `json:"unstaged"`
}

type GitDiffStats struct {
	Files      int `json:"files"`
	Insertions int `json:"insertions"`
	Deletions  int `json:"deletions"`
}

type GitCommitSummary struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

func (t *GitPreflightTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	state := GitPreflightState{}
	root, err := t.gitOutput(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		text := "Not a git repository."
		return agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
			Details: map[string]any{"inGitRepository": false},
		}, nil
	}
	state.InGitRepository = true
	state.RepoRoot = strings.TrimSpace(root)

	if out, err := t.gitOutput(ctx, "status", "--short"); err == nil {
		parseGitStatus(strings.TrimSpace(out), &state)
	}
	if out, err := t.gitOutput(ctx, "diff", "--numstat"); err == nil {
		state.UnstagedStats = parseGitDiffStats(strings.TrimSpace(out))
	}
	if out, err := t.gitOutput(ctx, "diff", "--cached", "--numstat"); err == nil {
		state.StagedStats = parseGitDiffStats(strings.TrimSpace(out))
	}
	if out, err := t.gitOutput(ctx, "log", "-1", "--pretty=format:%H%x09%s"); err == nil && strings.TrimSpace(out) != "" {
		parts := strings.SplitN(strings.TrimSpace(out), "\t", 2)
		commit := &GitCommitSummary{Hash: parts[0]}
		if len(parts) > 1 {
			commit.Subject = parts[1]
		}
		state.LastCommit = commit
	}

	data, _ := json.MarshalIndent(state, "", "  ")
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: string(data)}},
		Details: map[string]any{
			"inGitRepository": state.InGitRepository,
			"repoRoot":        state.RepoRoot,
			"stagedFiles":     len(state.StagedFiles),
			"unstagedFiles":   len(state.UnstagedFiles),
			"untrackedFiles":  len(state.UntrackedFiles),
		},
	}, nil
}

func (t *GitPreflightTool) gitOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = t.cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func parseGitStatus(out string, state *GitPreflightState) {
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
		entry := GitStatusEntry{
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

func parseGitDiffStats(out string) GitDiffStats {
	stats := GitDiffStats{}
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

func gitProjectKey(cwd string) string {
	return strings.ReplaceAll(strings.TrimPrefix(filepath.Clean(cwd), "/"), "/", "_")
}
