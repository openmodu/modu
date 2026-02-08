package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// CacheKey 生成缓存键
func CacheKey(operation string, params interface{}) string {
	data, err := json.Marshal(params)
	if err != nil {
		// 如果序列化失败，使用字符串表示
		data = []byte(fmt.Sprintf("%v", params))
	}

	hash := sha256.Sum256(append([]byte(operation+":"), data...))
	return fmt.Sprintf("%x", hash)
}

// GetCachedResult 获取缓存结果
func (s *Store) GetCachedResult(key string) (string, error) {
	var result string
	err := s.db.QueryRow(`
		SELECT result FROM llm_cache
		WHERE hash = ?
	`, key).Scan(&result)

	if err == sql.ErrNoRows {
		return "", nil // 缓存未命中
	}

	if err != nil {
		return "", fmt.Errorf("failed to get cached result: %w", err)
	}

	return result, nil
}

// SetCachedResult 设置缓存结果
func (s *Store) SetCachedResult(key string, result string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO llm_cache (hash, result, created_at)
		VALUES (?, ?, ?)
	`, key, result, now)

	if err != nil {
		return fmt.Errorf("failed to set cached result: %w", err)
	}

	return nil
}

// ClearCache 清空所有缓存
func (s *Store) ClearCache() error {
	_, err := s.db.Exec("DELETE FROM llm_cache")
	if err != nil {
		return fmt.Errorf("failed to clear cache: %w", err)
	}
	return nil
}

// GetCacheStats 获取缓存统计
func (s *Store) GetCacheStats() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM llm_cache`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get cache stats: %w", err)
	}
	return count, nil
}
