package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"

	"github.com/openmodu/modu/cmd/modu_cron/internal/crontools"
	"github.com/openmodu/modu/cmd/modu_cron/internal/provider"
)

// Add turns a natural-language task description into a cron_add call.
//
// The caller passes a plain sentence (`"every morning at 8, run git log"`).
// We start a one-shot CodingSession whose tool surface is restricted to
// cron_add + cron_list — the agent cannot read or write arbitrary files,
// only manage modu_cron's task table. A framed system instruction tells it
// how to derive the id / cron expression / on_overlap from the request and
// to reply with a single confirmation sentence.
func Add(ctx context.Context, cfgPath, description string, out io.Writer) error {
	description = strings.TrimSpace(description)
	if description == "" {
		return fmt.Errorf("add: task description required (e.g. modu_cron add \"every morning at 8, run git log\")")
	}

	model, getAPIKey := provider.Resolve()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	// Restrict to the cron management tools only. Default CodingTools would
	// give the agent Read/Write/Bash/etc., which is unnecessary scope for
	// "translate a sentence into one cron_add call".
	tools := filterCronTools(crontools.New(cfgPath), "cron_add", "cron_list")

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  coding_agent.DefaultAgentDir(),
		Model:     model,
		GetAPIKey: getAPIKey,
		Tools:     tools,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer session.Close("modu_cron_add_done")

	prompt := framePrompt(description)
	return modes.RunPrint(ctx, modes.PrintOptions{
		Mode:     modes.PrintModeText,
		Messages: []string{prompt},
		Session:  session,
		Output:   out,
	})
}

func filterCronTools(all []agent.AgentTool, names ...string) []agent.AgentTool {
	keep := map[string]bool{}
	for _, n := range names {
		keep[n] = true
	}
	out := make([]agent.AgentTool, 0, len(names))
	for _, t := range all {
		if keep[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

func framePrompt(description string) string {
	return `Create one scheduled task using the cron_add tool based on this request:

` + description + `

Guidelines:
- Generate a short, lowercase-hyphenated id derived from the user's intent (e.g. "daily-git-log", "weekly-release-check"). If cron_list shows an existing task with that id, pick a distinct one.
- Pick an appropriate 6-field cron expression. Prefer descriptors like "@every 5m", "@hourly", "@daily", "@weekly" when they cleanly match the request. Otherwise emit a literal "sec min hour dom mon dow" expression.
- Set enabled=true unless the user implies otherwise.
- Set on_overlap to skip, queue, or kill according to wording cues; default to skip.
- If the user names existing notification channels, pass them as channels. Do not invent channel names.
- Optionally call cron_list first to avoid id collisions.
- After cron_add succeeds, reply with ONE short sentence confirming what you scheduled (id, when it runs, and a brief paraphrase of the prompt).`
}
