package store

import (
	"fmt"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

// GetDocumentsNeedingEmbedding 获取需要生成嵌入的文档
func (s *Store) GetDocumentsNeedingEmbedding() ([]Document, error) {
	query := `
		SELECT DISTINCT d.hash, c.doc
		FROM documents d
		JOIN content c ON c.hash = d.hash
		LEFT JOIN content_vectors v ON d.hash = v.hash AND v.seq = 0
		WHERE d.active = 1 AND v.hash IS NULL
		ORDER BY d.modified_at DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var doc Document
		err := rows.Scan(&doc.Hash, &doc.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to scan document: %w", err)
		}
		docs = append(docs, doc)
	}

	return docs, nil
}

// StoreEmbedding 存储嵌入向量
func (s *Store) StoreEmbedding(hash string, seq int, pos int, embedding []float32, model string) error {
	// 确保 vectors_vec 虚拟表存在
	if err := s.ensureVectorTable(len(embedding)); err != nil {
		return fmt.Errorf("failed to ensure vector table: %w", err)
	}

	// 序列化向量（content_vectors 和 vectors_vec 共用）
	blob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return fmt.Errorf("failed to serialize vector: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// hash_seq 键格式：hash_seq（与qmd保持一致）
	hashSeq := fmt.Sprintf("%s_%d", hash, seq)

	// 开启事务，同时插入到两个表
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 插入到 content_vectors（元数据表）
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO content_vectors (hash, seq, pos, embedding, model, embedded_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, hash, seq, pos, blob, model, now)
	if err != nil {
		return fmt.Errorf("failed to store embedding metadata: %w", err)
	}

	// 插入到 vectors_vec（sqlite-vec虚拟表）
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO vectors_vec (hash_seq, embedding)
		VALUES (?, ?)
	`, hashSeq, blob)
	if err != nil {
		return fmt.Errorf("failed to store vector in vec table: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetEmbedding 获取嵌入向量
func (s *Store) GetEmbedding(hash string, seq int) ([]float32, error) {
	var blob []byte

	err := s.db.QueryRow(`
		SELECT embedding FROM content_vectors
		WHERE hash = ? AND seq = ?
	`, hash, seq).Scan(&blob)

	if err != nil {
		return nil, fmt.Errorf("failed to get embedding: %w", err)
	}

	return blobToFloat32(blob), nil
}

// GetAllEmbeddings 获取文档的所有嵌入向量
func (s *Store) GetAllEmbeddings(hash string) ([][]float32, error) {
	rows, err := s.db.Query(`
		SELECT seq, embedding FROM content_vectors
		WHERE hash = ?
		ORDER BY seq
	`, hash)

	if err != nil {
		return nil, fmt.Errorf("failed to query embeddings: %w", err)
	}
	defer rows.Close()

	var embeddings [][]float32
	for rows.Next() {
		var seq int
		var blob []byte

		err := rows.Scan(&seq, &blob)
		if err != nil {
			return nil, fmt.Errorf("failed to scan embedding: %w", err)
		}

		embeddings = append(embeddings, blobToFloat32(blob))
	}

	return embeddings, nil
}

// DeleteEmbeddings 删除文档的所有嵌入
func (s *Store) DeleteEmbeddings(hash string) error {
	// 开启事务，同时从两个表删除
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 从 content_vectors 删除
	_, err = tx.Exec("DELETE FROM content_vectors WHERE hash = ?", hash)
	if err != nil {
		return fmt.Errorf("failed to delete from content_vectors: %w", err)
	}

	// 从 vectors_vec 删除（使用 hash_seq 模式匹配）
	// 注意：SQLite LIKE 性能可能不如直接查询，但这是清理的最简单方式
	_, err = tx.Exec("DELETE FROM vectors_vec WHERE hash_seq LIKE ?", hash+"_%")
	if err != nil {
		return fmt.Errorf("failed to delete from vectors_vec: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// CountEmbeddedDocuments 统计已嵌入的文档数
func (s *Store) CountEmbeddedDocuments() (int, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(DISTINCT hash) FROM content_vectors
	`).Scan(&count)

	if err != nil {
		return 0, fmt.Errorf("failed to count embedded documents: %w", err)
	}

	return count, nil
}
