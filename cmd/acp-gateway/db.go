package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id         TEXT PRIMARY KEY,
	agent      TEXT NOT NULL,
	prompt     TEXT NOT NULL,
	cwd        TEXT NOT NULL,
	status     TEXT NOT NULL,
	result     TEXT NOT NULL DEFAULT '',
	error      TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);
`

// openDB opens (or creates) the SQLite database at path and applies the schema.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("acp-gateway: open db %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("acp-gateway: init schema: %w", err)
	}
	return db, nil
}

// dbInsertTask persists a newly created task.
func dbInsertTask(db *sql.DB, t *Task) {
	if db == nil {
		return
	}
	_, err := db.Exec(
		`INSERT INTO tasks(id,agent,prompt,cwd,status,result,error,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Agent, t.Prompt, t.Cwd, string(t.Status),
		t.Result, t.Error,
		t.CreatedAt.UTC().Format(time.RFC3339Nano),
		t.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		log.Printf("[acp-gateway] db insert task %s: %v", t.ID, err)
	}
}

// dbUpdateTask updates status/result/error/updated_at for an existing task.
func dbUpdateTask(db *sql.DB, t *Task) {
	if db == nil {
		return
	}
	_, err := db.Exec(
		`UPDATE tasks SET status=?,result=?,error=?,updated_at=? WHERE id=?`,
		string(t.Status), t.Result, t.Error,
		t.UpdatedAt.UTC().Format(time.RFC3339Nano),
		t.ID,
	)
	if err != nil {
		log.Printf("[acp-gateway] db update task %s: %v", t.ID, err)
	}
}

// dbLoadTasks reads all tasks from the DB into the store's in-memory map.
// Tasks that were pending or running (i.e. interrupted by a prior restart)
// are immediately marked failed so the UI never shows them stuck.
func dbLoadTasks(db *sql.DB, store *Store) error {
	if db == nil {
		return nil
	}
	rows, err := db.Query(
		`SELECT id,agent,prompt,cwd,status,result,error,created_at,updated_at FROM tasks ORDER BY created_at`,
	)
	if err != nil {
		return fmt.Errorf("acp-gateway: load tasks: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	for rows.Next() {
		var (
			t          Task
			createdRaw string
			updatedRaw string
		)
		if err := rows.Scan(
			&t.ID, &t.Agent, &t.Prompt, &t.Cwd,
			&t.Status, &t.Result, &t.Error,
			&createdRaw, &updatedRaw,
		); err != nil {
			log.Printf("[acp-gateway] db scan: %v", err)
			continue
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdRaw)
		t.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedRaw)

		// Interrupted tasks: mark failed so they're not stuck in pending/running.
		if t.Status == TaskPending || t.Status == TaskRunning {
			t.Status = TaskFailed
			t.Error = "interrupted by gateway restart"
			t.UpdatedAt = now
			dbUpdateTask(db, &t)
		}

		tc := t
		store.mu.Lock()
		// Restore counter so new IDs don't collide.
		if n := parseTaskSeq(t.ID); n > 0 {
			for {
				cur := store.counter.Load()
				if cur >= n {
					break
				}
				if store.counter.CompareAndSwap(cur, n) {
					break
				}
			}
		}
		store.tasks[t.ID] = &taskEntry{
			task: &tc,
			subs: make(map[int]chan SSEEvent),
			done: true, // historical tasks are closed; no new events
		}
		store.mu.Unlock()
	}
	return rows.Err()
}

// parseTaskSeq extracts the numeric suffix from a task ID like "task-42".
func parseTaskSeq(id string) uint64 {
	var n uint64
	fmt.Sscanf(id, "task-%d", &n)
	return n
}
