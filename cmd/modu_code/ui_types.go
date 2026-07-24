package main

type CommandHooks struct {
	Config            func(args string) (string, error)
	ConfigProviders   func() ([]ConfigProviderEntry, error)
	ConfigSetProvider func(ConfigProviderInput) (string, error)
	ConfigureTelegram func(TelegramChannelInput) (string, error)
	ConfigureFeishu   func(FeishuChannelInput) (string, error)
}

type RunOptions struct {
	CommandHooks  CommandHooks
	StartupNotice string
}

type ConfigProviderEntry struct {
	Name      string
	Type      string
	BaseURL   string
	APIKeyEnv string
}

type ConfigProviderInput struct {
	Provider  string
	Type      string
	BaseURL   string
	APIKey    string
	APIKeyEnv string
}

type TelegramChannelInput struct {
	Token string
}

type FeishuChannelInput struct {
	AppID     string
	AppSecret string
	ChatIDs   []string
}
