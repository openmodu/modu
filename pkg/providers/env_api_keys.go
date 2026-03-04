package providers

import "os"

var cachedVertexAdcCredentialsExists *bool

func hasVertexAdcCredentials() bool {
	if cachedVertexAdcCredentialsExists != nil {
		return *cachedVertexAdcCredentialsExists
	}
	value := false
	if gacPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); gacPath != "" {
		if _, err := os.Stat(gacPath); err == nil {
			value = true
		}
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			path := home + "/.config/gcloud/application_default_credentials.json"
			if _, err := os.Stat(path); err == nil {
				value = true
			}
		}
	}
	cachedVertexAdcCredentialsExists = &value
	return value
}

// GetEnvAPIKey returns the API key for the given provider from environment variables.
func GetEnvAPIKey(provider string) string {
	if provider == "github-copilot" {
		if v := os.Getenv("COPILOT_GITHUB_TOKEN"); v != "" {
			return v
		}
		if v := os.Getenv("GH_TOKEN"); v != "" {
			return v
		}
		return os.Getenv("GITHUB_TOKEN")
	}

	if provider == "anthropic" {
		if v := os.Getenv("ANTHROPIC_OAUTH_TOKEN"); v != "" {
			return v
		}
		return os.Getenv("ANTHROPIC_API_KEY")
	}

	if provider == "google-vertex" {
		hasCredentials := hasVertexAdcCredentials()
		hasProject := os.Getenv("GOOGLE_CLOUD_PROJECT") != "" || os.Getenv("GCLOUD_PROJECT") != ""
		hasLocation := os.Getenv("GOOGLE_CLOUD_LOCATION") != ""
		if hasCredentials && hasProject && hasLocation {
			return "<authenticated>"
		}
		return ""
	}

	if provider == "amazon-bedrock" {
		if os.Getenv("AWS_PROFILE") != "" {
			return "<authenticated>"
		}
		if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
			return "<authenticated>"
		}
		if os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "" {
			return "<authenticated>"
		}
		if os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "" {
			return "<authenticated>"
		}
		if os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI") != "" {
			return "<authenticated>"
		}
		if os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE") != "" {
			return "<authenticated>"
		}
		return ""
	}

	envMap := map[string]string{
		"openai":                 "OPENAI_API_KEY",
		"azure-openai-responses": "AZURE_OPENAI_API_KEY",
		"google":                 "GEMINI_API_KEY",
		"deepseek":               "DEEPSEEK_API_KEY",
		"groq":                   "GROQ_API_KEY",
		"cerebras":               "CEREBRAS_API_KEY",
		"xai":                    "XAI_API_KEY",
		"openrouter":             "OPENROUTER_API_KEY",
		"vercel-ai-gateway":      "AI_GATEWAY_API_KEY",
		"zai":                    "ZAI_API_KEY",
		"mistral":                "MISTRAL_API_KEY",
		"minimax":                "MINIMAX_API_KEY",
		"minimax-cn":             "MINIMAX_CN_API_KEY",
		"huggingface":            "HF_TOKEN",
		"opencode":               "OPENCODE_API_KEY",
		"kimi-coding":            "KIMI_API_KEY",
	}

	if envVar, ok := envMap[provider]; ok {
		return os.Getenv(envVar)
	}
	return ""
}
