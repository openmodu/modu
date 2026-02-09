package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ContextEntry 上下文条目
type ContextEntry struct {
	Path      string    // 路径（可以是collection或具体路径）
	Content   string    // 上下文内容
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AddContext 添加上下文
func (s *Store) AddContext(path, content string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// 检查是否已存在
	var exists int
	err := s.db.QueryRow("SELECT COUNT(*) FROM contexts WHERE path = ?", path).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check context: %w", err)
	}

	if exists > 0 {
		// 更新现有上下文
		_, err = s.db.Exec(`
			UPDATE contexts
			SET content = ?, updated_at = ?
			WHERE path = ?
		`, content, now, path)
		if err != nil {
			return fmt.Errorf("failed to update context: %w", err)
		}
	} else {
		// 插入新上下文
		_, err = s.db.Exec(`
			INSERT INTO contexts (path, content, created_at, updated_at)
			VALUES (?, ?, ?, ?)
		`, path, content, now, now)
		if err != nil {
			return fmt.Errorf("failed to insert context: %w", err)
		}
	}

	return nil
}

// ListContexts 列出所有上下文
func (s *Store) ListContexts() ([]ContextEntry, error) {
	rows, err := s.db.Query(`
		SELECT path, content, created_at, updated_at
		FROM contexts
		ORDER BY path
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query contexts: %w", err)
	}
	defer rows.Close()

	var contexts []ContextEntry
	for rows.Next() {
		var ctx ContextEntry
		var createdAtStr, updatedAtStr string

		err := rows.Scan(&ctx.Path, &ctx.Content, &createdAtStr, &updatedAtStr)
		if err != nil {
			continue
		}

		ctx.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		ctx.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

		contexts = append(contexts, ctx)
	}

	return contexts, nil
}

// GetContext 获取指定路径的上下文
func (s *Store) GetContext(path string) (*ContextEntry, error) {
	var ctx ContextEntry
	var createdAtStr, updatedAtStr string

	err := s.db.QueryRow(`
		SELECT path, content, created_at, updated_at
		FROM contexts
		WHERE path = ?
	`, path).Scan(&ctx.Path, &ctx.Content, &createdAtStr, &updatedAtStr)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("context not found for path: %s", path)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get context: %w", err)
	}

	ctx.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	ctx.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

	return &ctx, nil
}

// RemoveContext 删除上下文
func (s *Store) RemoveContext(path string) error {
	result, err := s.db.Exec("DELETE FROM contexts WHERE path = ?", path)
	if err != nil {
		return fmt.Errorf("failed to delete context: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("context not found for path: %s", path)
	}

	return nil
}

// GetContextsForPath 获取路径的所有相关上下文
// 支持层级匹配：/ -> mmq://collection -> mmq://collection/path
func (s *Store) GetContextsForPath(targetPath string) ([]ContextEntry, error) {
	rows, err := s.db.Query(`
		SELECT path, content, created_at, updated_at
		FROM contexts
		ORDER BY LENGTH(path) ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query contexts: %w", err)
	}
	defer rows.Close()

	var contexts []ContextEntry

	for rows.Next() {
		var ctx ContextEntry
		var createdAtStr, updatedAtStr string

		err := rows.Scan(&ctx.Path, &ctx.Content, &createdAtStr, &updatedAtStr)
		if err != nil {
			continue
		}

		ctx.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		ctx.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

		// 检查是否匹配
		if isPathMatch(ctx.Path, targetPath) {
			contexts = append(contexts, ctx)
		}
	}

	return contexts, nil
}

// CheckMissingContexts 检查缺失上下文的集合和路径
func (s *Store) CheckMissingContexts() ([]string, error) {
	// 获取所有集合
	collections, err := s.GetCollectionNames()
	if err != nil {
		return nil, err
	}

	var missing []string

	// 检查全局上下文（/）
	globalCtx, _ := s.GetContext("/")
	if globalCtx == nil {
		missing = append(missing, "/ (global context)")
	}

	// 检查每个集合
	for _, coll := range collections {
		collPath := fmt.Sprintf("mmq://%s", coll)
		ctx, _ := s.GetContext(collPath)
		if ctx == nil {
			missing = append(missing, collPath)
		}
	}

	return missing, nil
}

// ContextExists 检查上下文是否存在
func (s *Store) ContextExists(path string) (bool, error) {
	var exists int
	err := s.db.QueryRow("SELECT COUNT(*) FROM contexts WHERE path = ?", path).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check context: %w", err)
	}

	return exists > 0, nil
}

// isPathMatch 检查上下文路径是否匹配目标路径
// 支持：
// - "/" 匹配所有路径（全局）
// - "mmq://collection" 匹配该集合下所有文档
// - "mmq://collection/path" 匹配特定路径
func isPathMatch(contextPath, targetPath string) bool {
	// 全局上下文
	if contextPath == "/" {
		return true
	}

	// 精确匹配
	if contextPath == targetPath {
		return true
	}

	// 前缀匹配（目录级别）
	if strings.HasPrefix(targetPath, contextPath+"/") {
		return true
	}

	// collection级别匹配
	// contextPath: "mmq://collection"
	// targetPath: "mmq://collection/path"
	if strings.HasPrefix(contextPath, "mmq://") && strings.HasPrefix(targetPath, contextPath+"/") {
		return true
	}

	return false
}

// GetAllContextsForDocument 获取文档的所有相关上下文（按优先级排序）
func (s *Store) GetAllContextsForDocument(collection, path string) ([]ContextEntry, error) {
	targetPath := fmt.Sprintf("mmq://%s/%s", collection, path)

	// 可能的上下文路径（按优先级从高到低）
	paths := []string{
		targetPath,                      // 精确路径
		fmt.Sprintf("mmq://%s", collection), // 集合级别
		"/",                             // 全局
	}

	var contexts []ContextEntry

	for _, p := range paths {
		ctx, err := s.GetContext(p)
		if err == nil {
			contexts = append(contexts, *ctx)
		}
	}

	return contexts, nil
}
