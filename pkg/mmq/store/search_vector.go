package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

// SearchVectorDocuments 文档级向量搜索（对标QMD的vsearch）
// 使用 sqlite-vec 的高效 MATCH 查询，采用两步查询避免 JOIN 性能问题
func (s *Store) SearchVectorDocuments(query string, queryEmbed []float32, limit int, collection string) ([]SearchResult, error) {
	// 检查 vectors_vec 表是否存在
	var tableName string
	err := s.db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='vectors_vec'
	`).Scan(&tableName)

	if err == sql.ErrNoRows {
		// 如果表不存在，返回空结果（还没有索引任何向量）
		return []SearchResult{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to check vectors_vec table: %w", err)
	}

	// 序列化查询向量
	vecBlob, err := sqlite_vec.SerializeFloat32(queryEmbed)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize query vector: %w", err)
	}

	// STEP 1: 使用 sqlite-vec MATCH 查询获取最相似的向量
	// 获取 limit * 3 个结果用于后续过滤（参考 qmd 实现）
	type vecResult struct {
		hashSeq  string
		distance float64
	}

	vecQuery := `
		SELECT hash_seq, distance
		FROM vectors_vec
		WHERE embedding MATCH ? AND k = ?
	`

	vecRows, err := s.db.Query(vecQuery, vecBlob, limit*3)
	if err != nil {
		return nil, fmt.Errorf("failed to query vectors: %w", err)
	}
	defer vecRows.Close()

	var vecResults []vecResult
	distanceMap := make(map[string]float64)

	for vecRows.Next() {
		var vr vecResult
		if err := vecRows.Scan(&vr.hashSeq, &vr.distance); err != nil {
			continue
		}
		vecResults = append(vecResults, vr)
		distanceMap[vr.hashSeq] = vr.distance
	}

	if len(vecResults) == 0 {
		return []SearchResult{}, nil
	}

	// STEP 2: 获取文档信息
	// 构建 hash_seq IN (...) 查询
	hashSeqs := make([]interface{}, len(vecResults))
	placeholders := ""
	for i, vr := range vecResults {
		hashSeqs[i] = vr.hashSeq
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
	}

	docQuery := `
		SELECT
			cv.hash || '_' || cv.seq as hash_seq,
			cv.hash,
			cv.pos,
			d.collection || '/' || d.path as display_path,
			d.title,
			d.collection,
			d.path,
			d.id,
			d.modified_at,
			content.doc as body
		FROM content_vectors cv
		JOIN documents d ON d.hash = cv.hash AND d.active = 1
		JOIN content ON content.hash = d.hash
		WHERE cv.hash || '_' || cv.seq IN (` + placeholders + `)`

	args := hashSeqs
	if collection != "" {
		docQuery += ` AND d.collection = ?`
		args = append(args, collection)
	}

	docRows, err := s.db.Query(docQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}
	defer docRows.Close()

	// 收集结果并按文档去重（保留最佳距离）
	type docResult struct {
		hashSeq     string
		hash        string
		pos         int
		displayPath string
		title       string
		collection  string
		path        string
		id          int
		modifiedAt  string
		body        string
		distance    float64
	}

	seen := make(map[string]*docResult)
	for docRows.Next() {
		var dr docResult
		err := docRows.Scan(
			&dr.hashSeq,
			&dr.hash,
			&dr.pos,
			&dr.displayPath,
			&dr.title,
			&dr.collection,
			&dr.path,
			&dr.id,
			&dr.modifiedAt,
			&dr.body,
		)
		if err != nil {
			continue
		}

		dr.distance = distanceMap[dr.hashSeq]

		// 按文件路径去重，保留最佳距离
		filepath := dr.collection + "/" + dr.path
		existing, exists := seen[filepath]
		if !exists || dr.distance < existing.distance {
			seen[filepath] = &dr
		}
	}

	// 转换为切片并排序
	results := make([]docResult, 0, len(seen))
	for _, dr := range seen {
		results = append(results, *dr)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].distance < results[j].distance
	})

	// 取 TopK
	if len(results) > limit {
		results = results[:limit]
	}

	// 转换为 SearchResult
	searchResults := make([]SearchResult, len(results))
	for i, dr := range results {
		modifiedAt, _ := time.Parse(time.RFC3339, dr.modifiedAt)
		searchResults[i] = SearchResult{
			ID:         strconv.Itoa(dr.id),
			Title:      dr.title,
			Content:    dr.body,
			Snippet:    extractSnippet(dr.body, query, 200),
			Score:      1.0 - dr.distance, // 转换为相似度分数
			Source:     "vector",
			Collection: dr.collection,
			Path:       dr.path,
			Timestamp:  modifiedAt,
		}
	}

	return searchResults, nil
}
