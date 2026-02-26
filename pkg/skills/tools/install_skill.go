package skillstools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/skills"
)

// InstallSkillTool allows the LLM agent to install skills from registries.
type InstallSkillTool struct {
	registryMgr *skills.RegistryManager
	workspace   string
	mu          sync.Mutex
}

// NewInstallSkillTool creates a new InstallSkillTool.
// registryMgr is the shared registry manager.
// workspace is the root workspace directory; skills are installed to {workspace}/skills/{slug}/.
func NewInstallSkillTool(registryMgr *skills.RegistryManager, workspace string) *InstallSkillTool {
	return &InstallSkillTool{
		registryMgr: registryMgr,
		workspace:   workspace,
	}
}

func (t *InstallSkillTool) Name() string  { return "install_skill" }
func (t *InstallSkillTool) Label() string { return "Install Skill" }
func (t *InstallSkillTool) Description() string {
	return "Install a skill from a registry by slug. Downloads and extracts the skill into the workspace. Use find_skills first to discover available skills."
}
func (t *InstallSkillTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"slug": map[string]any{
				"type":        "string",
				"description": "The unique slug of the skill to install (e.g., 'github', 'docker-compose')",
			},
			"version": map[string]any{
				"type":        "string",
				"description": "Specific version to install (optional, defaults to latest)",
			},
			"registry": map[string]any{
				"type":        "string",
				"description": "Registry to install from (required, e.g., 'clawhub')",
			},
			"force": map[string]any{
				"type":        "boolean",
				"description": "Force reinstall if skill already exists (default false)",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Short human-readable label",
			},
		},
		"required": []string{"slug", "registry"},
	}
}

func (t *InstallSkillTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	// Serialize installs at workspace level to prevent concurrent directory operations.
	t.mu.Lock()
	defer t.mu.Unlock()

	slug, _ := args["slug"].(string)
	if err := validateInstallIdentifier(slug); err != nil {
		return errorResult(fmt.Sprintf("invalid slug %q: %s", slug, err)), nil
	}

	registryName, _ := args["registry"].(string)
	if err := validateInstallIdentifier(registryName); err != nil {
		return errorResult(fmt.Sprintf("invalid registry %q: %s", registryName, err)), nil
	}

	version, _ := args["version"].(string)
	force, _ := args["force"].(bool)

	skillsDir := filepath.Join(t.workspace, "skills")
	targetDir := filepath.Join(skillsDir, slug)

	if !force {
		if _, err := os.Stat(targetDir); err == nil {
			return errorResult(fmt.Sprintf("skill %q already installed at %s. Use force=true to reinstall.", slug, targetDir)), nil
		}
	} else {
		os.RemoveAll(targetDir)
	}

	registry := t.registryMgr.GetRegistry(registryName)
	if registry == nil {
		return errorResult(fmt.Sprintf("registry %q not found", registryName)), nil
	}

	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return errorResult(fmt.Sprintf("failed to create skills directory: %v", err)), nil
	}

	result, err := registry.DownloadAndInstall(ctx, slug, version, targetDir)
	if err != nil {
		os.RemoveAll(targetDir)
		return errorResult(fmt.Sprintf("failed to install %q: %v", slug, err)), nil
	}

	// Block malware.
	if result.IsMalwareBlocked {
		os.RemoveAll(targetDir)
		return errorResult(fmt.Sprintf("skill %q is flagged as malicious and cannot be installed", slug)), nil
	}

	// Write origin metadata.
	_ = writeOriginMeta(targetDir, registry.Name(), slug, result.Version)

	var output string
	if result.IsSuspicious {
		output = fmt.Sprintf("⚠️ Warning: skill %q is flagged as suspicious (may contain risky patterns).\n\n", slug)
	}
	output += fmt.Sprintf("Successfully installed skill %q v%s from %s registry.\nLocation: %s\n",
		slug, result.Version, registry.Name(), targetDir)
	if result.Summary != "" {
		output += fmt.Sprintf("Description: %s\n", result.Summary)
	}
	output += "\nThe skill is now available and can be loaded in the current session."

	return textResult(output), nil
}

// originMeta tracks which registry a skill was installed from.
type originMeta struct {
	Version          int    `json:"version"`
	Registry         string `json:"registry"`
	Slug             string `json:"slug"`
	InstalledVersion string `json:"installed_version"`
	InstalledAt      int64  `json:"installed_at"`
}

func writeOriginMeta(targetDir, registryName, slug, version string) error {
	meta := originMeta{
		Version:          1,
		Registry:         registryName,
		Slug:             slug,
		InstalledVersion: version,
		InstalledAt:      time.Now().UnixMilli(),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(targetDir, ".skill-origin.json"), data, 0o644)
}

// validateInstallIdentifier validates a slug or registry name for safety.
func validateInstallIdentifier(identifier string) error {
	if identifier == "" {
		return fmt.Errorf("identifier is required")
	}
	for _, c := range identifier {
		if c == '/' || c == '\\' || c == '.' {
			return fmt.Errorf("identifier must not contain path separators or dots")
		}
	}
	return nil
}

// Compile-time interface check.
var _ agent.AgentTool = (*InstallSkillTool)(nil)
