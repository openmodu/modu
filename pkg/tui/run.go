package tui

import (
	"context"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

type CommandHooks struct {
	Config func(args string) (string, error)
}

type RunOptions struct {
	CommandHooks CommandHooks
}

// Run starts the interactive TUI session.
func Run(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool) error {
	return RunWithOptions(ctx, session, model, noApprove, RunOptions{})
}

func RunWithOptions(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts RunOptions) error {
	return RunBubbleInlineWithOptions(ctx, session, model, noApprove, opts)
}
