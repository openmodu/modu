package moms

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoggedMessage is one line in log.jsonl.
type LoggedMessage struct {
	Date        string   `json:"date"`
	Ts          string   `json:"ts"`
	User        string   `json:"user"`
	UserName    string   `json:"userName,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Text        string   `json:"text"`
	Attachments []string `json:"attachments"`
	IsBot       bool     `json:"isBot"`
}

// Store manages per-chat persistence.
type Store struct {
	WorkingDir string
}

// NewStore creates a Store.
func NewStore(workingDir string) *Store {
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		panic(fmt.Sprintf("moms: could not create working directory: %v", err))
	}
	return &Store{WorkingDir: workingDir}
}

// ChatDir returns (and creates) the directory for a chat ID.
func (s *Store) ChatDir(chatID int64) string {
	dir := filepath.Join(s.WorkingDir, fmt.Sprintf("%d", chatID))
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// AppendLog appends a LoggedMessage to the chat's log.jsonl.
func (s *Store) AppendLog(chatID int64, msg LoggedMessage) error {
	logPath := filepath.Join(s.ChatDir(chatID), "log.jsonl")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// LogUserMessage writes a user message to log.jsonl.
func (s *Store) LogUserMessage(chatID int64, ts, userID, userName, text string, attachments []string) error {
	return s.AppendLog(chatID, LoggedMessage{
		Date:        time.Now().UTC().Format(time.RFC3339),
		Ts:          ts,
		User:        userID,
		UserName:    userName,
		DisplayName: userName,
		Text:        text,
		Attachments: attachments,
		IsBot:       false,
	})
}

// LogBotResponse writes a bot reply to log.jsonl.
func (s *Store) LogBotResponse(chatID int64, ts, text string) error {
	return s.AppendLog(chatID, LoggedMessage{
		Date:        time.Now().UTC().Format(time.RFC3339),
		Ts:          ts,
		User:        "bot",
		Text:        text,
		Attachments: []string{},
		IsBot:       true,
	})
}

// GetExistingTimestamps reads all ts values from log.jsonl.
func (s *Store) GetExistingTimestamps(chatID int64) map[string]struct{} {
	logPath := filepath.Join(s.WorkingDir, fmt.Sprintf("%d", chatID), "log.jsonl")
	set := make(map[string]struct{})
	data, err := os.ReadFile(logPath)
	if err != nil {
		return set
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg LoggedMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil && msg.Ts != "" {
			set[msg.Ts] = struct{}{}
		}
	}
	return set
}

// DownloadFile downloads a URL (with optional bearer token) and saves to destPath.
func DownloadFile(destPath, url, bearerToken string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, url)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
