package moms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

const (
	webUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	webSearchTimeout     = 10 * time.Second
	webPerplexityTimeout = 30 * time.Second
	webFetchTimeout      = 60 * time.Second

	webDefaultMaxChars     = 50000
	webDefaultMaxResults   = 5
	webMaxRedirects        = 5
	webDefaultFetchLimitMB = 10 * 1024 * 1024
)

var (
	webReScript     = regexp.MustCompile(`<script[\s\S]*?</script>`)
	webReStyle      = regexp.MustCompile(`<style[\s\S]*?</style>`)
	webReTags       = regexp.MustCompile(`<[^>]+>`)
	webReWhitespace = regexp.MustCompile(`[^\S\n]+`)
	webReBlankLines = regexp.MustCompile(`\n{3,}`)

	webReDDGLink    = regexp.MustCompile(`<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>([\s\S]*?)</a>`)
	webReDDGSnippet = regexp.MustCompile(`<a class="result__snippet[^"]*".*?>([\s\S]*?)</a>`)
)

// -----------------------------------------------------------------------
// HTTP client

func createWebHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	transport := &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		switch strings.ToLower(proxy.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("unsupported proxy scheme %q", proxy.Scheme)
		}
		if proxy.Host == "" {
			return nil, fmt.Errorf("invalid proxy URL: missing host")
		}
		transport.Proxy = http.ProxyURL(proxy)
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// -----------------------------------------------------------------------
// Search providers

type webSearchProvider interface {
	Search(ctx context.Context, query string, count int) (string, error)
}

// Brave

type braveSearchProvider struct {
	apiKey string
	client *http.Client
}

func (p *braveSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d", url.QueryEscape(query), count), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("brave api error (%d): %s", resp.StatusCode, body)
	}

	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	items := result.Web.Results
	if len(items) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via Brave)", query))
	for i, r := range items {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, r.Title, r.URL))
		if r.Description != "" {
			lines = append(lines, fmt.Sprintf("   %s", r.Description))
		}
	}
	return strings.Join(lines, "\n"), nil
}

// Tavily

type tavilySearchProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func (p *tavilySearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://api.tavily.com/search"
	}
	payload, _ := json.Marshal(map[string]any{
		"api_key":             p.apiKey,
		"query":               query,
		"search_depth":        "advanced",
		"include_answer":      false,
		"include_images":      false,
		"include_raw_content": false,
		"max_results":         count,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewBuffer(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", webUserAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tavily api error (%d): %s", resp.StatusCode, body)
	}

	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Results) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via Tavily)", query))
	for i, r := range result.Results {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, r.Title, r.URL))
		if r.Content != "" {
			lines = append(lines, fmt.Sprintf("   %s", r.Content))
		}
	}
	return strings.Join(lines, "\n"), nil
}

// DuckDuckGo

type duckDuckGoSearchProvider struct {
	client *http.Client
}

func (p *duckDuckGoSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query)), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", webUserAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return extractDDGResults(string(body), count, query), nil
}

func extractDDGResults(html string, count int, query string) string {
	matches := webReDDGLink.FindAllStringSubmatch(html, count+5)
	if len(matches) == 0 {
		return fmt.Sprintf("No results for: %s", query)
	}
	snippetMatches := webReDDGSnippet.FindAllStringSubmatch(html, count+5)
	maxItems := len(matches)
	if maxItems > count {
		maxItems = count
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via DuckDuckGo)", query))
	for i := range maxItems {
		rawURL := matches[i][1]
		title := strings.TrimSpace(webReTags.ReplaceAllString(matches[i][2], ""))
		if strings.Contains(rawURL, "uddg=") {
			if u, err := url.QueryUnescape(rawURL); err == nil {
				if _, after, ok := strings.Cut(u, "uddg="); ok {
					rawURL = after
				}
			}
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, title, rawURL))
		if i < len(snippetMatches) {
			snippet := strings.TrimSpace(webReTags.ReplaceAllString(snippetMatches[i][1], ""))
			if snippet != "" {
				lines = append(lines, fmt.Sprintf("   %s", snippet))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// Perplexity

type perplexitySearchProvider struct {
	apiKey string
	client *http.Client
}

func (p *perplexitySearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"model": "sonar",
		"messages": []map[string]string{
			{"role": "system", "content": "You are a search assistant. Provide concise search results with titles, URLs, and brief descriptions."},
			{"role": "user", "content": fmt.Sprintf("Search for: %s. Provide up to %d relevant results.", query, count)},
		},
		"max_tokens": 1000,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.perplexity.ai/chat/completions", bytes.NewBuffer(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("perplexity api error (%d): %s", resp.StatusCode, body)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}
	return fmt.Sprintf("Results for: %s (via Perplexity)\n%s", query, result.Choices[0].Message.Content), nil
}

// SearXNG

type searXNGSearchProvider struct {
	baseURL string
}

func (p *searXNGSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/search?q=%s&format=json&categories=general",
			strings.TrimSuffix(p.baseURL, "/"), url.QueryEscape(query)), nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: webSearchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("searxng returned status %d", resp.StatusCode)
	}
	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Results) > count {
		result.Results = result.Results[:count]
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Results for: %s (via SearXNG)\n", query))
	for i, r := range result.Results {
		b.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, r.Title, r.URL))
		if r.Content != "" {
			b.WriteString(fmt.Sprintf("   %s\n", r.Content))
		}
	}
	return b.String(), nil
}

// GLM Search

type glmSearchProvider struct {
	apiKey       string
	baseURL      string
	searchEngine string
	client       *http.Client
}

func (p *glmSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://open.bigmodel.cn/api/paas/v4/web_search"
	}
	engine := p.searchEngine
	if engine == "" {
		engine = "search_std"
	}
	payload, _ := json.Marshal(map[string]any{
		"search_query":  query,
		"search_engine": engine,
		"search_intent": false,
		"count":         count,
		"content_size":  "medium",
	})
	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("glm search api error (%d): %s", resp.StatusCode, body)
	}

	var result struct {
		SearchResult []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			Link    string `json:"link"`
		} `json:"search_result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.SearchResult) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via GLM Search)", query))
	for i, r := range result.SearchResult {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, r.Title, r.Link))
		if r.Content != "" {
			lines = append(lines, fmt.Sprintf("   %s", r.Content))
		}
	}
	return strings.Join(lines, "\n"), nil
}

// -----------------------------------------------------------------------
// WebSearchAgentTool implements agent.AgentTool

// WebSearchConfig holds configuration for the web search tool.
type WebSearchConfig struct {
	Provider         string // brave, tavily, duckduckgo, perplexity, searxng, glm
	MaxResults       int
	Proxy            string
	BraveAPIKey      string
	TavilyAPIKey     string
	TavilyURL        string
	PerplexityAPIKey string
	SearXNGURL       string
	GLMAPIKey        string
	GLMEngine        string
	GLMURL           string
}

// WebSearchAgentTool wraps a search provider as an agent.AgentTool.
type WebSearchAgentTool struct {
	provider   webSearchProvider
	maxResults int
}

// NewWebSearchAgentTool creates a WebSearchAgentTool from config.
// Returns nil, nil if no provider is configured.
func NewWebSearchAgentTool(cfg WebSearchConfig) (*WebSearchAgentTool, error) {
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = webDefaultMaxResults
	}
	var provider webSearchProvider

	switch cfg.Provider {
	case "brave":
		if cfg.BraveAPIKey == "" {
			return nil, fmt.Errorf("MOMS_BRAVE_API_KEY is required for brave provider")
		}
		client, err := createWebHTTPClient(cfg.Proxy, webSearchTimeout)
		if err != nil {
			return nil, err
		}
		provider = &braveSearchProvider{apiKey: cfg.BraveAPIKey, client: client}

	case "tavily":
		if cfg.TavilyAPIKey == "" {
			return nil, fmt.Errorf("MOMS_TAVILY_API_KEY is required for tavily provider")
		}
		client, err := createWebHTTPClient(cfg.Proxy, webSearchTimeout)
		if err != nil {
			return nil, err
		}
		provider = &tavilySearchProvider{apiKey: cfg.TavilyAPIKey, baseURL: cfg.TavilyURL, client: client}

	case "duckduckgo":
		client, err := createWebHTTPClient(cfg.Proxy, webSearchTimeout)
		if err != nil {
			return nil, err
		}
		provider = &duckDuckGoSearchProvider{client: client}

	case "perplexity":
		if cfg.PerplexityAPIKey == "" {
			return nil, fmt.Errorf("MOMS_PERPLEXITY_API_KEY is required for perplexity provider")
		}
		client, err := createWebHTTPClient(cfg.Proxy, webPerplexityTimeout)
		if err != nil {
			return nil, err
		}
		provider = &perplexitySearchProvider{apiKey: cfg.PerplexityAPIKey, client: client}

	case "searxng":
		if cfg.SearXNGURL == "" {
			return nil, fmt.Errorf("MOMS_SEARXNG_URL is required for searxng provider")
		}
		provider = &searXNGSearchProvider{baseURL: cfg.SearXNGURL}

	case "glm":
		if cfg.GLMAPIKey == "" {
			return nil, fmt.Errorf("MOMS_GLM_API_KEY is required for glm provider")
		}
		client, err := createWebHTTPClient(cfg.Proxy, webSearchTimeout)
		if err != nil {
			return nil, err
		}
		provider = &glmSearchProvider{
			apiKey:       cfg.GLMAPIKey,
			baseURL:      cfg.GLMURL,
			searchEngine: cfg.GLMEngine,
			client:       client,
		}

	default:
		return nil, nil
	}

	return &WebSearchAgentTool{provider: provider, maxResults: cfg.MaxResults}, nil
}

func (t *WebSearchAgentTool) Name() string  { return "web_search" }
func (t *WebSearchAgentTool) Label() string { return "Web Search" }
func (t *WebSearchAgentTool) Description() string {
	return "Search the web for current information. Returns titles, URLs, and snippets from search results."
}
func (t *WebSearchAgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": "Number of results (1-10)",
				"minimum":     1,
				"maximum":     10,
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchAgentTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return errorToolResult("query is required"), nil
	}
	count := t.maxResults
	if c, ok := args["count"].(float64); ok && int(c) > 0 && int(c) <= 10 {
		count = int(c)
	}
	result, err := t.provider.Search(ctx, query, count)
	if err != nil {
		return errorToolResult(fmt.Sprintf("search failed: %v", err)), nil
	}
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: result}},
	}, nil
}

// -----------------------------------------------------------------------
// WebFetchAgentTool implements agent.AgentTool

// WebFetchConfig holds configuration for the web fetch tool.
type WebFetchConfig struct {
	MaxChars        int
	Proxy           string
	FetchLimitBytes int64
}

// WebFetchAgentTool fetches URLs and extracts readable text.
type WebFetchAgentTool struct {
	maxChars        int
	client          *http.Client
	fetchLimitBytes int64
}

// NewWebFetchAgentTool creates a WebFetchAgentTool.
func NewWebFetchAgentTool(cfg WebFetchConfig) (*WebFetchAgentTool, error) {
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = webDefaultMaxChars
	}
	if cfg.FetchLimitBytes <= 0 {
		cfg.FetchLimitBytes = webDefaultFetchLimitMB
	}
	client, err := createWebHTTPClient(cfg.Proxy, webFetchTimeout)
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= webMaxRedirects {
			return fmt.Errorf("stopped after %d redirects", webMaxRedirects)
		}
		return nil
	}
	return &WebFetchAgentTool{maxChars: cfg.MaxChars, client: client, fetchLimitBytes: cfg.FetchLimitBytes}, nil
}

func (t *WebFetchAgentTool) Name() string  { return "web_fetch" }
func (t *WebFetchAgentTool) Label() string { return "Web Fetch" }
func (t *WebFetchAgentTool) Description() string {
	return "Fetch a URL and extract readable content (HTML to text). Use this to get weather info, news, articles, or any web content."
}
func (t *WebFetchAgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "URL to fetch",
			},
			"maxChars": map[string]any{
				"type":        "integer",
				"description": "Maximum characters to extract",
				"minimum":     100,
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchAgentTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return errorToolResult("url is required"), nil
	}
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return errorToolResult(fmt.Sprintf("invalid URL: %v", err)), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errorToolResult("only http/https URLs are allowed"), nil
	}
	if parsed.Host == "" {
		return errorToolResult("missing host in URL"), nil
	}

	maxChars := t.maxChars
	if mc, ok := args["maxChars"].(float64); ok && int(mc) > 100 {
		maxChars = int(mc)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return errorToolResult(fmt.Sprintf("failed to create request: %v", err)), nil
	}
	req.Header.Set("User-Agent", webUserAgent)

	resp, err := t.client.Do(req)
	if err != nil {
		return errorToolResult(fmt.Sprintf("request failed: %v", err)), nil
	}
	resp.Body = http.MaxBytesReader(nil, resp.Body, t.fetchLimitBytes)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errorToolResult(fmt.Sprintf("response too large (limit: %d bytes)", t.fetchLimitBytes)), nil
		}
		return errorToolResult(fmt.Sprintf("failed to read response: %v", err)), nil
	}

	contentType := resp.Header.Get("Content-Type")
	var text, extractor string

	if strings.Contains(contentType, "application/json") {
		var jsonData any
		if err := json.Unmarshal(body, &jsonData); err == nil {
			formatted, _ := json.MarshalIndent(jsonData, "", "  ")
			text = string(formatted)
			extractor = "json"
		} else {
			text = string(body)
			extractor = "raw"
		}
	} else if strings.Contains(contentType, "text/html") ||
		(len(body) > 0 && (strings.HasPrefix(string(body), "<!DOCTYPE") || strings.HasPrefix(strings.ToLower(string(body)), "<html"))) {
		text = extractWebText(string(body))
		extractor = "text"
	} else {
		text = string(body)
		extractor = "raw"
	}

	truncated := len(text) > maxChars
	if truncated {
		text = text[:maxChars]
	}

	result, _ := json.MarshalIndent(map[string]any{
		"url":       urlStr,
		"status":    resp.StatusCode,
		"extractor": extractor,
		"truncated": truncated,
		"length":    len(text),
		"text":      text,
	}, "", "  ")

	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: string(result)}},
	}, nil
}

func extractWebText(htmlContent string) string {
	result := webReScript.ReplaceAllLiteralString(htmlContent, "")
	result = webReStyle.ReplaceAllLiteralString(result, "")
	result = webReTags.ReplaceAllLiteralString(result, "")
	result = strings.TrimSpace(result)
	result = webReWhitespace.ReplaceAllString(result, " ")
	result = webReBlankLines.ReplaceAllString(result, "\n\n")

	lines := strings.Split(result, "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
