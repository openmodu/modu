package coding_agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/runtimepaths"
	"github.com/openmodu/modu/pkg/coding_agent/services/compaction"
	"github.com/openmodu/modu/pkg/coding_agent/services/session"
	toolcommon "github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

// Fork creates a new branch from the given entry ID.
func (s *CodingSession) Fork(entryID string) error {
	return s.sessionManager.Fork(entryID)
}

// GetSessionLeafID returns the current persisted session leaf entry ID.
func (s *CodingSession) GetSessionLeafID() string {
	if s.sessionManager == nil {
		return ""
	}
	return s.sessionManager.LastID()
}

// GetSessionBranches returns branch points in the current session tree.
func (s *CodingSession) GetSessionBranches() []SessionBranchInfo {
	if s.sessionTree == nil {
		return nil
	}
	branches := s.sessionTree.GetBranches()
	out := make([]SessionBranchInfo, 0, len(branches))
	for _, branch := range branches {
		out = append(out, SessionBranchInfo{
			ID:         branch.ID,
			ParentID:   branch.ParentID,
			Label:      branch.Label,
			EntryCount: len(branch.Entries),
		})
	}
	return out
}

// GetSessionTreeNodes returns a depth-first view of the current session tree.
func (s *CodingSession) GetSessionTreeNodes() []SessionTreeNode {
	if s.sessionManager == nil || s.sessionTree == nil {
		return nil
	}
	entries := s.sessionManager.Load()
	lookup := make(map[string]session.SessionEntry, len(entries))
	visible := make(map[string]struct{})
	for _, entry := range entries {
		lookup[entry.ID] = entry
		if session.IsVisibleEntry(entry) {
			visible[entry.ID] = struct{}{}
		}
	}
	children := make(map[string][]session.SessionEntry)
	for _, entry := range entries {
		if !session.IsVisibleEntry(entry) {
			continue
		}
		parentID := session.NearestVisibleParent(entry.ParentID, lookup, visible)
		children[parentID] = append(children[parentID], entry)
	}
	currentPath := make(map[string]struct{})
	for _, entry := range s.sessionTree.GetCurrentPath() {
		currentPath[entry.ID] = struct{}{}
	}
	currentID := s.sessionManager.LastID()
	var out []SessionTreeNode
	var walk func(parentID string, depth int)
	walk = func(parentID string, depth int) {
		for _, entry := range children[parentID] {
			_, inPath := currentPath[entry.ID]
			node := SessionTreeNode{
				ID:            entry.ID,
				ParentID:      session.NearestVisibleParent(entry.ParentID, lookup, visible),
				Type:          string(entry.Type),
				Role:          session.EntryRole(entry),
				Label:         session.TreeNodeLabel(entry, s.sessionManager.GetLabel(entry.ID)),
				Preview:       session.EntryPreview(entry),
				Depth:         depth,
				ChildCount:    len(children[entry.ID]),
				Current:       entry.ID == currentID,
				InCurrentPath: inPath,
				Timestamp:     entry.Timestamp,
			}
			out = append(out, node)
			walk(entry.ID, depth+1)
		}
	}
	walk("", 0)
	return out
}

// CreateBranchedSession creates a new session file from the path to entryID.
func (s *CodingSession) CreateBranchedSession(entryID string) (string, error) {
	if s.sessionManager == nil {
		return "", fmt.Errorf("session manager not available")
	}
	path, err := s.sessionManager.CreateBranchedSession(entryID)
	if err != nil {
		return "", err
	}
	s.sessionTree = session.NewTree(s.sessionManager)
	_, _ = s.RestoreMessages()
	s.writeRuntimeState()
	return path, nil
}

// NavigateTree navigates to a specific point in the session tree.
func (s *CodingSession) NavigateTree(entryID string) error {
	if s.sessionManager == nil || s.sessionTree == nil {
		return fmt.Errorf("session tree not available")
	}
	path := s.sessionTree.GetPath(entryID)
	if len(path) == 0 {
		return fmt.Errorf("entry %s not found", entryID)
	}
	if entryID == s.sessionManager.LastID() {
		_, err := s.RestoreMessages()
		return err
	}
	var msgs []types.AgentMessage
	for _, entry := range path {
		if entry.Type != session.EntryTypeMessage {
			continue
		}
		if msg, ok := agentMessageFromSessionData(entry.Data); ok && msg != nil {
			msgs = append(msgs, msg)
		}
	}
	summary, err := compaction.GenerateBranchSummary(context.Background(), msgs, compaction.BranchSummaryOptions{})
	if err != nil {
		return err
	}
	if _, err := s.sessionManager.BranchWithSummary(entryID, summary); err != nil {
		return err
	}
	s.sessionTree = session.NewTree(s.sessionManager)
	_, err = s.RestoreMessages()
	s.writeRuntimeState()
	return err
}

// GetSessionID returns the current session ID.
func (s *engine) GetSessionID() string {
	if s.sessionManager != nil {
		return s.sessionManager.SessionID()
	}
	return s.agent.GetSessionID()
}

// GetSessionFile returns the session file path.
func (s *CodingSession) GetSessionFile() string {
	return s.sessionManager.FilePath()
}

// ListSessions returns persisted sessions for the current working directory.
func (s *CodingSession) ListSessions() ([]session.SessionInfo, error) {
	return session.List(s.agentDir, s.cwd)
}

func (s *CodingSession) ListSessionInfos() ([]SessionInfo, error) {
	return s.ListSessions()
}

// ListAllSessions returns persisted sessions across all working directories.
func (s *CodingSession) ListAllSessions() ([]session.SessionInfo, error) {
	return session.ListAll(s.agentDir)
}

func (s *CodingSession) ListAllSessionInfos() ([]SessionInfo, error) {
	return s.ListAllSessions()
}

// ForkFromSession creates and switches to a new session copied from sessionFile.
func (s *CodingSession) ForkFromSession(sessionFile string) error {
	mgr, err := session.ForkFrom(s.agentDir, sessionFile, s.cwd)
	if err != nil {
		return err
	}
	return s.switchSessionManager(mgr)
}

// DeleteSession removes a saved session file, except the active session.
func (s *CodingSession) DeleteSession(sessionFile string) error {
	current, err := filepath.Abs(s.GetSessionFile())
	if err != nil {
		return err
	}
	target, err := filepath.Abs(sessionFile)
	if err != nil {
		return err
	}
	if current == target {
		return fmt.Errorf("refusing to delete the active session")
	}
	return session.Delete(s.agentDir, sessionFile)
}

// SetSessionName sets the display name for this session.
func (s *CodingSession) SetSessionName(name string) {
	s.sessionName = name
	if s.sessionManager != nil {
		_ = s.sessionManager.AppendSessionInfo(name)
	}
}

// GetSessionName returns the display name for this session.
func (s *CodingSession) GetSessionName() string {
	if s.sessionManager != nil {
		return s.sessionManager.SessionName()
	}
	return s.sessionName
}

// GetForkMessages returns user messages from the session history, suitable for forking.
func (s *CodingSession) GetForkMessages() []ForkMessage {
	entries := s.sessionTree.GetCurrentPath()
	var result []ForkMessage
	for _, entry := range entries {
		if entry.Type != session.EntryTypeMessage {
			continue
		}
		data, ok := entry.Data.(session.MessageData)
		if !ok {
			// Try map-based extraction (from JSON deserialization)
			if m, ok := entry.Data.(map[string]any); ok {
				role, _ := m["role"].(string)
				if role != string(types.RoleUser) {
					continue
				}
				content, _ := m["content"].(string)
				result = append(result, ForkMessage{
					EntryID: entry.ID,
					Role:    role,
					Content: content,
				})
			}
			continue
		}
		if data.Role != types.RoleUser {
			continue
		}
		content, _ := data.Content.(string)
		result = append(result, ForkMessage{
			EntryID: entry.ID,
			Role:    string(data.Role),
			Content: content,
		})
	}
	return result
}

// GetSessionStats returns aggregate statistics for the current session.
func (s *CodingSession) GetSessionStats() SessionStats {
	msgs := s.agent.GetState().Messages
	now := time.Now().UnixMilli()
	return SessionStats{
		TotalTokens:    s.ctxMgr.Tokens(),
		MessageCount:   len(msgs),
		SessionStarted: s.sessionStarted,
		DurationMs:     now - s.sessionStarted,
	}
}

// ResumeByID switches to a saved session identified by id (full id or a unique
// prefix), including sessions created in a different cwd.
func (s *CodingSession) ResumeByID(id string) error {
	info, err := ResolveSessionInfoByID(s.agentDir, id)
	if err != nil {
		return err
	}
	return s.resumeSession(info.Path, info.Cwd)
}

// ResumeSession switches to a saved session file and, when its recorded cwd
// differs, asks whether to use the session directory or the current directory.
func (s *CodingSession) ResumeSession(sessionFile string) error {
	header, err := session.ReadSessionHeader(sessionFile)
	if err != nil {
		return fmt.Errorf("read session header: %w", err)
	}
	return s.resumeSession(sessionFile, header.Cwd)
}

func (s *CodingSession) resumeSession(sessionFile, sessionCwd string) error {
	newMgr, err := session.NewManagerFromFile(sessionFile)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	currentCwd := s.cwd
	selectedCwd := currentCwd
	if strings.TrimSpace(sessionCwd) != "" && resumeCwdsDiffer(currentCwd, sessionCwd) {
		sessionOption := "Use session directory (" + sessionCwd + ")"
		currentOption := "Use current directory (" + currentCwd + ")"
		choice := s.requestExtensionSelect(
			"Choose working directory to resume this session",
			[]string{sessionOption, currentOption},
		)
		if choice == sessionOption {
			selectedCwd = sessionCwd
		}
	}
	if selectedCwd != currentCwd {
		s.rebindProjectResources(selectedCwd)
		s.SwitchCwd(selectedCwd)
	}
	return s.switchSessionManager(newMgr)
}

func resumeCwdsDiffer(currentCwd, sessionCwd string) bool {
	currentInfo, currentErr := os.Stat(currentCwd)
	sessionInfo, sessionErr := os.Stat(sessionCwd)
	if currentErr == nil && sessionErr == nil {
		return !os.SameFile(currentInfo, sessionInfo)
	}
	currentAbs, currentErr := filepath.Abs(currentCwd)
	sessionAbs, sessionErr := filepath.Abs(sessionCwd)
	if currentErr != nil || sessionErr != nil {
		return currentCwd != sessionCwd
	}
	currentAbs = filepath.Clean(currentAbs)
	sessionAbs = filepath.Clean(sessionAbs)
	if runtime.GOOS == "windows" {
		return !strings.EqualFold(currentAbs, sessionAbs)
	}
	return currentAbs != sessionAbs
}

// ResolveSessionInfoByID resolves a persisted session globally and includes
// the cwd recorded in its header without loading the transcript.
func ResolveSessionInfoByID(agentDir, id string) (SessionInfo, error) {
	info, err := resolveSessionInfoByID(agentDir, id)
	if err != nil {
		return SessionInfo{}, err
	}
	header, err := session.ReadSessionHeader(info.Path)
	if err != nil {
		return SessionInfo{}, fmt.Errorf("read session header: %w", err)
	}
	info.Cwd = header.Cwd
	return info, nil
}

func newSessionManager(agentDir, cwd, resumeID string) (*session.Manager, error) {
	if strings.TrimSpace(resumeID) == "" {
		return session.NewFreshManager(agentDir, cwd)
	}
	info, err := resolveSessionInfoByID(agentDir, resumeID)
	if err != nil {
		return nil, err
	}
	return session.NewManagerFromFile(info.Path)
}

func resolveSessionInfoByID(agentDir, id string) (session.SessionInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return session.SessionInfo{}, fmt.Errorf("session id is required")
	}
	// Session files are named <id>.jsonl, so global id resolution only scans
	// cwd directories and filenames — never session contents. The previous
	// implementation went through session.List, which summarizes every file;
	// against a multi-GB session dir that made `--resume <id>` burn ~40s
	// before even starting.
	info, ok, err := session.FindByIDPrefixAll(agentDir, id)
	if err != nil {
		return session.SessionInfo{}, err
	}
	if !ok {
		return session.SessionInfo{}, fmt.Errorf("no session found with id %q", id)
	}
	return info, nil
}

// SwitchSession loads messages from a different session file and replaces the current agent messages.
func (s *CodingSession) SwitchSession(sessionFile string) error {
	newMgr, err := session.NewManagerFromFile(sessionFile)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}
	return s.switchSessionManager(newMgr)
}

func (s *CodingSession) switchSessionManager(newMgr *session.Manager) error {
	var messages []types.AgentMessage
	newTree := session.NewTree(newMgr)
	for _, entry := range newTree.GetCurrentPath() {
		if entry.Type != session.EntryTypeMessage {
			continue
		}
		if msg, ok := agentMessageFromSessionData(entry.Data); ok && msg != nil {
			messages = append(messages, msg)
		}
	}

	s.sessionManager = newMgr
	s.sessionTree = newTree
	s.sessionName = newMgr.SessionName()
	s.artifactStore = toolcommon.NewArtifactStore(
		runtimepaths.SessionToolResultsDir(s.agentDir, s.cwd, newMgr.SessionID()),
	)
	s.refreshToolsForCwd(s.cwd)
	s.agent.ReplaceMessages(messages)
	s.lastSavedIndex = len(messages)
	if s.extensions != nil {
		s.extensions.EmitEvent(types.Event{Type: types.EventType("session_start"), Reason: "resume"})
	}
	s.writeRuntimeState()
	return nil
}
