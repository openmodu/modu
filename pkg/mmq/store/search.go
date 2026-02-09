package store

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/crosszan/modu/pkg/mmq/internal/vectordb"
)

// SearchFTS 使用BM25全文搜索
func (s *Store) SearchFTS(query string, limit int, collectionFilter string) ([]SearchResult, error) {
	// 构建FTS查询
	ftsQuery := buildFTS5Query(query)
	if ftsQuery == "" {
		return nil, nil
	}

	// 构建SQL查询
	sql := `
		SELECT
			d.id,
			d.collection || '/' || d.path as filepath,
			d.title,
			d.hash,
			d.collection,
			d.path,
			c.doc as body,
			d.modified_at,
			bm25(documents_fts, 10.0, 1.0, 1.0) as bm25_score
		FROM documents_fts f
		JOIN documents d ON d.id = f.rowid
		JOIN content c ON c.hash = d.hash
		WHERE documents_fts MATCH ? AND d.active = 1
	`

	args := []interface{}{ftsQuery}

	if collectionFilter != "" {
		sql += " AND d.collection = ?"
		args = append(args, collectionFilter)
	}

	sql += " ORDER BY bm25_score ASC LIMIT ?"
	args = append(args, limit)

	// 执行查询
	rows, err := s.db.Query(sql, args...)
	if err != nil {
		return nil, fmt.Errorf("FTS query failed: %w", err)
	}
	defer rows.Close()

	// 处理结果
	var results []SearchResult
	for rows.Next() {
		var result SearchResult
		var bm25Score float64
		var modifiedAt string

		err := rows.Scan(
			&result.ID, &result.Path, &result.Title, &result.ID,
			&result.Collection, &result.Path, &result.Content,
			&modifiedAt, &bm25Score,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan result: %w", err)
		}

		// 转换BM25分数为[0,1]范围
		// BM25分数是负数，绝对值越大表示越相关
		result.Score = normalizeBM25Score(bm25Score)
		result.Source = "fts"
		result.Timestamp, _ = time.Parse(time.RFC3339, modifiedAt)

		// 生成snippet
		result.Snippet = extractSnippet(result.Content, query, 300)

		results = append(results, result)
	}

	return results, nil
}

// SearchVector 使用向量相似搜索
// 注意：这个实现会加载所有向量到内存，适合中小规模数据集（<10000文档）
func (s *Store) SearchVector(query string, embedding []float32, limit int, collectionFilter string) ([]SearchResult, error) {
	// 获取所有向量
	sql := `
		SELECT cv.hash, cv.seq, cv.embedding, d.collection, d.path, d.title, c.doc, d.modified_at
		FROM content_vectors cv
		JOIN documents d ON d.hash = cv.hash
		JOIN content c ON c.hash = cv.hash
		WHERE d.active = 1
	`

	args := []interface{}{}
	if collectionFilter != "" {
		sql += " AND d.collection = ?"
		args = append(args, collectionFilter)
	}

	rows, err := s.db.Query(sql, args...)
	if err != nil {
		return nil, fmt.Errorf("vector query failed: %w", err)
	}
	defer rows.Close()

	// 计算所有向量的距离
	type candidate struct {
		hash       string
		seq        int
		distance   float64
		collection string
		path       string
		title      string
		body       string
		modifiedAt string
	}

	var candidates []candidate

	for rows.Next() {
		var c candidate
		var embeddingBlob []byte

		err := rows.Scan(&c.hash, &c.seq, &embeddingBlob, &c.collection, &c.path, &c.title, &c.body, &c.modifiedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan vector: %w", err)
		}

		// 将BLOB转换为float32切片
		vecEmbedding := blobToFloat32(embeddingBlob)

		// 计算余弦距离
		dist, err := vectordb.CosineDist(embedding, vecEmbedding)
		if err != nil {
			continue // 跳过维度不匹配的向量
		}

		c.distance = dist
		candidates = append(candidates, c)
	}

	// 按距离排序
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].distance < candidates[j].distance
	})

	// 去重：同一文档保留最佳匹配块
	seen := make(map[string]*candidate)
	for i := range candidates {
		c := &candidates[i]
		if existing, ok := seen[c.hash]; !ok || c.distance < existing.distance {
			seen[c.hash] = c
		}
	}

	// 转换为SearchResult
	var results []SearchResult
	for _, c := range seen {
		if len(results) >= limit {
			break
		}

		result := SearchResult{
			ID:         c.hash,
			Score:      1.0 - c.distance, // 余弦相似度
			Title:      c.title,
			Content:    c.body,
			Source:     "vector",
			Collection: c.collection,
			Path:       c.path,
		}
		result.Timestamp, _ = time.Parse(time.RFC3339, c.modifiedAt)
		result.Snippet = extractSnippet(c.body, query, 300)

		results = append(results, result)
	}

	// 按分数排序
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// ReciprocalRankFusion RRF算法融合多个排序列表
func ReciprocalRankFusion(resultLists [][]SearchResult, weights []float64, k int) []SearchResult {
	if k == 0 {
		k = 60 // 默认k值
	}

	type fusionScore struct {
		result   SearchResult
		rrfScore float64
		topRank  int
	}

	scores := make(map[string]*fusionScore)

	// 遍历所有结果列表
	for listIdx, list := range resultLists {
		weight := 1.0
		if listIdx < len(weights) {
			weight = weights[listIdx]
		}

		// 计算每个结果的RRF分数
		for rank, result := range list {
			key := result.ID
			if key == "" {
				key = result.Path
			}

			rrfContribution := weight / float64(k+rank+1)

			if existing, ok := scores[key]; ok {
				existing.rrfScore += rrfContribution
				if rank < existing.topRank {
					existing.topRank = rank
				}
			} else {
				scores[key] = &fusionScore{
					result:   result,
					rrfScore: rrfContribution,
					topRank:  rank,
				}
			}
		}
	}

	// 添加top-rank奖励
	for _, entry := range scores {
		if entry.topRank == 0 {
			entry.rrfScore += 0.05
		} else if entry.topRank <= 2 {
			entry.rrfScore += 0.02
		}
	}

	// 转换为结果列表并排序
	var results []SearchResult
	for _, entry := range scores {
		result := entry.result
		result.Score = entry.rrfScore
		result.Source = "hybrid"
		results = append(results, result)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// buildFTS5Query 构建FTS5查询字符串
func buildFTS5Query(query string) string {
	// 分词并清理
	words := strings.Fields(query)
	var terms []string

	for _, word := range words {
		// 移除非字母数字字符
		cleaned := strings.TrimFunc(word, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})

		if len(cleaned) > 0 {
			// 添加前缀匹配
			terms = append(terms, fmt.Sprintf(`"%s"*`, cleaned))
		}
	}

	if len(terms) == 0 {
		return ""
	}

	// 使用AND连接所有词
	return strings.Join(terms, " AND ")
}

// normalizeBM25Score 将BM25分数转换为[0,1]范围
func normalizeBM25Score(bm25 float64) float64 {
	// BM25分数是负数，绝对值越大越相关
	// 使用 1 / (1 + |score|) 归一化
	absScore := -bm25
	if absScore < 0 {
		absScore = 0
	}
	return 1.0 / (1.0 + absScore)
}

// extractSnippet 提取包含查询词的片段
func extractSnippet(content, query string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}

	// 查找查询词位置
	lowerContent := strings.ToLower(content)
	lowerQuery := strings.ToLower(query)

	idx := strings.Index(lowerContent, lowerQuery)
	if idx == -1 {
		// 未找到，返回开头
		return content[:maxLen] + "..."
	}

	// 在查询词周围提取上下文
	start := idx - maxLen/3
	if start < 0 {
		start = 0
	}

	end := idx + maxLen*2/3
	if end > len(content) {
		end = len(content)
	}

	snippet := content[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet = snippet + "..."
	}

	return snippet
}

// blobToFloat32 将BLOB转换为float32切片
func blobToFloat32(blob []byte) []float32 {
	if len(blob)%4 != 0 {
		return nil
	}

	result := make([]float32, len(blob)/4)
	for i := range result {
		result[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}

	return result
}
