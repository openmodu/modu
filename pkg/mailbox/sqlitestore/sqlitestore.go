// Package sqlitestore 提供基于 SQLite 的 mailbox.Store 实现（纯 Go，无 CGO）。
package sqlitestore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/mailbox"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id           TEXT PRIMARY KEY,
	description  TEXT NOT NULL DEFAULT '',
	created_by   TEXT NOT NULL DEFAULT '',
	assigned_to  TEXT NOT NULL DEFAULT '',
	assignees    TEXT NOT NULL DEFAULT '[]',
	agent_results TEXT NOT NULL DEFAULT '{}',
	project_id   TEXT NOT NULL DEFAULT '',
	status       TEXT NOT NULL DEFAULT 'pending',
	created_at   INTEGER NOT NULL DEFAULT 0,
	updated_at   INTEGER NOT NULL DEFAULT 0,
	result       TEXT NOT NULL DEFAULT '',
	error        TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS projects (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT '',
	task_ids   TEXT NOT NULL DEFAULT '[]',
	status     TEXT NOT NULL DEFAULT 'active',
	created_at INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS agent_roles (
	agent_id TEXT PRIMARY KEY,
	role     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS conversations (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	at         INTEGER NOT NULL DEFAULT 0,
	from_agent TEXT    NOT NULL DEFAULT '',
	to_agent   TEXT    NOT NULL DEFAULT '',
	task_id    TEXT    NOT NULL DEFAULT '',
	msg_type   TEXT    NOT NULL DEFAULT '',
	content    TEXT    NOT NULL DEFAULT ''
);
`

// migrations 处理旧数据库的 schema 升级（新列）
var migrations = []string{
	`ALTER TABLE tasks ADD COLUMN assignees TEXT NOT NULL DEFAULT '[]'`,
	`ALTER TABLE tasks ADD COLUMN agent_results TEXT NOT NULL DEFAULT '{}'`,
	`ALTER TABLE tasks ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
}

// SQLiteStore 是 mailbox.Store 的 SQLite 实现
type SQLiteStore struct {
	db *sql.DB
}

// New 打开（或创建）SQLite 数据库并初始化表结构
func New(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dsn, err)
	}
	// 单写连接，避免锁竞争
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	// 对旧数据库执行迁移（新列），忽略"已存在"错误
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			if !strings.Contains(err.Error(), "duplicate column name") {
				db.Close()
				return nil, fmt.Errorf("migration %q: %w", m, err)
			}
		}
	}

	return &SQLiteStore{db: db}, nil
}

// SaveTask 创建或更新一条任务记录（UPSERT）
func (s *SQLiteStore) SaveTask(task mailbox.Task) error {
	assigneesJSON, _ := json.Marshal(task.Assignees)
	agentResultsJSON, _ := json.Marshal(task.AgentResults)

	_, err := s.db.Exec(`
		INSERT INTO tasks (id, description, created_by, assigned_to, assignees, agent_results, project_id, status, created_at, updated_at, result, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			assigned_to   = excluded.assigned_to,
			assignees     = excluded.assignees,
			agent_results = excluded.agent_results,
			project_id    = excluded.project_id,
			status        = excluded.status,
			updated_at    = excluded.updated_at,
			result        = excluded.result,
			error         = excluded.error
	`,
		task.ID,
		task.Description,
		task.CreatedBy,
		task.AssignedTo,
		string(assigneesJSON),
		string(agentResultsJSON),
		task.ProjectID,
		string(task.Status),
		task.CreatedAt.UnixNano(),
		task.UpdatedAt.UnixNano(),
		task.Result,
		task.Error,
	)
	return err
}

// LoadTasks 加载所有任务
func (s *SQLiteStore) LoadTasks() ([]mailbox.Task, error) {
	rows, err := s.db.Query(`
		SELECT id, description, created_by, assigned_to, assignees, agent_results, project_id,
		       status, created_at, updated_at, result, error
		FROM tasks
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []mailbox.Task
	for rows.Next() {
		var t mailbox.Task
		var status, assigneesStr, agentResultsStr string
		var createdNano, updatedNano int64

		if err := rows.Scan(
			&t.ID, &t.Description, &t.CreatedBy, &t.AssignedTo,
			&assigneesStr, &agentResultsStr, &t.ProjectID,
			&status, &createdNano, &updatedNano, &t.Result, &t.Error,
		); err != nil {
			return nil, err
		}
		t.Status = mailbox.TaskStatus(status)
		t.CreatedAt = time.Unix(0, createdNano)
		t.UpdatedAt = time.Unix(0, updatedNano)

		_ = json.Unmarshal([]byte(assigneesStr), &t.Assignees)
		if t.Assignees == nil {
			t.Assignees = []string{}
		}
		_ = json.Unmarshal([]byte(agentResultsStr), &t.AgentResults)
		if t.AgentResults == nil {
			t.AgentResults = make(map[string]string)
		}

		// 向后兼容：旧记录 assignees 为空但 assigned_to 有值
		if len(t.Assignees) == 0 && t.AssignedTo != "" {
			t.Assignees = []string{t.AssignedTo}
		}
		if len(t.Assignees) > 0 && t.AssignedTo == "" {
			t.AssignedTo = t.Assignees[0]
		}

		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// SaveProject 创建或更新一条项目记录（UPSERT）
func (s *SQLiteStore) SaveProject(proj mailbox.Project) error {
	taskIDsJSON, _ := json.Marshal(proj.TaskIDs)
	_, err := s.db.Exec(`
		INSERT INTO projects (id, name, created_by, task_ids, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name       = excluded.name,
			task_ids   = excluded.task_ids,
			status     = excluded.status,
			updated_at = excluded.updated_at
	`,
		proj.ID,
		proj.Name,
		proj.CreatedBy,
		string(taskIDsJSON),
		proj.Status,
		proj.CreatedAt.UnixNano(),
		proj.UpdatedAt.UnixNano(),
	)
	return err
}

// LoadProjects 加载所有项目
func (s *SQLiteStore) LoadProjects() ([]mailbox.Project, error) {
	rows, err := s.db.Query(`
		SELECT id, name, created_by, task_ids, status, created_at, updated_at
		FROM projects
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []mailbox.Project
	for rows.Next() {
		var p mailbox.Project
		var taskIDsStr string
		var createdNano, updatedNano int64

		if err := rows.Scan(
			&p.ID, &p.Name, &p.CreatedBy, &taskIDsStr, &p.Status,
			&createdNano, &updatedNano,
		); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(0, createdNano)
		p.UpdatedAt = time.Unix(0, updatedNano)

		_ = json.Unmarshal([]byte(taskIDsStr), &p.TaskIDs)
		if p.TaskIDs == nil {
			p.TaskIDs = []string{}
		}

		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// SaveAgentRole 持久化 agent 的角色（UPSERT）
func (s *SQLiteStore) SaveAgentRole(agentID, role string) error {
	_, err := s.db.Exec(`
		INSERT INTO agent_roles (agent_id, role) VALUES (?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET role = excluded.role
	`, agentID, role)
	return err
}

// LoadAgentRoles 加载所有 agent 的角色映射
func (s *SQLiteStore) LoadAgentRoles() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT agent_id, role FROM agent_roles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roles := make(map[string]string)
	for rows.Next() {
		var id, role string
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		roles[id] = role
	}
	return roles, rows.Err()
}

// SaveConversation 追加一条对话记录
func (s *SQLiteStore) SaveConversation(e mailbox.ConversationEntry) error {
	_, err := s.db.Exec(`
		INSERT INTO conversations (at, from_agent, to_agent, task_id, msg_type, content)
		VALUES (?, ?, ?, ?, ?, ?)
	`, e.At.UnixNano(), e.From, e.To, e.TaskID, string(e.MsgType), e.Content)
	return err
}

// LoadConversations 加载所有对话记录，按 task_id 分组，按时间升序
func (s *SQLiteStore) LoadConversations() (map[string][]mailbox.ConversationEntry, error) {
	rows, err := s.db.Query(`
		SELECT at, from_agent, to_agent, task_id, msg_type, content
		FROM conversations ORDER BY at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]mailbox.ConversationEntry)
	for rows.Next() {
		var e mailbox.ConversationEntry
		var atNano int64
		var msgType string
		if err := rows.Scan(&atNano, &e.From, &e.To, &e.TaskID, &msgType, &e.Content); err != nil {
			return nil, err
		}
		e.At = time.Unix(0, atNano)
		e.MsgType = mailbox.MessageType(msgType)
		result[e.TaskID] = append(result[e.TaskID], e)
	}
	return result, rows.Err()
}

// Close 关闭数据库连接
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
