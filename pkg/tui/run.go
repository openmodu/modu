package tui

import (
	"context"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

type CommandHooks struct {
	Config            func(args string) (string, error)
	ConfigModels      func() ([]ConfigModelEntry, error)
	ConfigProviders   func() ([]ConfigProviderEntry, error)
	ConfigAdd         func(ConfigModelInput) (string, error)
	ConfigSetProvider func(ConfigProviderInput) (string, error)
	ConfigUse         func(target string) (string, error)
	ConfigRemove      func(target string) (string, error)
	ConfigWorkflows   func() (string, error)
	SaveScopedModels  func(ids []string) error
}

type RunOptions struct {
	CommandHooks CommandHooks
}

type ConfigModelEntry struct {
	Name        string
	Description string
	Provider    string
	Model       string
	BaseURL     string
	Active      bool
}

type ConfigProviderEntry struct {
	Name      string
	Type      string
	BaseURL   string
	APIKeySet bool
	APIKeyEnv string
}

type ConfigModelInput struct {
	Name        string
	Description string
	Provider    string
	Model       string
	BaseURL     string
	APIKey      string
}

type ConfigProviderInput struct {
	Provider  string
	Type      string
	BaseURL   string
	APIKey    string
	APIKeyEnv string
}

// Run starts the interactive TUI session.
func Run(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool) error {
	return RunWithOptions(ctx, session, model, noApprove, RunOptions{})
}

func RunWithOptions(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts RunOptions) error {
	return RunBubbleInlineWithOptions(ctx, session, model, noApprove, opts)
}
