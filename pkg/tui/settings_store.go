package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type persistedTUISettings struct {
	TranscriptMode bool `json:"transcriptMode"`
}

func (r *goTUIRoot) loadPersistedTUISettings() {
	path := r.tuiSettingsPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var settings persistedTUISettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return
	}
	r.model.transcriptMode = settings.TranscriptMode
}

func (r *goTUIRoot) savePersistedTUISettings() error {
	path := r.tuiSettingsPath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(persistedTUISettings{
		TranscriptMode: r.model.transcriptMode,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func (r *goTUIRoot) tuiSettingsPath() string {
	if r.session == nil {
		return ""
	}
	agentDir := r.session.GetContextInfo().AgentDir
	if agentDir == "" {
		return ""
	}
	return filepath.Join(agentDir, "tui_settings.json")
}
