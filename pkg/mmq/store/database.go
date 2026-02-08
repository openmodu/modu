package store

import (
	"database/sql"
	"fmt"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

// schema SQLite数据库schema
const schema = `
-- 内容寻址存储（Content-Addressable Storage）
CREATE TABLE IF NOT EXISTS content (
    hash TEXT PRIMARY KEY,
    doc TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- 文档元数据
CREATE TABLE IF NOT EXISTS documents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    collection TEXT NOT NULL,
    path TEXT NOT NULL,
    title TEXT NOT NULL,
    hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    modified_at TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    FOREIGN KEY (hash) REFERENCES content(hash) ON DELETE CASCADE,
    UNIQUE(collection, path)
);

-- 索引优化
CREATE INDEX IF NOT EXISTS idx_documents_collection ON documents(collection, active);
CREATE INDEX IF NOT EXISTS idx_documents_hash ON documents(hash);
CREATE INDEX IF NOT EXISTS idx_documents_path ON documents(path, active);

-- 向量嵌入元数据
CREATE TABLE IF NOT EXISTS content_vectors (
    hash TEXT NOT NULL,
    seq INTEGER NOT NULL DEFAULT 0,
    pos INTEGER NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    embedding BLOB,
    embedded_at TEXT NOT NULL,
    PRIMARY KEY (hash, seq)
);

-- FTS5全文搜索索引
CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
    filepath, title, body,
    tokenize='porter unicode61'
);

-- LLM缓存
CREATE TABLE IF NOT EXISTS llm_cache (
    hash TEXT PRIMARY KEY,
    result TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- 记忆存储
CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata TEXT,
    tags TEXT,
    timestamp TEXT NOT NULL,
    expires_at TEXT,
    importance REAL NOT NULL DEFAULT 0.5,
    embedding BLOB
);

-- 记忆索引
CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type);
CREATE INDEX IF NOT EXISTS idx_memories_timestamp ON memories(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_memories_expires ON memories(expires_at);

-- 集合管理
CREATE TABLE IF NOT EXISTS collections (
    name TEXT PRIMARY KEY,
    path TEXT NOT NULL,
    mask TEXT NOT NULL DEFAULT '**/*',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- 集合索引
CREATE INDEX IF NOT EXISTS idx_collections_path ON collections(path);

-- 上下文管理
CREATE TABLE IF NOT EXISTS contexts (
    path TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- 上下文索引
CREATE INDEX IF NOT EXISTS idx_contexts_path ON contexts(path);

-- 触发器：INSERT时同步FTS
CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents
BEGIN
    INSERT INTO documents_fts (rowid, filepath, title, body)
    SELECT NEW.id, NEW.collection || '/' || NEW.path, NEW.title, content.doc
    FROM content WHERE content.hash = NEW.hash;
END;

-- 触发器：UPDATE时同步FTS
CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents
BEGIN
    DELETE FROM documents_fts WHERE rowid = OLD.id;
    INSERT INTO documents_fts (rowid, filepath, title, body)
    SELECT NEW.id, NEW.collection || '/' || NEW.path, NEW.title, content.doc
    FROM content WHERE content.hash = NEW.hash AND NEW.active = 1;
END;

-- 触发器：DELETE时清理FTS
CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents
BEGIN
    DELETE FROM documents_fts WHERE rowid = OLD.id;
END;
`

// Store 数据存储
type Store struct {
	db     *sql.DB
	dbPath string
}

// New 创建新的Store实例
func New(dbPath string) (*Store, error) {
	// 初始化 sqlite-vec 扩展
	sqlite_vec.Auto()

	// 打开数据库
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 启用WAL模式（Write-Ahead Logging）
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// 启用外键约束
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// 初始化schema
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return &Store{
		db:     db,
		dbPath: dbPath,
	}, nil
}

// Close 关闭数据库连接
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// DB 返回底层数据库连接（用于高级操作）
func (s *Store) DB() *sql.DB {
	return s.db
}

// ensureVectorTable 确保vectors_vec虚拟表存在
// 如果表不存在，根据提供的向量维度创建它
func (s *Store) ensureVectorTable(dimensions int) error {
	// 检查表是否存在
	var tableName string
	err := s.db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='vectors_vec'
	`).Scan(&tableName)

	if err == sql.ErrNoRows {
		// 表不存在，创建它
		createSQL := fmt.Sprintf(
			"CREATE VIRTUAL TABLE vectors_vec USING vec0(hash_seq TEXT PRIMARY KEY, embedding float[%d] distance_metric=cosine)",
			dimensions,
		)
		if _, err := s.db.Exec(createSQL); err != nil {
			return fmt.Errorf("failed to create vectors_vec table: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check vectors_vec table: %w", err)
	}

	return nil
}
