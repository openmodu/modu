// Package config loads and saves modu_cron's YAML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
)

// OverlapPolicy decides what to do when a task fires while the previous run
// is still in flight.
//
//   - "skip"  drop the new tick and log a warning (default).
//   - "queue" enqueue up to QueueCapacity runs; further ticks log warning and
//     are dropped.
//   - "kill"  cancel the running execution and start the new one.
type OverlapPolicy string

const (
	OverlapSkip  OverlapPolicy = "skip"
	OverlapQueue OverlapPolicy = "queue"
	OverlapKill  OverlapPolicy = "kill"

	// QueueCapacity caps queued runs per task to avoid unbounded memory
	// when cron frequency exceeds task throughput. Tickets past this are
	// dropped with a warning.
	QueueCapacity = 8
)

// DefaultRunTimeout caps a single run's wall-clock time when the task does
// not set timeout explicitly. It doubles as a circuit breaker: a run that
// hangs (network stall, runaway loop) is cancelled instead of blocking the
// task slot forever.
const DefaultRunTimeout = 30 * time.Minute

// Task describes a single scheduled prompt for the agent to run.
type Task struct {
	UUID      string        `yaml:"uuid,omitempty"`
	Name      string        `yaml:"name,omitempty"`
	ID        string        `yaml:"id,omitempty"`
	Cron      string        `yaml:"cron"`
	Prompt    string        `yaml:"prompt"`
	Goal      string        `yaml:"goal,omitempty"`
	Enabled   bool          `yaml:"enabled"`
	Timezone  string        `yaml:"timezone,omitempty"`
	OnOverlap OverlapPolicy `yaml:"on_overlap,omitempty"`
	Channel   string        `yaml:"channel,omitempty"`
	Channels  []string      `yaml:"channels,omitempty"`

	// Timeout is the per-run wall-clock cap as a Go duration string
	// (e.g. "45m"). Empty falls back to DefaultRunTimeout.
	Timeout string `yaml:"timeout,omitempty"`
	// MaxTokensPerRun cancels the run once its accumulated input+output
	// tokens reach this value. Zero means no per-run token cap.
	MaxTokensPerRun int `yaml:"max_tokens_per_run,omitempty"`
	// MaxRetries is how many times the daemon re-runs the task after a
	// plain error (not after timeout / cap trips). Zero means no retries.
	MaxRetries int `yaml:"max_retries,omitempty"`
}

// Identity returns the stable identifier used for scheduling, removal, logs,
// and notifications. ID is a legacy fallback for old in-memory callers.
func (t Task) Identity() string {
	if s := strings.TrimSpace(t.UUID); s != "" {
		return s
	}
	if s := strings.TrimSpace(t.ID); s != "" {
		return s
	}
	return strings.TrimSpace(t.Name)
}

// DisplayName returns the human-readable task name. Legacy id becomes name
// when loaded from disk; ID remains a fallback for existing tests/callers.
func (t Task) DisplayName() string {
	if s := strings.TrimSpace(t.Name); s != "" {
		return s
	}
	if s := strings.TrimSpace(t.ID); s != "" {
		return s
	}
	return strings.TrimSpace(t.UUID)
}

// Normalize fills the new uuid/name fields and migrates legacy id to name.
// It intentionally clears ID so newly saved YAML uses name instead of id.
func (t *Task) Normalize() {
	if t == nil {
		return
	}
	t.UUID = strings.TrimSpace(t.UUID)
	t.Name = strings.TrimSpace(t.Name)
	t.ID = strings.TrimSpace(t.ID)
	if t.Name == "" && t.ID != "" {
		t.Name = t.ID
	}
	if t.UUID == "" {
		t.UUID = uuid.NewString()
	}
	t.ID = ""
}

// EffectiveTimeout returns the parsed per-run timeout, falling back to
// DefaultRunTimeout for empty or unparseable values. Use ValidateCaps to
// surface parse errors to the user instead of relying on the fallback.
func (t Task) EffectiveTimeout() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(t.Timeout))
	if err != nil || d <= 0 {
		return DefaultRunTimeout
	}
	return d
}

// ValidateCaps rejects malformed cap settings so config reload fails loudly
// instead of silently falling back to defaults.
func (t Task) ValidateCaps() error {
	if len(strings.TrimSpace(t.Goal)) > 4000 {
		return fmt.Errorf("task %s: goal is too long", t.Identity())
	}
	if s := strings.TrimSpace(t.Timeout); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("task %s: invalid timeout %q: %w", t.Identity(), t.Timeout, err)
		}
		if d <= 0 {
			return fmt.Errorf("task %s: timeout must be positive, got %q", t.Identity(), t.Timeout)
		}
	}
	if t.MaxTokensPerRun < 0 {
		return fmt.Errorf("task %s: max_tokens_per_run must be non-negative", t.Identity())
	}
	if t.MaxRetries < 0 {
		return fmt.Errorf("task %s: max_retries must be non-negative", t.Identity())
	}
	return nil
}

// Policy returns t.OnOverlap normalized; unknown / empty falls back to skip.
func (t Task) Policy() OverlapPolicy {
	switch t.OnOverlap {
	case OverlapQueue, OverlapKill:
		return t.OnOverlap
	default:
		return OverlapSkip
	}
}

// NotificationChannels returns the task's configured channel names with
// whitespace trimmed and duplicates removed. The singular channel field is
// kept for concise one-channel configs; channels is preferred for many.
func (t Task) NotificationChannels() []string {
	var out []string
	seen := make(map[string]bool)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	add(t.Channel)
	for _, name := range t.Channels {
		add(name)
	}
	return out
}

// Channel describes one outbound completion-notification destination.
//
// Supported types:
//   - webhook: POST a JSON payload to url / url_env.
//   - telegram: send text via Telegram Bot API using token / token_env and
//     chat_id / chat_id_env.
//   - feishu_webhook: send text to a Feishu/Lark custom bot webhook.
//   - feishu_bot: send text as a Feishu app bot (app_id / app_secret +
//     chat_id), no webhook needed — same credentials as pkg/channels/feishu.
type Channel struct {
	Type         string `yaml:"type"`
	URL          string `yaml:"url,omitempty"`
	URLEnv       string `yaml:"url_env,omitempty"`
	Token        string `yaml:"token,omitempty"`
	TokenEnv     string `yaml:"token_env,omitempty"`
	ChatID       string `yaml:"chat_id,omitempty"`
	ChatIDEnv    string `yaml:"chat_id_env,omitempty"`
	AppID        string `yaml:"app_id,omitempty"`
	AppIDEnv     string `yaml:"app_id_env,omitempty"`
	AppSecret    string `yaml:"app_secret,omitempty"`
	AppSecretEnv string `yaml:"app_secret_env,omitempty"`
}

// Config is the on-disk shape of modu_cron's runtime config file.
//
// There is no model/provider config here on purpose: cron tasks run with
// whatever model is active in modu_code's own config (~/.modu/config.toml,
// see pkg/provider), the same as an interactive session — one place to
// manage models, not two.
//
// Tasks is kept for backward compatibility with the original single-file
// format. New configs should keep tasks in TasksFile instead.
type Config struct {
	WorkingDir string             `yaml:"working_dir,omitempty"`
	Channels   map[string]Channel `yaml:"channels,omitempty"`
	TasksFile  string             `yaml:"tasks_file,omitempty"`
	Tasks      []Task             `yaml:"tasks,omitempty"`

	// DailyBudgetTokens is the per-task daily token ceiling. Global daily
	// usage is still recorded for observability, but this budget only gates
	// the task being started. Zero disables the daily budget.
	DailyBudgetTokens int `yaml:"daily_budget_tokens,omitempty"`
}

// TasksConfig is the on-disk shape of the isolated cron task file.
type TasksConfig struct {
	Tasks []Task `yaml:"tasks"`
}

// DefaultPath returns the cron config path inside modu's single config
// home: ~/.modu/cron/config.yaml. There is no separate ~/.modu_cron
// directory — everything modu owns lives under ~/.modu.
func DefaultPath() string {
	return filepath.Join(resource.DefaultAgentDir(), "cron", "config.yaml")
}

// DefaultTasksPath returns the default tasks file next to cfgPath.
func DefaultTasksPath(cfgPath string) string {
	dir := filepath.Dir(cfgPath)
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "tasks.yaml")
}

// ResolveTasksPath returns the task file configured by cfg. A relative
// tasks_file is resolved relative to cfgPath's directory.
func ResolveTasksPath(cfgPath string, cfg *Config) string {
	if cfg == nil || strings.TrimSpace(cfg.TasksFile) == "" {
		return DefaultTasksPath(cfgPath)
	}
	path := expandPath(strings.TrimSpace(cfg.TasksFile))
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(cfgPath), path)
}

// ResolveWorkingDir returns the configured working directory. If unset, it
// returns fallback. Relative paths are resolved relative to cfgPath's
// directory.
func ResolveWorkingDir(cfgPath string, cfg *Config, fallback string) string {
	if cfg == nil || strings.TrimSpace(cfg.WorkingDir) == "" {
		return fallback
	}
	path := expandPath(strings.TrimSpace(cfg.WorkingDir))
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(cfgPath), path)
}

// Load reads and parses the runtime config at path plus its isolated task
// file. A missing config yields an empty Config so the daemon can boot with
// zero tasks.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := &Config{}
			taskPath := ResolveTasksPath(path, cfg)
			if fileExists(taskPath) {
				tasksCfg, err := LoadTasks(taskPath)
				if err != nil {
					return nil, err
				}
				cfg.Tasks = tasksCfg.Tasks
			}
			NormalizeLoadedTasks(cfg.Tasks)
			return cfg, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	taskPath := ResolveTasksPath(path, &cfg)
	if cfg.TasksFile != "" || fileExists(taskPath) {
		tasksCfg, err := LoadTasks(taskPath)
		if err != nil {
			return nil, err
		}
		cfg.Tasks = tasksCfg.Tasks
	}
	NormalizeLoadedTasks(cfg.Tasks)
	return &cfg, nil
}

// LoadRuntime reads only the runtime config file, without merging tasks from
// the isolated task file.
func LoadRuntime(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	NormalizeLoadedTasks(cfg.Tasks)
	return &cfg, nil
}

// LoadTasks reads the isolated task file. A missing file yields zero tasks.
func LoadTasks(path string) (*TasksConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &TasksConfig{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg TasksConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	NormalizeLoadedTasks(cfg.Tasks)
	return &cfg, nil
}

// Save writes cfg to path as-is. It is kept for legacy single-file configs and
// tests; new runtime config writes should use SaveRuntime, and task mutations
// should use SaveTasks.
func Save(path string, cfg *Config) error {
	clone := *cfg
	clone.Tasks = normalizeTaskSlice(clone.Tasks)
	data, err := yaml.Marshal(&clone)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return writeFileAtomic(path, data)
}

// SaveRuntime writes cfg to path without embedding tasks.
func SaveRuntime(path string, cfg *Config) error {
	clone := *cfg
	clone.Tasks = nil
	data, err := yaml.Marshal(&clone)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return writeFileAtomic(path, data)
}

// SaveTasks writes the isolated task file, creating parent directories.
func SaveTasks(path string, tasks []Task) error {
	data, err := yaml.Marshal(TasksConfig{Tasks: normalizeTaskSlice(tasks)})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return writeFileAtomic(path, data)
}

// NormalizeTasks normalizes tasks in place.
func NormalizeTasks(tasks []Task) {
	for i := range tasks {
		tasks[i].Normalize()
	}
}

// NormalizeLoadedTasks normalizes tasks loaded from disk. Missing UUIDs are
// derived deterministically so `/cron list` can show a UUID that `/cron rm`
// can resolve on a subsequent load before the migrated file is saved.
func NormalizeLoadedTasks(tasks []Task) {
	for i := range tasks {
		if strings.TrimSpace(tasks[i].UUID) == "" {
			tasks[i].UUID = legacyTaskUUID(i, tasks[i])
		}
		tasks[i].Normalize()
	}
}

func normalizeTaskSlice(tasks []Task) []Task {
	if len(tasks) == 0 {
		return tasks
	}
	out := make([]Task, len(tasks))
	copy(out, tasks)
	NormalizeTasks(out)
	return out
}

func legacyTaskUUID(index int, task Task) string {
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = strings.TrimSpace(task.ID)
	}
	seed := fmt.Sprintf("modu-cron-task:%d:%s:%s:%s", index, name, strings.TrimSpace(task.Cron), strings.TrimSpace(task.Prompt))
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(seed)).String()
}

// writeFileAtomic writes data via a temp file + rename in the target
// directory, so concurrent readers (the daemon's hot reload, a modu_code
// session's cron tools) never observe a partially written YAML file. The
// daemon's fsnotify reload filter already accepts Rename events.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".modu-cron-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func expandPath(path string) string {
	path = os.ExpandEnv(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
