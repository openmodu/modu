package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/google/uuid"
)

// MemoryResult 记忆查询结果
type MemoryResult struct {
	ID         string
	Type       string
	Content    string
	Metadata   map[string]interface{}
	Tags       []string
	Timestamp  time.Time
	ExpiresAt  *time.Time
	Importance float64
	Relevance  float64 // 向量搜索时的相关度
}

// InsertMemory 插入记忆
func (s *Store) InsertMemory(
	memType, content string,
	metadata map[string]interface{},
	tags []string,
	timestamp time.Time,
	expiresAt *time.Time,
	importance float64,
	embedding []float32,
) error {
	// 生成UUID
	id := uuid.New().String()

	// 序列化metadata
	metadataJSON := "{}"
	if metadata != nil {
		data, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
		metadataJSON = string(data)
	}

	// 序列化tags
	tagsJSON := "[]"
	if tags != nil {
		data, err := json.Marshal(tags)
		if err != nil {
			return fmt.Errorf("failed to marshal tags: %w", err)
		}
		tagsJSON = string(data)
	}

	// 序列化embedding为BLOB
	embeddingBlob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return fmt.Errorf("failed to serialize embedding: %w", err)
	}

	// 处理expires_at
	var expiresAtStr *string
	if expiresAt != nil {
		str := expiresAt.Format(time.RFC3339)
		expiresAtStr = &str
	}

	// 插入数据库
	_, err = s.db.Exec(`
		INSERT INTO memories (id, type, content, metadata, tags, timestamp, expires_at, importance, embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, memType, content, metadataJSON, tagsJSON, timestamp.Format(time.RFC3339), expiresAtStr, importance, embeddingBlob)

	return err
}

// SearchMemories 向量搜索记忆
func (s *Store) SearchMemories(queryEmbedding []float32, limit int, memoryTypes []string) ([]MemoryResult, error) {
	// 构建类型过滤
	whereClause := ""
	args := make([]interface{}, 0)

	if len(memoryTypes) > 0 {
		placeholders := ""
		for i, mt := range memoryTypes {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
			args = append(args, mt)
		}
		whereClause = fmt.Sprintf("WHERE type IN (%s)", placeholders)
	}

	// 查询所有记忆
	query := fmt.Sprintf(`
		SELECT id, type, content, metadata, tags, timestamp, expires_at, importance, embedding
		FROM memories
		%s
	`, whereClause)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 计算相似度
	type candidate struct {
		result   MemoryResult
		distance float64
	}

	var candidates []candidate

	for rows.Next() {
		var id, memType, content, metadataJSON, tagsJSON, timestampStr string
		var expiresAtStr sql.NullString
		var importance float64
		var embeddingBlob []byte

		err := rows.Scan(&id, &memType, &content, &metadataJSON, &tagsJSON,
			&timestampStr, &expiresAtStr, &importance, &embeddingBlob)
		if err != nil {
			continue
		}

		// 解析metadata
		var metadata map[string]interface{}
		json.Unmarshal([]byte(metadataJSON), &metadata)

		// 解析tags
		var tags []string
		json.Unmarshal([]byte(tagsJSON), &tags)

		// 解析timestamp
		timestamp, _ := time.Parse(time.RFC3339, timestampStr)

		// 解析expires_at
		var expiresAt *time.Time
		if expiresAtStr.Valid {
			t, _ := time.Parse(time.RFC3339, expiresAtStr.String)
			expiresAt = &t
		}

		// 解析embedding
		embedding := blobToFloat32(embeddingBlob)

		// 计算余弦距离
		distance := cosineDist(queryEmbedding, embedding)

		result := MemoryResult{
			ID:         id,
			Type:       memType,
			Content:    content,
			Metadata:   metadata,
			Tags:       tags,
			Timestamp:  timestamp,
			ExpiresAt:  expiresAt,
			Importance: importance,
			Relevance:  1.0 - distance, // 余弦相似度
		}

		candidates = append(candidates, candidate{
			result:   result,
			distance: distance,
		})
	}

	// 按距离排序
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].distance < candidates[i].distance {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	// 返回TopK
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	results := make([]MemoryResult, len(candidates))
	for i, c := range candidates {
		results[i] = c.result
	}

	return results, nil
}

// GetMemoryByID 根据ID获取记忆
func (s *Store) GetMemoryByID(id string) (*MemoryResult, error) {
	var memType, content, metadataJSON, tagsJSON, timestampStr string
	var expiresAtStr sql.NullString
	var importance float64

	err := s.db.QueryRow(`
		SELECT id, type, content, metadata, tags, timestamp, expires_at, importance
		FROM memories
		WHERE id = ?
	`, id).Scan(&id, &memType, &content, &metadataJSON, &tagsJSON,
		&timestampStr, &expiresAtStr, &importance)

	if err != nil {
		return nil, err
	}

	// 解析metadata
	var metadata map[string]interface{}
	json.Unmarshal([]byte(metadataJSON), &metadata)

	// 解析tags
	var tags []string
	json.Unmarshal([]byte(tagsJSON), &tags)

	// 解析timestamp
	timestamp, _ := time.Parse(time.RFC3339, timestampStr)

	// 解析expires_at
	var expiresAt *time.Time
	if expiresAtStr.Valid {
		t, _ := time.Parse(time.RFC3339, expiresAtStr.String)
		expiresAt = &t
	}

	return &MemoryResult{
		ID:         id,
		Type:       memType,
		Content:    content,
		Metadata:   metadata,
		Tags:       tags,
		Timestamp:  timestamp,
		ExpiresAt:  expiresAt,
		Importance: importance,
	}, nil
}

// GetMemoriesByType 获取指定类型的所有记忆
func (s *Store) GetMemoriesByType(memType string) ([]MemoryResult, error) {
	rows, err := s.db.Query(`
		SELECT id, type, content, metadata, tags, timestamp, expires_at, importance
		FROM memories
		WHERE type = ?
		ORDER BY timestamp DESC
	`, memType)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanMemoryResults(rows)
}

// GetMemoriesBySession 获取指定会话的记忆
func (s *Store) GetMemoriesBySession(sessionID string, limit int) ([]MemoryResult, error) {
	rows, err := s.db.Query(`
		SELECT id, type, content, metadata, tags, timestamp, expires_at, importance
		FROM memories
		WHERE type = 'conversation' AND json_extract(metadata, '$.session_id') = ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, sessionID, limit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanMemoryResults(rows)
}

// GetRecentMemoriesByType 获取最近的指定类型记忆
func (s *Store) GetRecentMemoriesByType(memType string, limit int) ([]MemoryResult, error) {
	rows, err := s.db.Query(`
		SELECT id, type, content, metadata, tags, timestamp, expires_at, importance
		FROM memories
		WHERE type = ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, memType, limit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanMemoryResults(rows)
}

// UpdateMemory 更新记忆
func (s *Store) UpdateMemory(
	id, content string,
	metadata map[string]interface{},
	tags []string,
	expiresAt *time.Time,
	importance float64,
	embedding []float32,
) error {
	// 序列化metadata
	metadataJSON := "{}"
	if metadata != nil {
		data, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
		metadataJSON = string(data)
	}

	// 序列化tags
	tagsJSON := "[]"
	if tags != nil {
		data, err := json.Marshal(tags)
		if err != nil {
			return fmt.Errorf("failed to marshal tags: %w", err)
		}
		tagsJSON = string(data)
	}

	// 序列化embedding
	embeddingBlob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return fmt.Errorf("failed to serialize embedding: %w", err)
	}

	// 处理expires_at
	var expiresAtStr *string
	if expiresAt != nil {
		str := expiresAt.Format(time.RFC3339)
		expiresAtStr = &str
	}

	_, err = s.db.Exec(`
		UPDATE memories
		SET content = ?, metadata = ?, tags = ?, expires_at = ?, importance = ?, embedding = ?
		WHERE id = ?
	`, content, metadataJSON, tagsJSON, expiresAtStr, importance, embeddingBlob, id)

	return err
}

// DeleteMemory 删除记忆
func (s *Store) DeleteMemory(id string) error {
	_, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id)
	return err
}

// DeleteMemoriesBySession 删除指定会话的记忆
func (s *Store) DeleteMemoriesBySession(sessionID string) (int, error) {
	result, err := s.db.Exec(`
		DELETE FROM memories
		WHERE type = 'conversation' AND json_extract(metadata, '$.session_id') = ?
	`, sessionID)

	if err != nil {
		return 0, err
	}

	count, _ := result.RowsAffected()
	return int(count), nil
}

// DeleteExpiredMemories 删除过期记忆
func (s *Store) DeleteExpiredMemories() (int, error) {
	now := time.Now().Format(time.RFC3339)

	result, err := s.db.Exec(`
		DELETE FROM memories
		WHERE expires_at IS NOT NULL AND expires_at < ?
	`, now)

	if err != nil {
		return 0, err
	}

	count, _ := result.RowsAffected()
	return int(count), nil
}

// CountMemories 统计记忆总数
func (s *Store) CountMemories() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM memories").Scan(&count)
	return count, err
}

// CountMemoriesByType 统计指定类型的记忆数量
func (s *Store) CountMemoriesByType(memType string) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM memories WHERE type = ?", memType).Scan(&count)
	return count, err
}

// CountMemoriesBySession 统计指定会话的记忆数量
func (s *Store) CountMemoriesBySession(sessionID string) (int, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM memories
		WHERE type = 'conversation' AND json_extract(metadata, '$.session_id') = ?
	`, sessionID).Scan(&count)
	return count, err
}

// GetSessionIDs 获取所有会话ID
func (s *Store) GetSessionIDs() ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT json_extract(metadata, '$.session_id')
		FROM memories
		WHERE type = 'conversation' AND json_extract(metadata, '$.session_id') IS NOT NULL
	`)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessionIDs []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			continue
		}
		sessionIDs = append(sessionIDs, sessionID)
	}

	return sessionIDs, nil
}

// scanMemoryResults 扫描记忆查询结果
func (s *Store) scanMemoryResults(rows *sql.Rows) ([]MemoryResult, error) {
	var results []MemoryResult

	for rows.Next() {
		var id, memType, content, metadataJSON, tagsJSON, timestampStr string
		var expiresAtStr sql.NullString
		var importance float64

		err := rows.Scan(&id, &memType, &content, &metadataJSON, &tagsJSON,
			&timestampStr, &expiresAtStr, &importance)
		if err != nil {
			continue
		}

		// 解析metadata
		var metadata map[string]interface{}
		json.Unmarshal([]byte(metadataJSON), &metadata)

		// 解析tags
		var tags []string
		json.Unmarshal([]byte(tagsJSON), &tags)

		// 解析timestamp
		timestamp, _ := time.Parse(time.RFC3339, timestampStr)

		// 解析expires_at
		var expiresAt *time.Time
		if expiresAtStr.Valid {
			t, _ := time.Parse(time.RFC3339, expiresAtStr.String)
			expiresAt = &t
		}

		results = append(results, MemoryResult{
			ID:         id,
			Type:       memType,
			Content:    content,
			Metadata:   metadata,
			Tags:       tags,
			Timestamp:  timestamp,
			ExpiresAt:  expiresAt,
			Importance: importance,
		})
	}

	return results, nil
}

// cosineDist 计算余弦距离（内部函数，与search_vector.go中的实现相同）
func cosineDist(a, b []float32) float64 {
	if len(a) != len(b) {
		return 1.0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 1.0
	}

	similarity := dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
	return 1.0 - similarity
}
