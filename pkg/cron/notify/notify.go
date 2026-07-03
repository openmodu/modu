// Package notify sends modu_cron task completion notifications to outbound
// channels configured in config.yaml.
package notify

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/channels/feishu"
	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/runner"
)

// Sender owns the HTTP client used by outbound notification channels.
type Sender struct {
	Client *http.Client
}

// Payload is posted to generic webhook channels.
type Payload struct {
	Event      string `json:"event"`
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	EndedAt    string `json:"ended_at"`
	DurationMS int64  `json:"duration_ms"`
	LogPath    string `json:"log_path,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Error      string `json:"error,omitempty"`
	// InboxNew lists ./inbox entries the run left for a human (files under
	// <working_dir>/inbox touched during the run) — the loop's "open door",
	// surfaced in the notification instead of sitting unseen on disk.
	InboxNew []string `json:"inbox_new,omitempty"`
	// PRLinks are GitHub pull-request URLs found in the run's log, so the
	// notification links straight to what is waiting for review.
	PRLinks []string `json:"pr_links,omitempty"`
	Text    string   `json:"text"`
}

// NewSender returns a Sender with a bounded HTTP timeout.
func NewSender() *Sender {
	return &Sender{Client: &http.Client{Timeout: 10 * time.Second}}
}

// Completion sends one task completion notification to every channel named by
// task.channel / task.channels. Missing or invalid channels are reported after
// all other configured channels have been attempted. cwd is the run's working
// directory, used to surface ./inbox entries the run left for a human; empty
// skips that check.
func (s *Sender) Completion(ctx context.Context, cfg *config.Config, task config.Task, res runner.Result, runErr error, cwd string) error {
	names := task.NotificationChannels()
	if len(names) == 0 {
		return nil
	}
	if cfg == nil {
		return errors.New("notify: nil config")
	}
	if s == nil {
		s = NewSender()
	}
	if s.Client == nil {
		s.Client = NewSender().Client
	}

	payload := buildPayload(task, res, runErr, cwd)
	var errs []error
	for _, name := range names {
		ch, ok := cfg.Channels[name]
		if !ok {
			errs = append(errs, fmt.Errorf("channel %q not found", name))
			continue
		}
		if err := s.send(ctx, ch, payload); err != nil {
			errs = append(errs, fmt.Errorf("channel %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Sender) send(ctx context.Context, ch config.Channel, payload Payload) error {
	switch strings.ToLower(strings.TrimSpace(ch.Type)) {
	case "webhook", "http":
		url := valueOrEnv(ch.URL, ch.URLEnv)
		if url == "" {
			return errors.New("missing url")
		}
		return s.postJSON(ctx, url, payload)
	case "telegram":
		token := valueOrEnv(ch.Token, ch.TokenEnv)
		chatID := valueOrEnv(ch.ChatID, ch.ChatIDEnv)
		if token == "" || chatID == "" {
			return errors.New("missing token or chat_id")
		}
		url := "https://api.telegram.org/bot" + token + "/sendMessage"
		body := map[string]any{
			"chat_id": chatID,
			"text":    payload.Text,
		}
		return s.postJSON(ctx, url, body)
	case "feishu_webhook", "lark_webhook":
		url := valueOrEnv(ch.URL, ch.URLEnv)
		if url == "" {
			return errors.New("missing url")
		}
		body := map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": payload.Text},
		}
		return s.postJSON(ctx, url, body)
	case "feishu_bot", "lark_bot":
		appID := valueOrEnv(ch.AppID, ch.AppIDEnv)
		appSecret := valueOrEnv(ch.AppSecret, ch.AppSecretEnv)
		if appID == "" || appSecret == "" {
			// Share the app credentials modu's feishu channel already keeps
			// at ~/.modu/channels/feishu/config.toml (env overrides apply),
			// so a cron channel only needs a chat_id.
			if cfg, err := feishu.EffectiveConfig(); err == nil && cfg.Ready() {
				appID, appSecret = cfg.AppID, cfg.AppSecret
			}
		}
		chatID := valueOrEnv(ch.ChatID, ch.ChatIDEnv)
		if appID == "" || appSecret == "" || chatID == "" {
			return errors.New("missing app_id/app_secret (set them or configure ~/.modu/channels/feishu) or chat_id")
		}
		return feishu.SendText(ctx, appID, appSecret, chatID, payload.Text)
	default:
		return fmt.Errorf("unsupported type %q", ch.Type)
	}
}

func (s *Sender) postJSON(ctx context.Context, url string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, readSnippet(resp.Body, 512))
	}
	return nil
}

func buildPayload(task config.Task, res runner.Result, runErr error, cwd string) Payload {
	status := res.Status
	errText := ""
	if runErr != nil {
		if status == "" {
			status = runner.StatusError
		}
		errText = runErr.Error()
	}
	if status == "" {
		status = runner.StatusOK
	}
	summary := lastAssistantText(res.LogPath)
	payload := Payload{
		Event:      "modu_cron.task_completed",
		TaskID:     task.ID,
		Status:     status,
		StartedAt:  res.Started.Local().Format(time.RFC3339Nano),
		EndedAt:    res.Ended.Local().Format(time.RFC3339Nano),
		DurationMS: res.Ended.Sub(res.Started).Milliseconds(),
		LogPath:    res.LogPath,
		Summary:    summary,
		Error:      errText,
		InboxNew:   collectInboxNew(cwd, res.Started),
		PRLinks:    prLinks(res.LogPath),
	}
	payload.Text = formatText(payload)
	return payload
}

// inboxMaxListed caps how many inbox entries the notification names; the
// count still reflects the true total.
const inboxMaxListed = 10

// collectInboxNew returns the names of files under <cwd>/inbox modified at
// or after since (the run's start) — i.e. what this run left for a human.
// Dotfiles and subdirectories are ignored. Missing dir yields nil.
func collectInboxNew(cwd string, since time.Time) []string {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(cwd, "inbox"))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// prLinkRe matches GitHub pull-request URLs in the run log — both PRs the
// agent reported opening and ones referenced in tool output.
var prLinkRe = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`)

// prLinks extracts unique GitHub PR URLs from the run's log file, in first-
// seen order, capped at 5.
func prLinks(logPath string) []string {
	if logPath == "" {
		return nil
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var links []string
	for _, link := range prLinkRe.FindAllString(string(data), -1) {
		if seen[link] {
			continue
		}
		seen[link] = true
		links = append(links, link)
		if len(links) == 5 {
			break
		}
	}
	return links
}

func formatText(p Payload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "modu_cron task completed\n")
	fmt.Fprintf(&b, "task: %s\n", p.TaskID)
	fmt.Fprintf(&b, "status: %s\n", p.Status)
	if p.DurationMS >= 0 {
		fmt.Fprintf(&b, "duration: %s\n", (time.Duration(p.DurationMS) * time.Millisecond).Round(100*time.Millisecond))
	}
	if p.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", p.Error)
	}
	if p.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", truncate(p.Summary, 1200))
	}
	if n := len(p.InboxNew); n > 0 {
		listed := p.InboxNew
		suffix := ""
		if n > inboxMaxListed {
			listed = listed[:inboxMaxListed]
			suffix = fmt.Sprintf(" (+%d more)", n-inboxMaxListed)
		}
		fmt.Fprintf(&b, "inbox: %d new item(s) waiting for you: %s%s\n", n, strings.Join(listed, ", "), suffix)
	}
	for _, link := range p.PRLinks {
		fmt.Fprintf(&b, "pr: %s\n", link)
	}
	if p.LogPath != "" {
		fmt.Fprintf(&b, "log: %s", p.LogPath)
	}
	return strings.TrimRight(b.String(), "\n")
}

func lastAssistantText(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	last := ""
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if typ, _ := ev["type"].(string); typ != "assistant" {
			continue
		}
		if text, _ := ev["text"].(string); strings.TrimSpace(text) != "" {
			last = strings.TrimSpace(text)
		}
	}
	return last
}

func valueOrEnv(value, envName string) string {
	if value != "" {
		return os.ExpandEnv(value)
	}
	if envName == "" {
		return ""
	}
	return os.Getenv(envName)
}

func readSnippet(r io.Reader, max int64) string {
	data, _ := io.ReadAll(io.LimitReader(r, max))
	return strings.TrimSpace(string(data))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
