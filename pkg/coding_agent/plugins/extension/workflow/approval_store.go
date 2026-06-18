package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type workflowApprovalStoreFile struct {
	Approvals []workflowApprovalRecord `json:"approvals,omitempty"`
}

type workflowApprovalRecord struct {
	Project    string `json:"project"`
	Workflow   string `json:"workflow"`
	Source     string `json:"source,omitempty"`
	ScriptHash string `json:"scriptHash"`
	ApprovedAt int64  `json:"approvedAt"`
}

type workflowApprovalKey struct {
	Project    string
	Workflow   string
	Source     string
	ScriptHash string
}

func (e *Extension) workflowApprovalKey(summary workflowApprovalSummary, exec workflowExecution, source string) (workflowApprovalKey, bool) {
	if e == nil || e.api == nil {
		return workflowApprovalKey{}, false
	}
	agentDir := strings.TrimSpace(e.api.AgentDir())
	cwd := strings.TrimSpace(e.api.Cwd())
	if agentDir == "" || cwd == "" || strings.TrimSpace(exec.Script) == "" {
		return workflowApprovalKey{}, false
	}
	name := strings.TrimSpace(summary.Name)
	if name == "" {
		name = "unnamed"
	}
	sum := sha256.Sum256([]byte(normalizeScript(exec.Script)))
	return workflowApprovalKey{
		Project:    findWorkflowProjectRoot(cwd),
		Workflow:   name,
		Source:     strings.TrimSpace(source),
		ScriptHash: hex.EncodeToString(sum[:]),
	}, true
}

func (e *Extension) workflowApprovalStorePath() string {
	if e == nil || e.api == nil || strings.TrimSpace(e.api.AgentDir()) == "" {
		return ""
	}
	return filepath.Join(e.api.AgentDir(), "workflow_approvals.json")
}

func (e *Extension) workflowApprovalAllowed(key workflowApprovalKey) bool {
	path := e.workflowApprovalStorePath()
	if path == "" {
		return false
	}
	store, err := readWorkflowApprovalStore(path)
	if err != nil {
		return false
	}
	for _, record := range store.Approvals {
		if record.Project == key.Project && record.Workflow == key.Workflow && record.ScriptHash == key.ScriptHash {
			if record.Source == "" || key.Source == "" || record.Source == key.Source {
				return true
			}
		}
	}
	return false
}

func (e *Extension) rememberWorkflowApproval(key workflowApprovalKey) error {
	path := e.workflowApprovalStorePath()
	if path == "" {
		return nil
	}
	store, err := readWorkflowApprovalStore(path)
	if err != nil {
		return err
	}
	for i, record := range store.Approvals {
		if record.Project == key.Project && record.Workflow == key.Workflow && record.Source == key.Source && record.ScriptHash == key.ScriptHash {
			store.Approvals[i].ApprovedAt = time.Now().UnixMilli()
			return writeWorkflowApprovalStore(path, store)
		}
	}
	store.Approvals = append(store.Approvals, workflowApprovalRecord{
		Project:    key.Project,
		Workflow:   key.Workflow,
		Source:     key.Source,
		ScriptHash: key.ScriptHash,
		ApprovedAt: time.Now().UnixMilli(),
	})
	return writeWorkflowApprovalStore(path, store)
}

func readWorkflowApprovalStore(path string) (workflowApprovalStoreFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return workflowApprovalStoreFile{}, nil
		}
		return workflowApprovalStoreFile{}, fmt.Errorf("read workflow approvals %s: %w", path, err)
	}
	var store workflowApprovalStoreFile
	if err := json.Unmarshal(data, &store); err != nil {
		return workflowApprovalStoreFile{}, fmt.Errorf("decode workflow approvals %s: %w", path, err)
	}
	return store, nil
}

func writeWorkflowApprovalStore(path string, store workflowApprovalStoreFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create workflow approvals dir: %w", err)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workflow approvals: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
