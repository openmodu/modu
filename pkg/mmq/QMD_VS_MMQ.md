# MMQ vs QMD Feature Review

Target: Benchmark `mmq` (Go) against `qmd` (TypeScript/Bun).

## Executive Summary

`mmq` has successfully implemented the core RAG primitives (Storage, FTS, Vector Search, Hybrid Fusion, Document Management) but lacks the **User-Facing Application Layer (CLI)** and some **Advanced Search Pipeline** features (Query Expansion, specialized Vector Indexing) that `qmd` provides.

If the goal is to have a library, `mmq` is in a good state (Phase 5.4). If the goal is a drop-in implementation replacement for `qmd` tool usage, `mmq` is currently incomplete (missing CLI, robust local LLM default integration).

## Detailed Comparison

| Feature Category | QMD (Target) | MMQ (Current) | Status |
| :--- | :--- | :--- | :--- |
| **Interface** | **CLI Tool** (`qmd search`, `qmd server`, etc.) | **Go Library** (`pkg/mmq`) | ❌ **Missing** (Planned Phase 5.5) |
| **Full Text Search** | SQLite FTS5 (BM25) | SQLite FTS5 (BM25) | ✅ Parity |
| **Vector Search** | `sqlite-vec` (Vector Index) | **`sqlite-vec`** (Vector Index, vec0 virtual table) | ✅ **Parity** |
| **Hybrid Search** | RRF Fusion (BM25 + Vector) | RRF Fusion (BM25 + Vector) | ✅ Parity |
| **Query Expansion** | **Yes** (LLM generates variants -> Parallel Search) | **No** (Searches only original query) | ❌ **Missing** |
| **Reranking** | **Yes** (qwen3-reranker via node-llama-cpp) | **Partial** (Logic exists, defaults to Mock, needs build tags) | ⚠️ Verification Needed |
| **LLM Inference** | `node-llama-cpp` (GGUF, auto-download) | `go-llama.cpp` wrapper (Needs `-tags llama`) | ⚠️ Usability Barrier |
| **Document Mgmt** | Add/Remove Collections, Contexts, Globs | Full CRUD, Collections, Contexts supported | ✅ Parity |
| **Protocol** | Native CLI + MCP Server | Library only (MCP planned Phase 5.6) | ❌ Missing MCP |

## Critical Gaps & Recommendations

### 1. Missing CLI Entry Point
`qmd` is primarily used as a command-line tool. `mmq` currently resides in `pkg/` and requires a `cmd/mmq/main.go` to be useful to an end-user.
**Recommendation:** Implement the `cobra` based CLI immediately (Phase 5.5).

### 2. Vector Search Scalability ✅ **RESOLVED**
**Status:** `mmq` now uses `sqlite-vec` with the same two-step query pattern as `qmd`.

**Implementation:**
- Uses `vec0` virtual table with cosine distance metric
- Two-step query to avoid JOIN performance issues:
  1. Query `vectors_vec` using MATCH operator for k-NN
  2. Fetch document metadata using hash_seq keys
- Vectors stored in dual tables: `content_vectors` (metadata) + `vectors_vec` (index)
- Dependency: `github.com/asg017/sqlite-vec-go-bindings/cgo v0.1.6`

**Testing:** All vector search tests passing (see pkg/mmq/store/search_vector.go)

### 3. Query Pipeline Simplification
`mmq`'s `HybridSearch` is simpler than `qmd`'s.
- **QMD:** `Query` -> `LLM Expansion` -> `[Q1, Q2, Q3]` -> `Search(Q1, Q2, Q3)` -> `Fusion`
- **MMQ:** `Query` -> `Search(Query)` -> `Fusion`
**Recommendation:** Implement the Query Expansion step in `pkg/mmq/rag/retriever.go`.

### 4. LLM Integration Friction
`mmq` requires compiling with CGO and build tags (`-tags "fts5,llama"`) to get real LLM features. `qmd` (via Bun/Node) handles this somewhat more transparently for the user by downloading pre-built binaries or using N-API bindings.
**Recommendation:** Ensure the CLI release pipeline produces binaries with `llama` tags enabled, or provide a strictly "server/client" model where the server handles the heavy lifting.

## Conclusion

`mmq` now has **feature parity** with `qmd` at the core RAG library level, including:
- ✅ SQLite FTS5 for full-text search
- ✅ `sqlite-vec` for efficient vector search
- ✅ RRF hybrid search fusion
- ✅ Document management (collections, contexts)
- ✅ Embedding storage and retrieval

**Remaining gaps for end-user parity:**
1. ❌ **Query Expansion** - LLM-based query variants
2. ❌ **CLI Tool** - Command-line interface (Phase 5.5)
3. ❌ **MCP Server** - Model Context Protocol support (Phase 5.6)

The library foundation is now solid and scalable. Focus can shift to application layer features.
