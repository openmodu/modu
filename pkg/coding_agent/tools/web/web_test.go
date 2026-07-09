package webtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/launcher"
	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

func TestWebFetchReturnsVisibleHTMLText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Example</title><style>.x{}</style></head><body><nav>Noise link</nav><main><h1>Source Title</h1><script>ignore()</script><p>Important <strong>finding</strong> from <a href="/source">the source</a>.</p></main></body></html>`))
	}))
	defer server.Close()

	result, err := NewFetchTool().Execute(context.Background(), "fetch-1", map[string]any{"url": server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	for _, want := range []string{"Status: 200 OK", "Title: Source Title", "# Source Title", "**finding**", server.URL + "/source"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in fetch output:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ignore()") || strings.Contains(text, ".x{}") || strings.Contains(text, "Noise link") {
		t.Fatalf("expected script/style/nav text to be removed:\n%s", text)
	}
}

func TestWebFetchStoresTruncatedArtifactWithoutFullContentDetails(t *testing.T) {
	body := "<html><body><main>" + strings.Repeat("<p>long body text</p>", 5000) + "</main></body></html>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	tool := NewFetchToolWithArtifacts(common.NewArtifactStore(filepath.Join(t.TempDir(), "artifacts")))
	result, err := tool.Execute(context.Background(), "fetch-1", map[string]any{"url": server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	details, ok := result.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %T", result.Details)
	}
	if _, ok := details["content"]; ok {
		t.Fatalf("fetch details should not include full content: %#v", details)
	}
	output, ok := details["output"].(map[string]any)
	if !ok || output["truncated"] != true {
		t.Fatalf("expected truncated output metadata, got %#v", details["output"])
	}
	text := extractText(result.Content)
	if strings.Count(text, "long body text") >= strings.Count(body, "long body text") {
		t.Fatalf("preview should not contain full fetched body, got %d repeated body chunks", strings.Count(text, "long body text"))
	}
	artifactPath, ok := output["artifactPath"].(string)
	if !ok || artifactPath == "" {
		t.Fatalf("expected artifact path, got %#v", output)
	}
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "long body text") {
		t.Fatalf("expected artifact to contain fetched body, got %d bytes", len(data))
	}
}

func TestFetchJSONIncludesMetadataAndMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Example JSON</title><meta name="author" content="Ada"></head><body><article><h1>Report</h1><p>Useful body.</p></article></body></html>`))
	}))
	defer server.Close()

	page, err := Fetch(context.Background(), nil, server.URL, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := page.JSON()
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{`"title": "Report"`, "# Report", "Useful body."} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in JSON:\n%s", want, got)
		}
	}
}

func TestFetchWithJSRender(t *testing.T) {
	if os.Getenv("MODU_WEB_FETCH_ROD_TEST") != "1" {
		t.Skip("set MODU_WEB_FETCH_ROD_TEST=1 to run Rod browser integration test")
	}
	if _, ok := launcher.LookPath(); !ok {
		t.Skip("no Chrome/Chromium browser found")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>JS page</title></head><body><main id="app">loading</main><script>setTimeout(() => { document.getElementById('app').innerHTML = '<article><h1>Rendered JS</h1><p>Client generated body.</p></article>'; }, 20);</script></body></html>`))
	}))
	defer server.Close()

	page, err := Fetch(context.Background(), nil, server.URL, FetchOptions{
		JSRender: true,
		JSWait:   200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Rendered JS", "Client generated body."} {
		if !strings.Contains(page.Content, want) {
			t.Fatalf("expected %q in rendered markdown:\n%s", want, page.Content)
		}
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

func TestWebSearchUsesExaEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("x-api-key"); got != "test-exa-key" {
			t.Fatalf("x-api-key = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if got := payload["query"]; got != "modu workflow" {
			t.Fatalf("query = %#v", got)
		}
		if got := payload["numResults"]; got != float64(2) {
			t.Fatalf("numResults = %#v", got)
		}
		if got := payload["type"]; got != "fast" {
			t.Fatalf("type = %#v", got)
		}
		contents, _ := payload["contents"].(map[string]any)
		if contents["highlights"] != true {
			t.Fatalf("contents.highlights = %#v", contents["highlights"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"requestId": "req-123",
			"resolvedSearchType": "fast",
			"costDollars": {"total": 0},
			"results": [
				{
					"title": "First Result",
					"url": "https://example.com/one",
					"publishedDate": "2026-07-09T00:00:00.000Z",
					"author": "Ada",
					"highlights": ["Primary source snippet."]
				},
				{
					"title": "Second Result",
					"url": "https://example.com/two",
					"text": "Secondary text snippet."
				},
				{
					"title": "Third Result",
					"url": "https://example.com/three",
					"text": "Should be capped by max_results."
				}
			]
		}`))
	}))
	defer server.Close()

	result, err := NewSearchToolWithConfig(SearchConfig{
		Provider:   "exa",
		Endpoint:   server.URL,
		APIKey:     "test-exa-key",
		SearchType: "fast",
	}).Execute(context.Background(), "search-1", map[string]any{
		"query":       "modu workflow",
		"max_results": 2,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	for _, want := range []string{"Provider: exa", "First Result", "https://example.com/one", "Published: 2026-07-09T00:00:00.000Z", "Author: Ada", "Primary source snippet.", "Second Result", "Secondary text snippet."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in search output:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Third Result") {
		t.Fatalf("expected max_results cap, got:\n%s", text)
	}
	details, ok := result.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %T", result.Details)
	}
	if details["provider"] != "exa" || details["request_id"] != "req-123" {
		t.Fatalf("unexpected details: %#v", details)
	}
}

func TestWebSearchExaRequiresAPIKey(t *testing.T) {
	result, err := NewExaSearchToolWithEndpoint("https://example.test/search", "").Execute(context.Background(), "search-1", map[string]any{
		"query": "modu workflow",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "exa api key is required") {
		t.Fatalf("unexpected result: %s", text)
	}
}

func TestWebSearchUsesTavilyEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tavily-key" {
			t.Fatalf("authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["query"] != "modu workflow" || payload["max_results"] != float64(2) || payload["search_depth"] != "basic" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"request_id": "tvly-1",
			"usage": {"credits": 1},
			"results": [
				{"title": "Tavily One", "url": "https://example.com/tavily-one", "content": "Tavily snippet."},
				{"title": "Tavily Two", "url": "https://example.com/tavily-two", "content": "Second snippet."}
			]
		}`))
	}))
	defer server.Close()

	result, err := NewSearchToolWithConfig(SearchConfig{
		Provider:   "tavily",
		Endpoint:   server.URL,
		APIKey:     "tavily-key",
		SearchType: "basic",
	}).Execute(context.Background(), "search-1", map[string]any{"query": "modu workflow", "max_results": 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	for _, want := range []string{"Provider: tavily", "Tavily One", "https://example.com/tavily-one", "Tavily snippet."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in search output:\n%s", want, text)
		}
	}
}

func TestWebSearchUsesBraveEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("X-Subscription-Token"); got != "brave-key" {
			t.Fatalf("X-Subscription-Token = %q", got)
		}
		if r.URL.Query().Get("q") != "modu workflow" || r.URL.Query().Get("count") != "2" || r.URL.Query().Get("result_filter") != "web" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"web": {
				"results": [
					{"title": "Brave One", "url": "https://example.com/brave-one", "description": "Brave snippet.", "age": "2026-07-09"},
					{"title": "Brave Two", "url": "https://example.com/brave-two", "extra_snippets": ["Fallback snippet."]}
				]
			}
		}`))
	}))
	defer server.Close()

	result, err := NewSearchToolWithConfig(SearchConfig{
		Provider: "brave",
		Endpoint: server.URL,
		APIKey:   "brave-key",
	}).Execute(context.Background(), "search-1", map[string]any{"query": "modu workflow", "max_results": 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	for _, want := range []string{"Provider: brave", "Brave One", "https://example.com/brave-one", "Brave snippet.", "Published: 2026-07-09", "Fallback snippet."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in search output:\n%s", want, text)
		}
	}
}

func TestWebSearchUsesFirecrawlEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer firecrawl-key" {
			t.Fatalf("authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["query"] != "modu workflow" || payload["limit"] != float64(2) {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"id": "fc-search-1",
			"creditsUsed": 2,
			"data": [
				{"title": "Firecrawl One", "url": "https://example.com/fire-one", "description": "Firecrawl snippet."},
				{"metadata": {"title": "Firecrawl Two", "sourceURL": "https://example.com/fire-two", "description": "Metadata snippet."}}
			]
		}`))
	}))
	defer server.Close()

	result, err := NewSearchToolWithConfig(SearchConfig{
		Provider: "firecrawl",
		Endpoint: server.URL,
		APIKey:   "firecrawl-key",
	}).Execute(context.Background(), "search-1", map[string]any{"query": "modu workflow", "max_results": 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	for _, want := range []string{"Provider: firecrawl", "Firecrawl One", "https://example.com/fire-one", "Firecrawl snippet.", "Firecrawl Two", "Metadata snippet."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in search output:\n%s", want, text)
		}
	}
}

func TestWebFetchUsesFirecrawlScrape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer firecrawl-key" {
			t.Fatalf("authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["url"] != "https://example.com/source" {
			t.Fatalf("url = %#v", payload["url"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"markdown": "# Firecrawl Page\n\nClean markdown body.",
				"metadata": {
					"title": "Firecrawl Page",
					"description": "Clean page",
					"sourceURL": "https://example.com/source",
					"contentType": "text/markdown"
				}
			}
		}`))
	}))
	defer server.Close()

	tool := NewFetchToolWithConfig(nil, FetchConfig{
		Provider: "firecrawl",
		Endpoint: server.URL,
		APIKey:   "firecrawl-key",
	})
	result, err := tool.Execute(context.Background(), "fetch-1", map[string]any{"url": "https://example.com/source"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	for _, want := range []string{"URL: https://example.com/source", "Title: Firecrawl Page", "# Firecrawl Page", "Clean markdown body."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in fetch output:\n%s", want, text)
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
