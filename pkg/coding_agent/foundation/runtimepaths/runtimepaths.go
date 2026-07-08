package runtimepaths

import (
	"path/filepath"
	"strings"
)

func ProjectKey(cwd string) string {
	projectKey := strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "_")
	if projectKey == "" {
		return "root"
	}
	return projectKey
}

func ProjectToolResultsDir(agentDir, cwd string) string {
	return filepath.Join(agentDir, "tool-results", ProjectKey(cwd))
}

func SessionToolResultsDir(agentDir, cwd, sessionID string) string {
	return filepath.Join(ProjectToolResultsDir(agentDir, cwd), "sessions", sessionID)
}
