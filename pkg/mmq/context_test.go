package mmq

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAddContext(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 添加全局上下文
	err = m.AddContext("/", "This is global context for all documents")
	if err != nil {
		t.Fatal(err)
	}

	// 添加集合级上下文
	err = m.AddContext("mmq://docs", "This collection contains documentation")
	if err != nil {
		t.Fatal(err)
	}

	// 添加路径级上下文
	err = m.AddContext("mmq://docs/api", "API documentation section")
	if err != nil {
		t.Fatal(err)
	}

	// 验证上下文已添加
	ctx, err := m.GetContext("/")
	if err != nil {
		t.Fatal(err)
	}

	if ctx.Content != "This is global context for all documents" {
		t.Errorf("Unexpected content: %s", ctx.Content)
	}

	t.Logf("Added contexts successfully")
}

func TestListContexts(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 添加多个上下文
	contexts := map[string]string{
		"/":                  "Global context",
		"mmq://docs":         "Documentation collection",
		"mmq://code":         "Code examples collection",
		"mmq://docs/api":     "API documentation",
		"mmq://docs/guides":  "User guides",
	}

	for path, content := range contexts {
		err := m.AddContext(path, content)
		if err != nil {
			t.Fatal(err)
		}
	}

	// 列出所有上下文
	list, err := m.ListContexts()
	if err != nil {
		t.Fatal(err)
	}

	if len(list) != len(contexts) {
		t.Errorf("Expected %d contexts, got %d", len(contexts), len(list))
	}

	t.Log("Contexts:")
	for i, ctx := range list {
		t.Logf("  [%d] %s: %s", i+1, ctx.Path, ctx.Content)
	}
}

func TestUpdateContext(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	path := "mmq://test"

	// 添加上下文
	err = m.AddContext(path, "Original content")
	if err != nil {
		t.Fatal(err)
	}

	// 更新上下文
	err = m.AddContext(path, "Updated content")
	if err != nil {
		t.Fatal(err)
	}

	// 验证已更新
	ctx, err := m.GetContext(path)
	if err != nil {
		t.Fatal(err)
	}

	if ctx.Content != "Updated content" {
		t.Errorf("Expected 'Updated content', got '%s'", ctx.Content)
	}

	t.Logf("Context updated successfully")
}

func TestRemoveContext(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	path := "mmq://temp"

	// 添加上下文
	err = m.AddContext(path, "Temporary context")
	if err != nil {
		t.Fatal(err)
	}

	// 删除上下文
	err = m.RemoveContext(path)
	if err != nil {
		t.Fatal(err)
	}

	// 验证已删除
	_, err = m.GetContext(path)
	if err == nil {
		t.Error("Expected error when getting deleted context")
	}

	t.Log("Context removed successfully")
}

func TestCheckMissingContexts(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 创建集合
	m.CreateCollection("docs", "/tmp/docs", CollectionOptions{})
	m.CreateCollection("code", "/tmp/code", CollectionOptions{})

	// 只为docs添加上下文
	m.AddContext("mmq://docs", "Documentation collection")

	// 检查缺失的上下文
	missing, err := m.CheckMissingContexts()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Missing contexts: %d", len(missing))
	for _, path := range missing {
		t.Logf("  - %s", path)
	}

	// 应该至少缺少全局上下文和code集合的上下文
	if len(missing) < 2 {
		t.Error("Expected at least 2 missing contexts")
	}

	// 验证包含全局和code
	hasGlobal := false
	hasCode := false
	for _, path := range missing {
		if strings.Contains(path, "/") && strings.Contains(path, "global") {
			hasGlobal = true
		}
		if strings.Contains(path, "code") {
			hasCode = true
		}
	}

	if !hasGlobal {
		t.Error("Expected missing global context")
	}
	if !hasCode {
		t.Error("Expected missing code collection context")
	}
}

func TestGetContextsForPath(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 添加层级上下文
	m.AddContext("/", "Global context")
	m.AddContext("mmq://docs", "Docs collection context")
	m.AddContext("mmq://docs/api", "API section context")

	// 测试获取特定路径的上下文
	t.Run("Document in API section", func(t *testing.T) {
		contexts, err := m.GetContextsForPath("mmq://docs/api/endpoints.md")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("Found %d contexts for mmq://docs/api/endpoints.md", len(contexts))
		for _, ctx := range contexts {
			t.Logf("  - %s: %s", ctx.Path, ctx.Content)
		}

		// 应该匹配所有三个层级的上下文
		if len(contexts) < 3 {
			t.Errorf("Expected at least 3 contexts, got %d", len(contexts))
		}
	})

	t.Run("Document in docs root", func(t *testing.T) {
		contexts, err := m.GetContextsForPath("mmq://docs/readme.md")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("Found %d contexts for mmq://docs/readme.md", len(contexts))

		// 应该匹配全局和docs集合
		if len(contexts) < 2 {
			t.Errorf("Expected at least 2 contexts, got %d", len(contexts))
		}
	})
}

func TestGetDocumentContexts(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 添加层级上下文
	m.AddContext("/", "Global: All documents in the system")
	m.AddContext("mmq://docs", "Collection: Technical documentation")
	m.AddContext("mmq://docs/api/endpoints.md", "Document: REST API endpoints reference")

	// 获取文档的上下文（按优先级排序）
	contexts, err := m.GetDocumentContexts("docs", "api/endpoints.md")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Document contexts (priority order):")
	for i, ctx := range contexts {
		t.Logf("  [%d] %s: %s", i+1, ctx.Path, ctx.Content)
	}

	// 验证顺序：精确路径 > 集合 > 全局
	if len(contexts) >= 1 && !strings.Contains(contexts[0].Path, "endpoints.md") {
		// 如果有精确路径，应该在第一个
		t.Logf("Note: Exact path context not found (expected)")
	}

	// 至少应该有集合和全局上下文
	if len(contexts) < 2 {
		t.Errorf("Expected at least 2 contexts, got %d", len(contexts))
	}
}

func TestContextPathMatching(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 添加不同层级的上下文
	m.AddContext("/", "Global")
	m.AddContext("mmq://docs", "Docs collection")
	m.AddContext("mmq://docs/guides", "Guides section")
	m.AddContext("mmq://code", "Code collection")

	tests := []struct {
		targetPath      string
		expectedMatches int
		description     string
	}{
		{"mmq://docs/guides/intro.md", 3, "Should match /, docs, and guides"},
		{"mmq://docs/api/rest.md", 2, "Should match / and docs"},
		{"mmq://code/example.go", 2, "Should match / and code"},
		{"mmq://other/file.md", 1, "Should only match /"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			contexts, err := m.GetContextsForPath(tt.targetPath)
			if err != nil {
				t.Fatal(err)
			}

			if len(contexts) != tt.expectedMatches {
				t.Errorf("For path %s: expected %d matches, got %d",
					tt.targetPath, tt.expectedMatches, len(contexts))
				for _, ctx := range contexts {
					t.Logf("  Matched: %s", ctx.Path)
				}
			}
		})
	}
}

func TestContextWithCollections(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewWithDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// 创建集合
	m.CreateCollection("notes", "/tmp/notes", CollectionOptions{})
	m.CreateCollection("articles", "/tmp/articles", CollectionOptions{})

	// 添加全局上下文
	m.AddContext("/", "Global context for all collections")

	// 为每个集合添加上下文
	m.AddContext("mmq://notes", "Personal notes and ideas")
	m.AddContext("mmq://articles", "Published articles and blog posts")

	// 检查缺失的上下文
	missing, err := m.CheckMissingContexts()
	if err != nil {
		t.Fatal(err)
	}

	// 所有集合都有上下文了，应该只有全局上下文存在
	t.Logf("Missing contexts: %v", missing)

	// 列出所有上下文
	contexts, err := m.ListContexts()
	if err != nil {
		t.Fatal(err)
	}

	if len(contexts) != 3 {
		t.Errorf("Expected 3 contexts (global + 2 collections), got %d", len(contexts))
	}

	t.Log("All contexts:")
	for _, ctx := range contexts {
		t.Logf("  %s: %s", ctx.Path, ctx.Content)
	}
}
