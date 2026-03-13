// Package sqlitestore 提供基于 SQLite 的 mailbox.Store 实现（纯 Go，无 CGO）。
package sqlitestore

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/crosszan/modu/pkg/mailbox"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id          TEXT PRIMARY KEY,
	description TEXT NOT NULL DEFAULT '',
	created_by  TEXT NOT NULL DEFAULT '',
	assigned_to TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'pending',
	created_at  INTEGER NOT NULL DEFAULT 0,
	updated_at  INTEGER NOT NULL DEFAULT 0,
	result      TEXT NOT NULL DEFAULT '',
	error       TEXT NOT NULL DEFAULT ''
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
	return &SQLiteStore{db: db}, nil
}

// SaveTask 创建或更新一条任务记录（UPSERT）
func (s *SQLiteStore) SaveTask(task mailbox.Task) error {
	_, err := s.db.Exec(`
		INSERT INTO tasks (id, description, created_by, assigned_to, status, created_at, updated_at, result, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			assigned_to = excluded.assigned_to,
			status      = excluded.status,
			updated_at  = excluded.updated_at,
			result      = excluded.result,
			error       = excluded.error
	`,
		task.ID,
		task.Description,
		task.CreatedBy,
		task.AssignedTo,
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
		SELECT id, description, created_by, assigned_to, status,
		       created_at, updated_at, result, error
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
		var status string
		var createdNano, updatedNano int64

		if err := rows.Scan(
			&t.ID, &t.Description, &t.CreatedBy, &t.AssignedTo, &status,
			&createdNano, &updatedNano, &t.Result, &t.Error,
		); err != nil {
			return nil, err
		}
		t.Status = mailbox.TaskStatus(status)
		t.CreatedAt = time.Unix(0, createdNano)
		t.UpdatedAt = time.Unix(0, updatedNano)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
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
