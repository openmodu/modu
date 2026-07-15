package coding_agent

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openmodu/modu/pkg/providers"
)

const doctorReachabilityTimeout = 800 * time.Millisecond

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
	BaseURLStatus      string
	BaseURLReachable   bool
	ContextFileCount   int
	MCPServerCount     int
	MCPToolCount       int
	Problems           []string
}

// GetDoctorInfo returns a read-only snapshot of basic runtime health.
func (s *CodingSession) GetDoctorInfo(ctx context.Context) DoctorInfo {
	if ctx == nil {
		ctx = context.Background()
	}
	state := s.agent.GetState()
	info := DoctorInfo{
		Cwd:             s.cwd,
		AgentDir:        s.agentDir,
		ModelConfigPath: s.modelConfigPath,
		APIKeyStatus:    "not checked",
		BaseURLStatus:   "not checked",
	}
	if s.mcpManager != nil {
		info.MCPServerCount = s.mcpManager.ServerCount()
		info.MCPToolCount = len(s.mcpManager.Tools())
	}
	for _, warning := range s.mcpWarnings {
		info.Problems = append(info.Problems, warning.Error())
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
		info.BaseURLStatus = "empty"
	} else {
		info.BaseURLReachable, info.BaseURLStatus = checkBaseURLReachable(ctx, info.ModelBaseURL)
		if !info.BaseURLReachable {
			info.Problems = append(info.Problems, "baseUrl not reachable: "+info.BaseURLStatus)
		}
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

func checkBaseURLReachable(ctx context.Context, baseURL string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, doctorReachabilityTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return false, err.Error()
	}
	client := &http.Client{Timeout: doctorReachabilityTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	return true, fmt.Sprintf("reachable (HTTP %d)", resp.StatusCode)
}
