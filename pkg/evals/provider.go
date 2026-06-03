package evals

import (
	"fmt"
	"os"
	"strings"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

// ProviderSpec describes one OpenAI-compatible provider used by evals.
type ProviderSpec struct {
	ProviderID string
	BaseURL    string
	APIKey     string
	ModelID    string
}

// Model returns the modu model descriptor for this provider spec.
func (s ProviderSpec) Model() *types.Model {
	return &types.Model{
		ID:         s.ModelID,
		Name:       s.ModelID,
		ProviderID: s.ProviderID,
		BaseURL:    s.BaseURL,
	}
}

// NewProvider creates a provider from a spec.
func NewProvider(spec ProviderSpec) (providers.Provider, error) {
	if spec.ProviderID == "" {
		return nil, fmt.Errorf("provider id is required")
	}
	if spec.BaseURL == "" {
		return nil, fmt.Errorf("base url is required for provider %q", spec.ProviderID)
	}
	if spec.ModelID == "" {
		return nil, fmt.Errorf("model is required for provider %q", spec.ProviderID)
	}
	return openai.New(
		spec.ProviderID,
		openai.WithBaseURL(spec.BaseURL),
		openai.WithAPIKey(spec.APIKey),
	), nil
}

func providerSpecsFromEnv() []ProviderSpec {
	providerEnv := strings.TrimSpace(os.Getenv("EVAL_PROVIDER"))
	if providerEnv == "" {
		providerEnv = "lmstudio"
	}

	parts := strings.Split(providerEnv, ",")
	specs := make([]ProviderSpec, 0, len(parts))
	for _, part := range parts {
		providerID := strings.ToLower(strings.TrimSpace(part))
		if providerID == "" {
			continue
		}
		specs = append(specs, ProviderSpec{
			ProviderID: providerID,
			BaseURL:    firstNonEmpty(os.Getenv("EVAL_"+envProviderName(providerID)+"_BASE_URL"), os.Getenv("EVAL_BASE_URL"), defaultBaseURL(providerID)),
			APIKey:     firstNonEmpty(os.Getenv("EVAL_"+envProviderName(providerID)+"_API_KEY"), os.Getenv("EVAL_API_KEY"), defaultAPIKey(providerID)),
			ModelID:    firstNonEmpty(os.Getenv("EVAL_"+envProviderName(providerID)+"_MODEL"), os.Getenv("EVAL_MODEL")),
		})
	}
	return specs
}

func graderSpecFromEnv(fallback ProviderSpec) ProviderSpec {
	providerID := strings.TrimSpace(os.Getenv("GRADER_PROVIDER"))
	if providerID == "" {
		providerID = fallback.ProviderID
	}
	providerID = strings.ToLower(providerID)

	return ProviderSpec{
		ProviderID: providerID,
		BaseURL:    firstNonEmpty(os.Getenv("GRADER_BASE_URL"), fallback.BaseURL, defaultBaseURL(providerID)),
		APIKey:     firstNonEmpty(os.Getenv("GRADER_API_KEY"), fallback.APIKey, defaultAPIKey(providerID)),
		ModelID:    firstNonEmpty(os.Getenv("GRADER_MODEL"), fallback.ModelID),
	}
}

func defaultBaseURL(providerID string) string {
	switch providerID {
	case "openai":
		return "https://api.openai.com/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	case "lmstudio":
		return "http://localhost:1234/v1"
	default:
		return ""
	}
}

func defaultAPIKey(providerID string) string {
	switch providerID {
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "deepseek":
		return os.Getenv("DEEPSEEK_API_KEY")
	default:
		return ""
	}
}

func envProviderName(providerID string) string {
	replacer := strings.NewReplacer("-", "_", ".", "_")
	return strings.ToUpper(replacer.Replace(providerID))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
