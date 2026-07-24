package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
	"golang.org/x/term"
)

func restoreModuTUITerminal() {
	fmt.Fprint(os.Stdout, "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1049l\x1b[?25h\x1b[0m\r\n")
}

func moduTUIMouseDisabledFromEnv(env []string) bool {
	if mode, ok := envValue(env, "MODU_TUI_MOUSE"); ok {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "1", "true", "yes", "on", "cell", "mouse":
			return false
		case "0", "false", "no", "off", "none", "disabled", "disable":
			return true
		}
	}
	return false
}

func moduTUIArrowKeysScrollFromEnv(env []string) bool {
	return moduTUIMouseDisabledFromEnv(env) || moduTUISSHSessionFromEnv(env)
}

func moduTUISSHSessionFromEnv(env []string) bool {
	return envNonEmpty(env, "SSH_TTY") || envNonEmpty(env, "SSH_CONNECTION") || envNonEmpty(env, "SSH_CLIENT")
}

func envNonEmpty(env []string, key string) bool {
	value, ok := envValue(env, key)
	return ok && strings.TrimSpace(value) != ""
}

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix), true
		}
	}
	return "", false
}

func moduTUISlashRunningStatus(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "running slash command"
	}
	return "running " + fields[0]
}

func moduTUIInfoCardLines(session *coding_agent.CodingSession, model *types.Model) []string {
	lines := []string{"modu_code"}
	if label := moduTUIModelLabel(model); label != "" {
		lines = append(lines, "model: "+label)
	}
	if session != nil {
		if cwd := strings.TrimSpace(session.RuntimeState().Cwd); cwd != "" {
			lines = append(lines, "cwd: "+cwd)
		}
		if id := shortModuTUISessionID(session.GetSessionID()); id != "" {
			lines = append(lines, "session: "+id)
		}
	}
	return append(lines, "commands: type /  send: Enter  quit: Ctrl+C")
}

func moduTUIFooter(session *coding_agent.CodingSession) string {
	if session == nil {
		return "ctx - · - · -"
	}
	model := session.GetModel()
	parts := []string{moduTUIContextUsage(session, model)}
	if label := moduTUIModelLabel(model); label != "" {
		parts = append(parts, label)
	} else {
		parts = append(parts, "-")
	}
	cwd := strings.TrimSpace(session.RuntimeState().Cwd)
	if cwd == "" {
		cwd = session.Cwd()
	}
	if cwd == "" {
		cwd = "-"
	}
	return strings.Join(append(parts, compactModuTUICwd(cwd)), " · ")
}

func moduTUIContextUsage(session *coding_agent.CodingSession, model *types.Model) string {
	used := 0
	if session != nil {
		used = session.GetSessionStats().TotalTokens
	}
	limit := 0
	if model != nil {
		limit = model.ContextWindow
	}
	if limit <= 0 {
		return "ctx " + formatModuTUITokens(used)
	}
	return fmt.Sprintf("ctx %s/%s", formatModuTUITokens(used), formatModuTUITokens(limit))
}

func moduTUIModelLabel(model *types.Model) string {
	if model == nil {
		return ""
	}
	if label := strings.TrimSpace(model.Name); label != "" {
		return label
	}
	return strings.TrimSpace(model.ID)
}

func compactModuTUICwd(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "-" {
		return "-"
	}
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	separator := string(os.PathSeparator)
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) <= 2 {
		if volume != "" {
			return volume + strings.Join(parts, separator)
		}
		return strings.Join(parts, separator)
	}
	return "…" + separator + filepath.Join(parts[len(parts)-2:]...)
}

func formatModuTUITokens(tokens int) string {
	if tokens < 0 {
		tokens = 0
	}
	if tokens < 1000 {
		return strconv.Itoa(tokens)
	}
	if tokens < 1_000_000 {
		value := float64(tokens) / 1000
		if tokens < 10_000 {
			return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", value), "0"), ".") + "K"
		}
		return fmt.Sprintf("%.0fK", value)
	}
	value := float64(tokens) / 1_000_000
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", value), "0"), ".") + "M"
}

func moduTUITodos(session *coding_agent.CodingSession) []modutui.TodoItem {
	if session == nil {
		return nil
	}
	todos := session.GetTodos()
	out := make([]modutui.TodoItem, 0, len(todos))
	for _, todo := range todos {
		out = append(out, modutui.TodoItem{Content: todo.Content, Status: todo.Status})
	}
	return out
}

func shortModuTUISessionID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func loadModuTUIInputHistory(path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	out := make([]string, 0, min(len(items), 100))
	for _, item := range items {
		line := strings.TrimSpace(item)
		if line == "" {
			continue
		}
		var decoded string
		if err := json.Unmarshal([]byte(line), &decoded); err == nil {
			if decoded = strings.TrimSpace(decoded); decoded != "" {
				out = append(out, decoded)
			}
			continue
		}
		out = append(out, line)
	}
	if len(out) > 100 {
		out = append([]string(nil), out[len(out)-100:]...)
	}
	return out, nil
}

func saveModuTUIInputHistory(path string, history []string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if len(history) > 100 {
		history = history[len(history)-100:]
	}
	var lines []string
	for _, item := range history {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return err
		}
		lines = append(lines, string(encoded))
	}
	if len(lines) == 0 {
		return os.WriteFile(path, nil, 0o600)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func initialTerminalSize(fd int, fallbackWidth, fallbackHeight int) (int, int) {
	width, height, err := term.GetSize(fd)
	if err != nil || width <= 0 || height <= 0 {
		return fallbackWidth, fallbackHeight
	}
	return width, height
}
