# MMQ - Modu Memory & Query CLI

`mmq` 是一个本地优先的RAG引擎和记忆管理系统的命令行工具。

## 快速开始

### 构建

```bash
 CGO_CFLAGS="-Wno-deprecated-declarations" go build -tags="fts5" -o mmq
```

### 基本使用

```bash
# 1. 创建集合
mmq collection add ~/Documents/notes --name notes --mask "**/*.md"

# 2. 索引文档
mmq update

# 3. 生成嵌入
mmq embed

# 4. 搜索
mmq search "AI chatbot"
mmq vsearch "how to build RAG system"
mmq query "LLM with memory"
```

## 命令

### Collection管理
- `mmq collection add <path> --name <name>` - 创建集合
- `mmq collection list` - 列出所有集合
- `mmq collection remove <name>` - 删除集合
- `mmq collection rename <old> <new>` - 重命名集合

### Context管理
- `mmq context add [path] <content>` - 添加上下文
- `mmq context list` - 列出所有上下文
- `mmq context check` - 检查缺失的上下文
- `mmq context rm <path>` - 删除上下文

### 文档查询
- `mmq ls [collection[/path]]` - 列出文档
- `mmq get <file>` - 获取文档（按路径或docid）
- `mmq multi-get <pattern>` - 批量获取文档

### 管理
- `mmq status` - 显示索引状态
- `mmq update` - 重新索引所有集合
- `mmq embed` - 生成向量嵌入

### 搜索
- `mmq search <query>` - BM25全文搜索
- `mmq vsearch <query>` - 向量语义搜索
- `mmq query <query>` - 混合搜索（最佳质量）

## 全局选项

- `-d, --db <path>` - 数据库路径
- `-c, --collection <name>` - 集合过滤
- `-f, --format <format>` - 输出格式（text|json|csv|md|xml）

## 搜索选项

- `-n <num>` - 结果数量
- `--min-score <score>` - 最小分数阈值
- `--all` - 返回所有匹配
- `--full` - 显示完整内容

## 示例

```bash
# JSON输出
mmq collection list --format json

# 搜索并限制结果
mmq search "RAG system" -n 5 --min-score 0.5

# 获取文档并显示行号
mmq get docs/readme.md --full --line-numbers

# 批量获取并限制行数
mmq multi-get "docs/**/*.md" -l 100

# 使用集合过滤搜索
mmq search "embedding" --collection notes --format md
```

## 环境变量

- `MMQ_DB` - 自定义数据库路径（默认：`~/.cache/mmq/index.db`）

## 参考文档

- [Phase 5.5完成文档](PHASE5.5_COMPLETE.md) - 详细实施说明
- [MMQ包文档](../../pkg/mmq/) - API文档
