package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS projects (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	path       TEXT NOT NULL,
	created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	agent      TEXT NOT NULL,
	title      TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL DEFAULT 'idle',
	cwd        TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS turns (
	id         TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	agent      TEXT NOT NULL DEFAULT '',
	cwd        TEXT NOT NULL DEFAULT '',
	prompt     TEXT NOT NULL,
	result     TEXT NOT NULL DEFAULT '',
	error      TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);
`

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("acp-gateway: open db %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("acp-gateway: init schema: %w", err)
	}
	return db, nil
}

// ---------- project ----------

func dbInsertProject(db *sql.DB, p *Project) {
	if db == nil {
		return
	}
	_, err := db.Exec(
		`INSERT INTO projects(id,name,path,created_at) VALUES(?,?,?,?)`,
		p.ID, p.Name, p.Path, p.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		log.Printf("[acp-gateway] db insert project %s: %v", p.ID, err)
	}
}

func dbDeleteProject(db *sql.DB, id string) {
	if db == nil {
		return
	}
	if _, err := db.Exec(`DELETE FROM projects WHERE id=?`, id); err != nil {
		log.Printf("[acp-gateway] db delete project %s: %v", id, err)
	}
}

// ---------- session ----------

func dbInsertSession(db *sql.DB, s *Session) {
	if db == nil {
		return
	}
	_, err := db.Exec(
		`INSERT INTO sessions(id,project_id,agent,title,status,cwd,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		s.ID, s.ProjectID, s.Agent, s.Title, string(s.Status), s.Cwd,
		s.CreatedAt.UTC().Format(time.RFC3339Nano),
		s.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		log.Printf("[acp-gateway] db insert session %s: %v", s.ID, err)
	}
}

func dbUpdateSession(db *sql.DB, s *Session) {
	if db == nil {
		return
	}
	_, err := db.Exec(
		`UPDATE sessions SET title=?,status=?,updated_at=? WHERE id=?`,
		s.Title, string(s.Status), s.UpdatedAt.UTC().Format(time.RFC3339Nano), s.ID,
	)
	if err != nil {
		log.Printf("[acp-gateway] db update session %s: %v", s.ID, err)
	}
}

func dbDeleteSession(db *sql.DB, id string) {
	if db == nil {
		return
	}
	if _, err := db.Exec(`DELETE FROM sessions WHERE id=?`, id); err != nil {
		log.Printf("[acp-gateway] db delete session %s: %v", id, err)
	}
	if _, err := db.Exec(`DELETE FROM turns WHERE session_id=?`, id); err != nil {
		log.Printf("[acp-gateway] db delete turns for session %s: %v", id, err)
	}
}

// ---------- turn ----------

func dbInsertTurn(db *sql.DB, t *Turn) {
	if db == nil {
		return
	}
	_, err := db.Exec(
		`INSERT INTO turns(id,session_id,agent,cwd,prompt,result,error,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.SessionID, t.Agent, t.Cwd, t.Prompt, t.Result, t.Error, string(t.Status),
		t.CreatedAt.UTC().Format(time.RFC3339Nano),
		t.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		log.Printf("[acp-gateway] db insert turn %s: %v", t.ID, err)
	}
}

func dbUpdateTurn(db *sql.DB, t *Turn) {
	if db == nil {
		return
	}
	_, err := db.Exec(
		`UPDATE turns SET status=?,result=?,error=?,updated_at=? WHERE id=?`,
		string(t.Status), t.Result, t.Error,
		t.UpdatedAt.UTC().Format(time.RFC3339Nano), t.ID,
	)
	if err != nil {
		log.Printf("[acp-gateway] db update turn %s: %v", t.ID, err)
	}
}

// ---------- load on startup ----------

func dbLoadAll(db *sql.DB, store *Store) error {
	if db == nil {
		return nil
	}
	if err := dbLoadProjects(db, store); err != nil {
		return err
	}
	if err := dbLoadSessions(db, store); err != nil {
		return err
	}
	return dbLoadTurns(db, store)
}

func dbLoadProjects(db *sql.DB, store *Store) error {
	rows, err := db.Query(`SELECT id,name,path,created_at FROM projects ORDER BY created_at`)
	if err != nil {
		return fmt.Errorf("load projects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p Project
		var createdRaw string
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &createdRaw); err != nil {
			log.Printf("[acp-gateway] db scan project: %v", err)
			continue
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdRaw)
		if n := parseProjectSeq(p.ID); n > 0 {
			for {
				cur := store.pctr.Load()
				if cur >= n {
					break
				}
				if store.pctr.CompareAndSwap(cur, n) {
					break
				}
			}
		}
		pc := p
		store.mu.Lock()
		store.projects[p.ID] = &pc
		store.mu.Unlock()
	}
	return rows.Err()
}

func dbLoadSessions(db *sql.DB, store *Store) error {
	rows, err := db.Query(
		`SELECT id,project_id,agent,title,status,cwd,created_at,updated_at FROM sessions ORDER BY created_at`,
	)
	if err != nil {
		return fmt.Errorf("load sessions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s Session
		var createdRaw, updatedRaw string
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Agent, &s.Title, &s.Status, &s.Cwd, &createdRaw, &updatedRaw); err != nil {
			log.Printf("[acp-gateway] db scan session: %v", err)
			continue
		}
		s.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdRaw)
		s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedRaw)
		// Sessions interrupted mid-run are marked idle on restart.
		if s.Status == SessionRunning {
			s.Status = SessionIdle
			s.UpdatedAt = time.Now().UTC()
			dbUpdateSession(db, &s)
		}
		if n := parseSessionSeq(s.ID); n > 0 {
			for {
				cur := store.sctr.Load()
				if cur >= n {
					break
				}
				if store.sctr.CompareAndSwap(cur, n) {
					break
				}
			}
		}
		sc := s
		store.mu.Lock()
		store.sessions[s.ID] = &sessionEntry{session: &sc}
		store.mu.Unlock()
	}
	return rows.Err()
}

func dbLoadTurns(db *sql.DB, store *Store) error {
	rows, err := db.Query(
		`SELECT id,session_id,agent,cwd,prompt,result,error,status,created_at,updated_at FROM turns ORDER BY created_at`,
	)
	if err != nil {
		return fmt.Errorf("load turns: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC()
	for rows.Next() {
		var t Turn
		var createdRaw, updatedRaw string
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Agent, &t.Cwd, &t.Prompt, &t.Result, &t.Error, &t.Status, &createdRaw, &updatedRaw); err != nil {
			log.Printf("[acp-gateway] db scan turn: %v", err)
			continue
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdRaw)
		t.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedRaw)
		if t.Status == TurnPending || t.Status == TurnRunning {
			t.Status = TurnFailed
			t.Error = "interrupted by gateway restart"
			t.UpdatedAt = now
			dbUpdateTurn(db, &t)
		}
		if n := parseTurnSeq(t.ID); n > 0 {
			for {
				cur := store.tctr.Load()
				if cur >= n {
					break
				}
				if store.tctr.CompareAndSwap(cur, n) {
					break
				}
			}
		}
		tc := t
		store.mu.Lock()
		store.turns[t.ID] = &turnEntry{
			turn: &tc,
			subs: make(map[int]chan SSEEvent),
			done: true,
		}
		if se, ok := store.sessions[t.SessionID]; ok {
			se.turns = append(se.turns, t.ID)
		}
		store.mu.Unlock()
	}
	return rows.Err()
}
