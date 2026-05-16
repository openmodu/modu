package coding_agent

import "github.com/openmodu/modu/pkg/providers"

// DoctorInfo describes basic runtime health checks for a session.
type DoctorInfo struct {
	Cwd                string
	AgentDir           string
	ModelConfigPath    string
	ModelName          string
	ModelProvider      string
	ModelID            string
	ModelBaseURL       string
	ProviderRegistered bool
	APIKeyStatus       string
	ContextFileCount   int
	Problems           []string
}

// GetDoctorInfo returns a read-only snapshot of basic runtime health.
func (s *CodingSession) GetDoctorInfo() DoctorInfo {
	state := s.agent.GetState()
	info := DoctorInfo{
		Cwd:             s.cwd,
		AgentDir:        s.agentDir,
		ModelConfigPath: s.modelConfigPath,
		APIKeyStatus:    "not checked",
	}
	if state.Model == nil {
		info.Problems = append(info.Problems, "model is not configured")
		return info
	}

	info.ModelName = state.Model.Name
	info.ModelProvider = state.Model.ProviderID
	info.ModelID = state.Model.ID
	info.ModelBaseURL = state.Model.BaseURL
	if info.ModelProvider == "" {
		info.Problems = append(info.Problems, "model provider is empty")
	} else {
		_, info.ProviderRegistered = providers.Get(info.ModelProvider)
		if !info.ProviderRegistered {
			info.Problems = append(info.Problems, "provider is not registered: "+info.ModelProvider)
		}
	}
	if info.ModelID == "" {
		info.Problems = append(info.Problems, "model id is empty")
	}
	if info.ModelBaseURL == "" {
		info.Problems = append(info.Problems, "model baseUrl is empty")
	}

	if s.getAPIKey != nil && info.ModelProvider != "" {
		key, err := s.getAPIKey(info.ModelProvider)
		switch {
		case err != nil:
			info.APIKeyStatus = "missing: " + err.Error()
			info.Problems = append(info.Problems, "api key lookup failed")
		case key == "":
			info.APIKeyStatus = "empty"
		default:
			info.APIKeyStatus = "set"
		}
	}

	if s.resources != nil {
		info.ContextFileCount = len(s.resources.LoadContextFiles())
	}
	return info
}
