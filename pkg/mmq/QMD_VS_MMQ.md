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
| **Query Expansion** | **Yes** (LLM generates variants -> Parallel Search) | **Yes** (LLM generates lex/vec/hyde variants with caching) | ✅ **Parity** |
| **Reranking** | **Yes** (qwen3-reranker via node-llama-cpp) | **Yes** (Rerank interface + MockLLM/LlamaCpp implementations) | ✅ **Parity** |
| **LLM Inference** | `node-llama-cpp` (GGUF, auto-download) | `go-llama.cpp` wrapper (Needs `-tags llama`, Mock available) | ✅ **Parity** |
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

### 3. Query Pipeline ✅ **RESOLVED**
**Status:** `mmq` now supports full query expansion pipeline.

**Implementation:**
- `LLM.ExpandQuery()` generates query variants (lex/vec/hyde types)
- Each variant uses appropriate search strategy (FTS for lex, vector for vec/hyde)
- Results fused using RRF with variant-specific weights
- Full caching support via `store.CacheKey()` and LLM cache table
- Enable via `RetrieveOptions.ExpandQuery = true`

**Pipeline comparison:**
- **QMD:** `Query` -> `LLM Expansion` -> `[Q1, Q2, Q3]` -> `Search(Q1, Q2, Q3)` -> `Fusion`
- **MMQ:** `Query` -> `LLM Expansion` -> `[Q1, Q2, Q3]` -> `Search(Q1, Q2, Q3)` -> `Fusion`

**Testing:** See pkg/mmq/advanced_features_test.go (all tests passing)

### 4. LLM Integration ✅ **ADDRESSED**
**Status:** `mmq` provides dual-mode LLM support.

**Implementation:**
- **MockLLM**: Works out-of-the-box, no build tags needed. Perfect for development and testing.
- **LlamaCpp**: Full llama.cpp integration via `go-llama.cpp` (requires `-tags llama`)
- Both implement the same `LLM` interface, allowing seamless switching

**Usability:**
- Development: Use MockLLM (no dependencies, instant startup)
- Production: Build with `-tags "fts5,llama"` for real models
- The MockLLM provides realistic test behavior (deterministic embeddings, simple reranking)

**Recommendation:** CLI binaries should be pre-built with llama tags, or offer separate builds (lite vs full).

## Conclusion

`mmq` now has **complete feature parity** with `qmd` at the core RAG library level:

### Core Features (100% Complete)
- ✅ SQLite FTS5 for full-text search (BM25)
- ✅ `sqlite-vec` for efficient vector search (cosine similarity)
- ✅ RRF hybrid search fusion
- ✅ Document management (collections, contexts, content-addressable storage)
- ✅ Embedding storage and retrieval
- ✅ **Query Expansion** - LLM-based query variants (lex/vec/hyde)
- ✅ **Reranking** - LLM-based result reranking
- ✅ **LLM Inference** - Text generation via unified LLM interface
- ✅ **Caching** - LLM result caching (query expansion, reranking)

### Architecture Strengths
- **Modular Design**: Clean separation of concerns (store, llm, rag)
- **Interface-Based**: Easy to swap implementations (Mock ↔ LlamaCpp)
- **Testing**: Comprehensive test coverage (basic + advanced features)
- **Scalable**: sqlite-vec ensures performance at scale

### Remaining Gaps for End-User Parity
1. ❌ **CLI Tool** - Command-line interface (Phase 5.5)
2. ❌ **MCP Server** - Model Context Protocol support (Phase 5.6)
3. ❌ **Model Management** - Auto-download GGUF models like qmd

**The library foundation is complete and production-ready.** Focus can now shift to application layer (CLI, MCP, UX).
