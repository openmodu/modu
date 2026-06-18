package webtools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestWebFetchReturnsVisibleHTMLText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Example</title><style>.x{}</style></head><body><h1>Source Title</h1><script>ignore()</script><p>Important finding.</p></body></html>`))
	}))
	defer server.Close()

	result, err := NewFetchTool().Execute(context.Background(), "fetch-1", map[string]any{"url": server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	for _, want := range []string{"Status: 200 OK", "Source Title", "Important finding."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in fetch output:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ignore()") || strings.Contains(text, ".x{}") {
		t.Fatalf("expected script/style text to be removed:\n%s", text)
	}
}

func TestWebFetchRejectsNonHTTPURLs(t *testing.T) {
	result, err := NewFetchTool().Execute(context.Background(), "fetch-1", map[string]any{"url": "file:///tmp/a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "url must use http or https") {
		t.Fatalf("unexpected result: %s", text)
	}
}

func TestWebSearchUsesEndpointAndParsesResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "modu workflow" {
			t.Fatalf("query = %q", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>
<div class="result"><a href="https://example.com/one">First Result</a><span>Primary source snippet.</span></div>
<div class="result"><a href="/two">Second Result</a><span>Secondary snippet.</span></div>
</body></html>`))
	}))
	defer server.Close()

	result, err := NewSearchToolWithEndpoint(server.URL+"/search").Execute(context.Background(), "search-1", map[string]any{
		"query":       "modu workflow",
		"max_results": 2,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	for _, want := range []string{"Search results for \"modu workflow\"", "First Result", "https://example.com/one", server.URL + "/two"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in search output:\n%s", want, text)
		}
	}
}

func extractText(content []types.ContentBlock) string {
	var b strings.Builder
	for _, block := range content {
		if text, ok := block.(*types.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
