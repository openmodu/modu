package main

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
	CommandHooks  CommandHooks
	StartupNotice string
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
