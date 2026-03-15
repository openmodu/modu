package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Workspace manages the shared filesystem for a creative team run.
//
// Layout:
//
//	workspace/
//	  agents/{id}/info.json   — agent 元数据（角色、状态、任务数）
//	  docs/{agentID}-{taskID}.md  — 各 agent 的任务产出
//	  final-{timestamp}.md    — 最终成稿 + 编辑寄语
type Workspace struct {
	root string
	mu   sync.Mutex
}

type agentInfo struct {
	AgentID   string `json:"agent_id"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	TaskCount int    `json:"task_count"`
	UpdatedAt string `json:"updated_at"`
}

func workspaceRoot() string {
	if v := os.Getenv("WORKSPACE_DIR"); v != "" {
		return v
	}
	return "workspace"
}

// NewWorkspace creates the directory structure and returns a Workspace.
func NewWorkspace(root string) (*Workspace, error) {
	for _, dir := range []string{
		filepath.Join(root, "agents"),
		filepath.Join(root, "docs"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("workspace mkdir %s: %w", dir, err)
		}
	}
	return &Workspace{root: root}, nil
}

// AgentDir returns the path used as coding_agent's AgentDir for the given agent.
// The directory is created on first call.
func (w *Workspace) AgentDir(agentID string) string {
	dir := filepath.Join(w.root, "agents", agentID)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// UpdateAgent saves or updates an agent's info.json.
func (w *Workspace) UpdateAgent(agentID, role, status string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	path := filepath.Join(w.root, "agents", agentID, "info.json")

	var info agentInfo
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &info)
	}
	info.AgentID = agentID
	info.Role = role
	info.Status = status
	info.UpdatedAt = time.Now().Format(time.RFC3339)

	data, _ := json.MarshalIndent(info, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
}

// IncrTaskCount increments the task counter for an agent.
func (w *Workspace) IncrTaskCount(agentID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	path := filepath.Join(w.root, "agents", agentID, "info.json")
	var info agentInfo
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &info)
	}
	info.TaskCount++
	data, _ := json.MarshalIndent(info, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
}

// SaveDoc writes a task result to docs/{agentID}-{taskID}.md and returns the file path.
func (w *Workspace) SaveDoc(agentID, taskID, title, content string) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	name := fmt.Sprintf("%s-%s.md", agentID, taskID)
	path := filepath.Join(w.root, "docs", name)

	body := fmt.Sprintf("# %s\n\n**Agent:** %s  \n**Task:** %s  \n**Time:** %s\n\n---\n\n%s\n",
		title, agentID, taskID, time.Now().Format("2006-01-02 15:04:05"), content)
	_ = os.WriteFile(path, []byte(body), 0o644)
	return path
}

// SaveFinal writes the completed article + editor's note and returns the file path.
func (w *Workspace) SaveFinal(brief, article, note string) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	ts := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("final-%s.md", ts)
	path := filepath.Join(w.root, name)

	body := fmt.Sprintf("# 创作成果\n\n**主题：** %s  \n**完成时间：** %s\n\n---\n\n## 正文\n\n%s\n\n---\n\n## 编辑寄语\n\n%s\n",
		brief, time.Now().Format("2006-01-02 15:04:05"), article, note)
	_ = os.WriteFile(path, []byte(body), 0o644)
	return path
}
