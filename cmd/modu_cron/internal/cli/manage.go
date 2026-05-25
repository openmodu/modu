package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/openmodu/modu/pkg/types"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/crontools"
	"github.com/openmodu/modu/cmd/modu_cron/internal/provider"
)

// ManageOptions configures one natural-language cron-management turn.
type ManageOptions struct {
	CfgPath   string
	Cwd       string
	AgentDir  string
	Model     *types.Model
	GetAPIKey func(provider string) (string, error)
}

// ManageCron handles a natural-language request against the modu_cron config.
// The session is intentionally limited to cron_add, cron_list, and cron_remove.
func ManageCron(ctx context.Context, opts ManageOptions, message string, out io.Writer) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("manage: message required")
	}
	if out == nil {
		out = io.Discard
	}

	model := opts.Model
	getAPIKey := opts.GetAPIKey
	cfg, err := config.Load(opts.CfgPath)
	if err != nil {
		return err
	}
	if model == nil || getAPIKey == nil {
		model, getAPIKey = provider.ResolveWithConfig(cfg)
	}
	cwd := opts.Cwd
	if cwd == "" {
		fallbackCwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		cwd = config.ResolveWorkingDir(opts.CfgPath, cfg, fallbackCwd)
	}
	agentDir := opts.AgentDir
	if agentDir == "" {
		agentDir = coding_agent.DefaultAgentDir()
	}

	tools := filterCronTools(crontools.New(opts.CfgPath), "cron_add", "cron_list", "cron_remove")
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: getAPIKey,
		Tools:     tools,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer session.Close("modu_cron_manage_done")

	return modes.RunPrint(ctx, modes.PrintOptions{
		Mode:     modes.PrintModeText,
		Messages: []string{frameManagePrompt(message)},
		Session:  session,
		Output:   out,
	})
}

func frameManagePrompt(message string) string {
	return `Manage the modu_cron schedule using only the provided cron tools.

User request:
` + message + `

Rules:
- For listing, showing, checking, or asking what exists, call cron_list and summarize the configured tasks.
- For creating a schedule, call cron_list first to avoid duplicate ids and to see configured notification channel names, then call cron_add.
- For removing a schedule, call cron_list first if the id is not explicit, then call cron_remove only when the target is clear.
- Do not invent notification channel names. If the user asks for notification but no matching channel name is configured, ask them to configure the channel first.
- Cron expressions use the 6-field format with seconds first. Use descriptors such as "@every 5m", "@hourly", "@daily", or "@weekly" only when they match cleanly.
- If the request is ambiguous, ask one concise clarification question instead of changing the config.
- Reply with one concise plain-text answer in the same language as the user.`
}
