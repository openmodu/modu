package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openmodu/modu/pkg/cron/config"
)

// InitOptions configures the initial modu_cron files. The LLM model itself
// is not part of this config — cron reuses whatever model modu_code has
// active (see `modu_code config`), the same as an interactive session.
type InitOptions struct {
	Force             bool
	Interactive       bool
	In                io.Reader
	WorkingDir        string
	TasksFile         string
	TelegramChannel   string
	DisableTelegram   bool
	TelegramToken     string
	TelegramTokenEnv  string
	TelegramChatID    string
	TelegramChatIDEnv string
}

// Init creates an isolated runtime config and task file.
func Init(cfgPath string, opts InitOptions, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if opts.In == nil {
		opts.In = strings.NewReader("")
	}
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	if !opts.Force {
		if _, err := os.Stat(cfgPath); err == nil {
			return fmt.Errorf("config already exists: %s (pass --force to overwrite)", cfgPath)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	defaultWorkdir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	workingDir := defaultString(opts.WorkingDir, defaultWorkdir)
	tasksFile := defaultString(opts.TasksFile, "tasks.yaml")
	tgChannel := defaultString(opts.TelegramChannel, "telegram-home")
	tgToken := opts.TelegramToken
	tgTokenEnv := defaultString(opts.TelegramTokenEnv, "MODU_CRON_TG_TOKEN")
	tgChatID := opts.TelegramChatID
	tgChatIDEnv := defaultString(opts.TelegramChatIDEnv, "MODU_CRON_TG_CHAT_ID")

	if opts.Interactive {
		r := bufio.NewReader(opts.In)
		fmt.Fprintln(out, "Initialize modu_cron config. Press Enter to accept defaults.")
		workingDir, err = promptString(r, out, "Working directory", workingDir)
		if err != nil {
			return err
		}
		tasksFile, err = promptString(r, out, "Tasks file", tasksFile)
		if err != nil {
			return err
		}
		if !opts.DisableTelegram {
			enableTG, err := promptBool(r, out, "Create Telegram channel", true)
			if err != nil {
				return err
			}
			opts.DisableTelegram = !enableTG
		}
		if !opts.DisableTelegram {
			tgChannel, err = promptString(r, out, "Telegram channel name", tgChannel)
			if err != nil {
				return err
			}
			if tgToken == "" {
				tgToken, err = promptString(r, out, "Telegram bot token (blank to use env)", "")
				if err != nil {
					return err
				}
			}
			if tgToken == "" {
				tgTokenEnv, err = promptString(r, out, "Telegram token env", tgTokenEnv)
				if err != nil {
					return err
				}
			} else {
				tgTokenEnv = ""
			}
			if tgChatID == "" {
				tgChatID, err = promptString(r, out, "Telegram chat id (blank to use env)", "")
				if err != nil {
					return err
				}
			}
			if tgChatID == "" {
				tgChatIDEnv, err = promptString(r, out, "Telegram chat id env", tgChatIDEnv)
				if err != nil {
					return err
				}
			} else {
				tgChatIDEnv = ""
			}
		}
	}
	if abs, err := filepath.Abs(workingDir); err == nil {
		workingDir = abs
	}

	cfg := &config.Config{
		WorkingDir: workingDir,
		TasksFile:  tasksFile,
	}
	if !opts.DisableTelegram {
		cfg.Channels = map[string]config.Channel{
			tgChannel: {
				Type:      "telegram",
				Token:     tgToken,
				TokenEnv:  tgTokenEnv,
				ChatID:    tgChatID,
				ChatIDEnv: tgChatIDEnv,
			},
		}
	}
	if err := config.SaveRuntime(cfgPath, cfg); err != nil {
		return err
	}
	taskPath := config.ResolveTasksPath(cfgPath, cfg)
	if opts.Force || !exists(taskPath) {
		if err := config.SaveTasks(taskPath, nil); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "wrote config: %s\n", cfgPath)
	fmt.Fprintf(out, "wrote tasks: %s\n", taskPath)
	fmt.Fprintf(out, "working_dir: %s\n", workingDir)
	if !opts.DisableTelegram {
		fmt.Fprintf(out, "telegram channel: %s (%s, %s)\n", tgChannel, telegramRef(tgToken, tgTokenEnv), telegramRef(tgChatID, tgChatIDEnv))
	}
	return nil
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func telegramRef(value, envName string) string {
	if value != "" {
		return "direct"
	}
	return envName
}

func promptString(r *bufio.Reader, out io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		value = def
	}
	return value, nil
}

func promptBool(r *bufio.Reader, out io.Writer, label string, def bool) (bool, error) {
	suffix := "[Y/n]"
	if !def {
		suffix = "[y/N]"
	}
	fmt.Fprintf(out, "%s %s: ", label, suffix)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	value := strings.ToLower(strings.TrimSpace(line))
	if value == "" {
		return def, nil
	}
	return value == "y" || value == "yes", nil
}
