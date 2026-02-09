package mmq

import "time"

// RetrievalStrategy 检索策略类型
type RetrievalStrategy string

const (
	// StrategyFTS 仅使用BM25全文搜索
	StrategyFTS RetrievalStrategy = "fts"
	// StrategyVector 仅使用向量语义搜索
	StrategyVector RetrievalStrategy = "vector"
	// StrategyHybrid 混合搜索+重排（最佳质量）
	StrategyHybrid RetrievalStrategy = "hybrid"
)

// MemoryType 记忆类型
type MemoryType string

const (
	// MemoryTypeConversation 对话记忆
	MemoryTypeConversation MemoryType = "conversation"
	// MemoryTypeFact 事实记忆
	MemoryTypeFact MemoryType = "fact"
	// MemoryTypePreference 偏好记忆
	MemoryTypePreference MemoryType = "preference"
	// MemoryTypeEpisodic 情景记忆
	MemoryTypeEpisodic MemoryType = "episodic"
)

// SearchResult 搜索结果
type SearchResult struct {
	ID         string                 `json:"id"`
	Score      float64                `json:"score"`
	Title      string                 `json:"title"`
	Content    string                 `json:"content"`
	Snippet    string                 `json:"snippet,omitempty"`
	Source     string                 `json:"source"` // "fts", "vector", "hybrid"
	Collection string                 `json:"collection"`
	Path       string                 `json:"path"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
}

// Context RAG上下文
type Context struct {
	Text      string                 `json:"text"`
	Source    string                 `json:"source"`
	Relevance float64                `json:"relevance"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Memory 记忆
type Memory struct {
	ID         string                 `json:"id"`
	Type       MemoryType             `json:"type"`
	Content    string                 `json:"content"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	Tags       []string               `json:"tags,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
	ExpiresAt  *time.Time             `json:"expires_at,omitempty"` // 可选过期时间
	Importance float64                `json:"importance"`            // 重要性权重 0.0-1.0
}

// Document 文档
type Document struct {
	ID         string                 `json:"id"`
	Collection string                 `json:"collection"`
	Path       string                 `json:"path"`
	Title      string                 `json:"title"`
	Content    string                 `json:"content"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	ModifiedAt time.Time              `json:"modified_at"`
}

// RetrieveOptions 检索选项
type RetrieveOptions struct {
	Limit      int               // 返回结果数量
	MinScore   float64           // 最小相关度分数
	Collection string            // 集合过滤
	Strategy   RetrievalStrategy // 检索策略
	Rerank     bool              // 是否使用LLM重排
}

// SearchOptions 搜索选项
type SearchOptions struct {
	Limit      int     // 返回结果数量
	MinScore   float64 // 最小分数
	Collection string  // 集合过滤
}

// IndexOptions 索引选项
type IndexOptions struct {
	Mask       string // Glob模式，如 "**/*.md"
	Recursive  bool   // 是否递归
	Collection string // 集合名称
}

// Status 索引状态
type Status struct {
	TotalDocuments int      `json:"total_documents"`
	NeedsEmbedding int      `json:"needs_embedding"`
	Collections    []string `json:"collections"`
	DBPath         string   `json:"db_path"`
	CacheDir       string   `json:"cache_dir"`
}

// RecallOptions 记忆回忆选项
type RecallOptions struct {
	Limit               int          // 返回记忆数量
	MemoryTypes         []MemoryType // 过滤记忆类型
	ApplyDecay          bool         // 是否应用时间衰减
	DecayHalflife       time.Duration // 衰减半衰期
	WeightByImportance  bool         // 是否按重要性加权
	MinRelevance        float64      // 最小相关度
}

// Collection 集合
type Collection struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Mask      string    `json:"mask"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	DocCount  int       `json:"doc_count"`
}

// CollectionOptions 集合选项
type CollectionOptions struct {
	Mask      string // Glob模式，如 "**/*.md"
	Recursive bool   // 是否递归（默认true）
	GitPull   bool   // 是否先执行git pull
}

// ContextEntry 上下文条目
type ContextEntry struct {
	Path      string    `json:"path"`       // 路径（/为全局，mmq://collection为集合级）
	Content   string    `json:"content"`    // 上下文内容
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

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
