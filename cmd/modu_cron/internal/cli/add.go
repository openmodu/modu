package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/scheduler"
)

// Add interactively prompts for a new task on out and reads answers from in,
// appends it to cfgPath, and writes the file back. Caller is responsible for
// ensuring in is a TTY when desired — Add itself does not enforce that, so
// tests can drive it with a strings.Reader.
//
// IDs must be unique within the file. Cron expressions are validated against
// the same parser the scheduler uses, so anything that parses here will run.
func Add(cfgPath string, in io.Reader, out io.Writer) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, t := range cfg.Tasks {
		existing[t.ID] = true
	}

	r := bufio.NewReader(in)
	fmt.Fprintf(out, "Add task — config: %s\n", cfgPath)

	id, err := readNonEmpty(r, out, "id", func(v string) error {
		if existing[v] {
			return fmt.Errorf("task %q already exists", v)
		}
		return nil
	})
	if err != nil {
		return err
	}

	cronExpr, err := readNonEmpty(r, out, "cron (6 fields, e.g. \"0 0 9 * * *\")", scheduler.ValidateCron)
	if err != nil {
		return err
	}

	prompt, err := readNonEmpty(r, out, "prompt", nil)
	if err != nil {
		return err
	}

	enabled, err := readBool(r, out, "enabled", true)
	if err != nil {
		return err
	}

	overlap, err := readChoice(r, out, "on_overlap", []string{"skip", "queue", "kill"}, "skip")
	if err != nil {
		return err
	}

	cfg.Tasks = append(cfg.Tasks, config.Task{
		ID:        id,
		Cron:      cronExpr,
		Prompt:    prompt,
		Enabled:   enabled,
		OnOverlap: config.OverlapPolicy(overlap),
	})
	if err := config.Save(cfgPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "added task %q to %s\n", id, cfgPath)
	return nil
}

// readNonEmpty prompts until a non-empty answer is given. If validate is
// non-nil, the answer is re-prompted on validation failure with the message
// shown.
func readNonEmpty(r *bufio.Reader, out io.Writer, label string, validate func(string) error) (string, error) {
	for {
		fmt.Fprintf(out, "%s: ", label)
		line, err := r.ReadString('\n')
		if err == io.EOF && line == "" {
			return "", fmt.Errorf("%s: input closed", label)
		}
		if err != nil && err != io.EOF {
			return "", err
		}
		v := strings.TrimSpace(line)
		if v == "" {
			fmt.Fprintf(out, "  (required)\n")
			if err == io.EOF {
				return "", fmt.Errorf("%s: input closed", label)
			}
			continue
		}
		if validate != nil {
			if verr := validate(v); verr != nil {
				fmt.Fprintf(out, "  invalid: %v\n", verr)
				if err == io.EOF {
					return "", fmt.Errorf("%s: input closed after invalid value", label)
				}
				continue
			}
		}
		return v, nil
	}
}

// readBool prompts for yes/no with a default applied on empty input.
func readBool(r *bufio.Reader, out io.Writer, label string, dflt bool) (bool, error) {
	hint := "Y/n"
	if !dflt {
		hint = "y/N"
	}
	for {
		fmt.Fprintf(out, "%s [%s]: ", label, hint)
		line, err := r.ReadString('\n')
		if err == io.EOF && line == "" {
			return dflt, nil
		}
		if err != nil && err != io.EOF {
			return false, err
		}
		v := strings.ToLower(strings.TrimSpace(line))
		switch v {
		case "":
			return dflt, nil
		case "y", "yes", "true", "1":
			return true, nil
		case "n", "no", "false", "0":
			return false, nil
		default:
			fmt.Fprintf(out, "  please answer y or n\n")
			if err == io.EOF {
				return dflt, fmt.Errorf("%s: input closed", label)
			}
		}
	}
}

// readChoice prompts for one of choices; empty input uses dflt. Comparison is
// case-insensitive.
func readChoice(r *bufio.Reader, out io.Writer, label string, choices []string, dflt string) (string, error) {
	hint := strings.Join(choices, "/")
	for {
		fmt.Fprintf(out, "%s [%s, default=%s]: ", label, hint, dflt)
		line, err := r.ReadString('\n')
		if err == io.EOF && line == "" {
			return dflt, nil
		}
		if err != nil && err != io.EOF {
			return "", err
		}
		v := strings.ToLower(strings.TrimSpace(line))
		if v == "" {
			return dflt, nil
		}
		for _, c := range choices {
			if v == c {
				return c, nil
			}
		}
		fmt.Fprintf(out, "  must be one of: %s\n", hint)
		if err == io.EOF {
			return dflt, fmt.Errorf("%s: input closed", label)
		}
	}
}
