package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// DocumentListEntry 文档列表条目
type DocumentListEntry struct {
	ID         int       `json:"id"`
	DocID      string    `json:"docid"`      // 短docid（前6位哈希）
	Collection string    `json:"collection"`
	Path       string    `json:"path"`
	Title      string    `json:"title"`
	Hash       string    `json:"hash"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
}

// DocumentDetail 文档详情
type DocumentDetail struct {
	ID         int       `json:"id"`
	DocID      string    `json:"docid"`
	Collection string    `json:"collection"`
	Path       string    `json:"path"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	Hash       string    `json:"hash"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
}

// ListDocumentsByPath 列出集合或路径下的文档
// - collection 为空：列出所有集合
// - collection 不为空，path 为空：列出集合下所有文档
// - collection 和 path 都不为空：列出路径下的文档（前缀匹配）
func (s *Store) ListDocumentsByPath(collection, path string) ([]DocumentListEntry, error) {
	var rows *sql.Rows
	var err error

	if collection == "" {
		// 列出所有文档（按集合分组）
		rows, err = s.db.Query(`
			SELECT
				id,
				collection,
				path,
				title,
				hash,
				created_at,
				modified_at
			FROM documents
			WHERE active = 1
			ORDER BY collection, path
		`)
	} else if path == "" {
		// 列出集合下所有文档
		rows, err = s.db.Query(`
			SELECT
				id,
				collection,
				path,
				title,
				hash,
				created_at,
				modified_at
			FROM documents
			WHERE active = 1 AND collection = ?
			ORDER BY path
		`, collection)
	} else {
		// 列出路径下的文档（前缀匹配）
		pathPrefix := strings.TrimSuffix(path, "/")
		rows, err = s.db.Query(`
			SELECT
				id,
				collection,
				path,
				title,
				hash,
				created_at,
				modified_at
			FROM documents
			WHERE active = 1
				AND collection = ?
				AND (path = ? OR path LIKE ?)
			ORDER BY path
		`, collection, pathPrefix, pathPrefix+"/%")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to list documents: %w", err)
	}
	defer rows.Close()

	var docs []DocumentListEntry
	for rows.Next() {
		var doc DocumentListEntry
		var createdStr, modifiedStr string

		err := rows.Scan(
			&doc.ID,
			&doc.Collection,
			&doc.Path,
			&doc.Title,
			&doc.Hash,
			&createdStr,
			&modifiedStr,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan document: %w", err)
		}

		// 解析时间
		doc.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		doc.ModifiedAt, _ = time.Parse(time.RFC3339, modifiedStr)

		// 生成短docid（前6位哈希）
		doc.DocID = "#" + doc.Hash[:6]

		docs = append(docs, doc)
	}

	return docs, nil
}

// GetDocumentByPath 通过路径获取文档
// 路径格式：collection/path 或 mmq://collection/path
func (s *Store) GetDocumentByPath(filePath string) (*DocumentDetail, error) {
	// 解析路径
	collection, path := parseFilePath(filePath)
	if collection == "" {
		return nil, fmt.Errorf("invalid file path: %s", filePath)
	}

	var doc DocumentDetail
	var createdStr, modifiedStr string
	var content string

	err := s.db.QueryRow(`
		SELECT
			d.id,
			d.collection,
			d.path,
			d.title,
			d.hash,
			c.doc,
			d.created_at,
			d.modified_at
		FROM documents d
		JOIN content c ON c.hash = d.hash
		WHERE d.active = 1
			AND d.collection = ?
			AND d.path = ?
	`, collection, path).Scan(
		&doc.ID,
		&doc.Collection,
		&doc.Path,
		&doc.Title,
		&doc.Hash,
		&content,
		&createdStr,
		&modifiedStr,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("document not found: %s", filePath)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get document: %w", err)
	}

	doc.Content = content
	doc.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	doc.ModifiedAt, _ = time.Parse(time.RFC3339, modifiedStr)
	doc.DocID = "#" + doc.Hash[:6]

	return &doc, nil
}

// GetDocumentByID 通过短docid获取文档
// docid 格式：#abc123 或 abc123
func (s *Store) GetDocumentByID(docID string) (*DocumentDetail, error) {
	// 移除前缀 #
	docID = strings.TrimPrefix(docID, "#")

	if len(docID) < 6 {
		return nil, fmt.Errorf("invalid docid: must be at least 6 characters")
	}

	var doc DocumentDetail
	var createdStr, modifiedStr string
	var content string

	// 使用 LIKE 匹配前缀
	err := s.db.QueryRow(`
		SELECT
			d.id,
			d.collection,
			d.path,
			d.title,
			d.hash,
			c.doc,
			d.created_at,
			d.modified_at
		FROM documents d
		JOIN content c ON c.hash = d.hash
		WHERE d.active = 1
			AND d.hash LIKE ?
		LIMIT 1
	`, docID+"%").Scan(
		&doc.ID,
		&doc.Collection,
		&doc.Path,
		&doc.Title,
		&doc.Hash,
		&content,
		&createdStr,
		&modifiedStr,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("document not found: #%s", docID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get document by id: %w", err)
	}

	doc.Content = content
	doc.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	doc.ModifiedAt, _ = time.Parse(time.RFC3339, modifiedStr)
	doc.DocID = "#" + doc.Hash[:6]

	return &doc, nil
}

// GetMultipleDocuments 批量获取文档
// 支持：
// - 逗号分隔的docid列表：#abc123, #def456
// - 逗号分隔的路径列表：docs/a.md, docs/b.md
// - Glob模式：docs/**/*.md
func (s *Store) GetMultipleDocuments(pattern string, maxBytes int) ([]*DocumentDetail, error) {
	// 检测模式类型
	if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
		// Glob模式
		return s.getDocumentsByGlob(pattern, maxBytes)
	} else if strings.Contains(pattern, ",") {
		// 逗号分隔列表
		return s.getDocumentsByList(pattern, maxBytes)
	} else {
		// 单个文件
		doc, err := s.getDocumentSingle(pattern, maxBytes)
		if err != nil {
			return nil, err
		}
		return []*DocumentDetail{doc}, nil
	}
}

// getDocumentsByGlob 通过Glob模式获取文档
func (s *Store) getDocumentsByGlob(pattern string, maxBytes int) ([]*DocumentDetail, error) {
	// 解析模式（collection/pattern）
	collection, pathPattern := parseFilePath(pattern)

	var rows *sql.Rows
	var err error

	if collection == "" {
		// 全局匹配
		rows, err = s.db.Query(`
			SELECT
				d.id,
				d.collection,
				d.path,
				d.title,
				d.hash,
				c.doc,
				d.created_at,
				d.modified_at,
				length(c.doc) as size
			FROM documents d
			JOIN content c ON c.hash = d.hash
			WHERE d.active = 1
			ORDER BY d.collection, d.path
		`)
	} else {
		// 集合内匹配
		rows, err = s.db.Query(`
			SELECT
				d.id,
				d.collection,
				d.path,
				d.title,
				d.hash,
				c.doc,
				d.created_at,
				d.modified_at,
				length(c.doc) as size
			FROM documents d
			JOIN content c ON c.hash = d.hash
			WHERE d.active = 1 AND d.collection = ?
			ORDER BY d.path
		`, collection)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}
	defer rows.Close()

	var docs []*DocumentDetail
	for rows.Next() {
		var doc DocumentDetail
		var createdStr, modifiedStr string
		var size int

		err := rows.Scan(
			&doc.ID,
			&doc.Collection,
			&doc.Path,
			&doc.Title,
			&doc.Hash,
			&doc.Content,
			&createdStr,
			&modifiedStr,
			&size,
		)
		if err != nil {
			continue
		}

		// 跳过过大的文件
		if maxBytes > 0 && size > maxBytes {
			continue
		}

		// Glob匹配
		matched, _ := filepath.Match(pathPattern, doc.Path)
		if !matched && pathPattern != "" {
			// 尝试双星匹配（递归目录）
			if !strings.Contains(pathPattern, "**") {
				continue
			}
			// 简化的**匹配：移除**并检查后缀
			simplifiedPattern := strings.ReplaceAll(pathPattern, "**/", "")
			if !strings.HasSuffix(doc.Path, simplifiedPattern) {
				continue
			}
		}

		doc.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		doc.ModifiedAt, _ = time.Parse(time.RFC3339, modifiedStr)
		doc.DocID = "#" + doc.Hash[:6]

		docs = append(docs, &doc)
	}

	return docs, nil
}

// getDocumentsByList 通过逗号分隔列表获取文档
func (s *Store) getDocumentsByList(list string, maxBytes int) ([]*DocumentDetail, error) {
	items := strings.Split(list, ",")
	var docs []*DocumentDetail

	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		doc, err := s.getDocumentSingle(item, maxBytes)
		if err != nil {
			// 跳过错误，继续处理其他文档
			continue
		}

		// doc 为 nil 表示被 maxBytes 过滤掉了
		if doc != nil {
			docs = append(docs, doc)
		}
	}

	return docs, nil
}

// getDocumentSingle 获取单个文档（按路径或docid）
func (s *Store) getDocumentSingle(identifier string, maxBytes int) (*DocumentDetail, error) {
	// 判断是docid还是路径
	var doc *DocumentDetail
	var err error

	if strings.HasPrefix(identifier, "#") || !strings.Contains(identifier, "/") {
		// 按docid获取
		doc, err = s.GetDocumentByID(identifier)
	} else {
		// 按路径获取
		doc, err = s.GetDocumentByPath(identifier)
	}

	if err != nil {
		return nil, err
	}

	// 检查大小（如果超过限制则跳过，不返回错误）
	if maxBytes > 0 && len(doc.Content) > maxBytes {
		return nil, nil // 返回 nil 表示跳过
	}

	return doc, nil
}

// parseFilePath 解析文件路径
// 支持格式：
// - mmq://collection/path -> (collection, path)
// - qmd://collection/path -> (collection, path) (兼容旧格式)
// - collection/path -> (collection, path)
// - path -> ("", path)
func parseFilePath(filePath string) (collection, path string) {
	// 移除 mmq:// 或 qmd:// 前缀
	filePath = strings.TrimPrefix(filePath, "mmq://")
	filePath = strings.TrimPrefix(filePath, "qmd://")

	// 分割路径
	parts := strings.SplitN(filePath, "/", 2)

	if len(parts) == 1 {
		return "", parts[0]
	}

	return parts[0], parts[1]
}
