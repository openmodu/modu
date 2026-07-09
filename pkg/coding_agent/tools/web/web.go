package webtools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	trafilatura "github.com/markusmobius/go-trafilatura"
	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
	nethtml "golang.org/x/net/html"
)

const (
	defaultFetchMaxBytes           = 2 * 1024 * 1024
	maxFetchBytes                  = 32 * 1024 * 1024
	defaultSearchEndpoint          = "https://duckduckgo.com/html/?q={query}"
	defaultExaEndpoint             = "https://api.exa.ai/search"
	defaultTavilyEndpoint          = "https://api.tavily.com/search"
	defaultBraveEndpoint           = "https://api.search.brave.com/res/v1/web/search"
	defaultFirecrawlSearchEndpoint = "https://api.firecrawl.dev/v2/search"
	defaultFirecrawlScrapeEndpoint = "https://api.firecrawl.dev/v2/scrape"
	defaultSearchMaxBytes          = 128 * 1024
	defaultSearchResults           = 5
	maxSearchResults               = 10
	defaultJSWait                  = 2 * time.Second
	maxJSWait                      = 15 * time.Second
	defaultFetchUserAgent          = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type FetchTool struct {
	client    HTTPClient
	artifacts *common.ArtifactStore
	config    FetchConfig
}

type FetchConfig struct {
	Provider  string
	Endpoint  string
	APIKey    string
	APIKeyEnv string
}

type FetchOptions struct {
	MaxBytes int
	Raw      bool
	JSRender bool
	JSWait   time.Duration
}

type FetchPage struct {
	URL          string `json:"url"`
	Status       string `json:"status"`
	ContentType  string `json:"content_type"`
	Bytes        int    `json:"bytes"`
	Truncated    bool   `json:"truncated"`
	RenderedHTML bool   `json:"rendered_html"`
	Extracted    bool   `json:"extracted"`
	Title        string `json:"title,omitempty"`
	Author       string `json:"author,omitempty"`
	Date         string `json:"date,omitempty"`
	SiteName     string `json:"site_name,omitempty"`
	Description  string `json:"description,omitempty"`
	Content      string `json:"content"`
}

func NewFetchTool() types.Tool {
	return &FetchTool{client: &http.Client{Timeout: 15 * time.Second}}
}

func NewFetchToolWithArtifacts(artifacts *common.ArtifactStore) types.Tool {
	return &FetchTool{client: &http.Client{Timeout: 15 * time.Second}, artifacts: artifacts}
}

func NewFetchToolWithConfig(artifacts *common.ArtifactStore, cfg FetchConfig) types.Tool {
	return &FetchTool{client: &http.Client{Timeout: 15 * time.Second}, artifacts: artifacts, config: cfg}
}

func (t *FetchTool) Name() string  { return "web_fetch" }
func (t *FetchTool) Label() string { return "Web Fetch" }
func (t *FetchTool) Description() string {
	return "Fetch an HTTP or HTTPS URL and return readable text. Use this for source pages found during research."
}
func (t *FetchTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "HTTP or HTTPS URL to fetch",
			},
			"max_bytes": map[string]any{
				"type":        "integer",
				"description": "Maximum response bytes to read, capped at 33554432. Defaults to 2097152.",
			},
			"raw": map[string]any{
				"type":        "boolean",
				"description": "Return raw response text instead of extracting Markdown from HTML.",
			},
			"js_render": map[string]any{
				"type":        "boolean",
				"description": "Render the page in a headless browser with JavaScript before extracting Markdown.",
			},
			"js_wait_ms": map[string]any{
				"type":        "integer",
				"description": "Additional browser wait time after page load when js_render is true. Capped at 15000ms. Defaults to 2000ms.",
			},
		},
		"required": []string{"url"},
	}
}
func (t *FetchTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	target, _ := args["url"].(string)
	raw, _ := args["raw"].(bool)
	jsRender, _ := args["js_render"].(bool)
	opts := FetchOptions{
		MaxBytes: boundedInt(args["max_bytes"], defaultFetchMaxBytes, maxFetchBytes),
		Raw:      raw,
		JSRender: jsRender,
		JSWait:   boundedDurationMS(args["js_wait_ms"], defaultJSWait, maxJSWait),
	}
	var page *FetchPage
	var err error
	if strings.EqualFold(strings.TrimSpace(t.config.Provider), "firecrawl") {
		page, err = FetchFirecrawl(ctx, t.client, target, t.config, opts)
	} else {
		page, err = Fetch(ctx, t.client, target, opts)
	}
	if err != nil {
		return common.ErrorResult(err.Error()), nil
	}
	rawText := formatFetchText(page)
	preview := common.PreviewText(rawText, common.TextPreviewOptions{
		ToolCallID:    toolCallID,
		ArtifactName:  "web-fetch",
		ArtifactStore: t.artifacts,
		Strategy:      common.PreviewHead,
		MaxLines:      common.DefaultMaxLines,
		MaxBytes:      common.DefaultMaxBytes,
	})
	return textResult(preview.Text, fetchDetails(page, preview.Details["output"])), nil
}

func Fetch(ctx context.Context, client HTTPClient, target string, opts FetchOptions) (*FetchPage, error) {
	u, err := validateHTTPURL(target)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultFetchMaxBytes
	}
	if maxBytes > maxFetchBytes {
		maxBytes = maxFetchBytes
	}
	if opts.JSRender {
		doc, err := renderJavaScriptHTML(ctx, u.String(), boundedDuration(opts.JSWait, defaultJSWait, maxJSWait))
		if err != nil {
			return nil, fmt.Errorf("js render failed: %w", err)
		}
		page := &FetchPage{
			URL:         u.String(),
			Status:      "200 OK (browser rendered)",
			ContentType: "text/html; charset=utf-8",
			Bytes:       len(doc),
			Content:     doc,
		}
		if opts.Raw {
			return page, nil
		}
		return extractPageMarkdown(page, doc, u), nil
	}
	body, status, contentType, truncated, err := fetch(ctx, client, u.String(), maxBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}

	page := &FetchPage{
		URL:         u.String(),
		Status:      status,
		ContentType: contentType,
		Bytes:       len(body),
		Truncated:   truncated,
		Content:     string(body),
	}
	if opts.Raw || !strings.Contains(strings.ToLower(contentType), "html") {
		return page, nil
	}
	return extractPageMarkdown(page, string(body), u), nil
}

func FetchFirecrawl(ctx context.Context, client HTTPClient, target string, cfg FetchConfig, opts FetchOptions) (*FetchPage, error) {
	u, err := validateHTTPURL(target)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultFetchMaxBytes
	}
	if maxBytes > maxFetchBytes {
		maxBytes = maxFetchBytes
	}
	apiKey := providerAPIKey(cfg.APIKey, cfg.APIKeyEnv, "FIRECRAWL_API_KEY", "MODU_FIRECRAWL_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("firecrawl api key is required; set FIRECRAWL_API_KEY or settings.webFetch.apiKeyEnv")
	}
	endpoint := strings.TrimSpace(firstNonEmpty(cfg.Endpoint, os.Getenv("MODU_FIRECRAWL_SCRAPE_ENDPOINT")))
	if endpoint == "" {
		endpoint = defaultFirecrawlScrapeEndpoint
	}
	payload := map[string]any{
		"url":             u.String(),
		"formats":         []any{"markdown"},
		"onlyMainContent": true,
	}
	body, status, contentType, truncated, err := postJSONWithHeaders(ctx, client, endpoint, payload, maxBytes, map[string]string{
		"Authorization": "Bearer " + apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("firecrawl scrape failed: %w", err)
	}
	if statusCode(status) >= 400 {
		return nil, fmt.Errorf("firecrawl scrape failed: %s: %s", status, common.PreviewText(string(body), common.TextPreviewOptions{MaxLines: 3, MaxBytes: 512}).Text)
	}
	var resp firecrawlScrapeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("firecrawl scrape returned invalid JSON: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("firecrawl scrape failed: %s", firstNonEmpty(resp.Error, resp.Code, "unknown error"))
	}
	content := resp.Data.Markdown
	if opts.Raw {
		content = string(body)
	}
	page := &FetchPage{
		URL:          firstNonEmpty(resp.Data.Metadata.SourceURL, resp.Data.Metadata.URL, u.String()),
		Status:       status,
		ContentType:  firstNonEmpty(resp.Data.Metadata.ContentType, contentType),
		Bytes:        len(body),
		Truncated:    truncated,
		RenderedHTML: true,
		Extracted:    true,
		Title:        resp.Data.Metadata.Title,
		Description:  resp.Data.Metadata.Description,
		Content:      content,
	}
	if page.ContentType == "" {
		page.ContentType = "text/markdown; charset=utf-8"
	}
	return page, nil
}

type firecrawlScrapeResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Markdown string `json:"markdown"`
		Metadata struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			SourceURL   string `json:"sourceURL"`
			URL         string `json:"url"`
			ContentType string `json:"contentType"`
		} `json:"metadata"`
	} `json:"data"`
	Code  string `json:"code"`
	Error string `json:"error"`
}

func extractPageMarkdown(page *FetchPage, doc string, u *url.URL) *FetchPage {
	extracted, err := extractMarkdown(doc, u)
	if err != nil {
		page.Content = visibleHTMLText(doc)
		page.Title = htmlTitle(doc)
		page.RenderedHTML = true
		return page
	}
	page.Content = extracted.Markdown
	page.RenderedHTML = true
	page.Extracted = extracted.Extracted
	page.Title = extracted.Title
	page.Author = extracted.Author
	page.Date = extracted.Date
	page.SiteName = extracted.SiteName
	page.Description = extracted.Description
	return page
}

func (p *FetchPage) JSON() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

type extractedMarkdown struct {
	Markdown    string
	Extracted   bool
	Title       string
	Author      string
	Date        string
	SiteName    string
	Description string
}

func extractMarkdown(doc string, baseURL *url.URL) (extractedMarkdown, error) {
	opts := trafilatura.Options{
		OriginalURL:     baseURL,
		EnableFallback:  true,
		ExcludeComments: true,
		IncludeLinks:    true,
		HtmlDateMode:    trafilatura.Disabled,
		Config: &trafilatura.Config{
			CacheSize:             trafilatura.DefaultConfig().CacheSize,
			MaxDuplicateCount:     trafilatura.DefaultConfig().MaxDuplicateCount,
			MinDuplicateCheckSize: trafilatura.DefaultConfig().MinDuplicateCheckSize,
			MinExtractedSize:      0,
			MinOutputSize:         1,
		},
	}
	result, err := trafilatura.Extract(strings.NewReader(doc), opts)
	if err != nil {
		markdown, convErr := markdownFromHTML(doc, baseURL)
		if convErr != nil {
			return extractedMarkdown{}, err
		}
		return extractedMarkdown{
			Markdown:  strings.TrimSpace(markdown),
			Extracted: false,
			Title:     htmlTitle(doc),
		}, nil
	}

	var rendered bytes.Buffer
	if result.ContentNode != nil {
		if err := nethtml.Render(&rendered, result.ContentNode); err != nil {
			return extractedMarkdown{}, err
		}
	}
	markdown, err := markdownFromHTML(rendered.String(), baseURL)
	if err != nil {
		return extractedMarkdown{}, err
	}
	if strings.TrimSpace(markdown) == "" {
		markdown = result.ContentText
	}

	meta := result.Metadata
	date := ""
	if !meta.Date.IsZero() {
		date = meta.Date.Format(time.RFC3339)
	}
	return extractedMarkdown{
		Markdown:    strings.TrimSpace(markdown),
		Extracted:   true,
		Title:       firstNonEmpty(meta.Title, htmlTitle(doc)),
		Author:      meta.Author,
		Date:        date,
		SiteName:    meta.Sitename,
		Description: meta.Description,
	}, nil
}

func markdownFromHTML(doc string, baseURL *url.URL) (string, error) {
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(
				table.WithHeaderPromotion(true),
			),
		),
	)
	domain := ""
	if baseURL != nil {
		domain = baseURL.String()
	}
	out, err := conv.ConvertString(doc, converter.WithDomain(domain))
	if err != nil {
		return "", err
	}
	return out, nil
}

func renderJavaScriptHTML(ctx context.Context, target string, wait time.Duration) (string, error) {
	l := launcher.New().Context(ctx).Headless(true).NoSandbox(true)
	controlURL, err := l.Launch()
	if err != nil {
		return "", err
	}
	defer l.Cleanup()

	browser := rod.New().Context(ctx).ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return "", err
	}
	defer browser.Close()

	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return "", err
	}
	defer page.Close()

	if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent:      defaultFetchUserAgent,
		AcceptLanguage: "zh-CN,zh;q=0.9,en;q=0.8",
		Platform:       "MacIntel",
	}); err != nil {
		return "", err
	}
	u, _ := url.Parse(target)
	if u != nil && strings.EqualFold(u.Host, "mp.weixin.qq.com") {
		if _, err := page.SetExtraHeaders([]string{"Referer", "https://mp.weixin.qq.com/"}); err != nil {
			return "", err
		}
	}
	if err := page.Navigate(target); err != nil {
		return "", err
	}
	_ = page.WaitLoad()
	if wait > 0 {
		time.Sleep(wait)
	}
	_ = page.WaitStable(500 * time.Millisecond)
	return page.HTML()
}

func formatFetchText(page *FetchPage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\nStatus: %s\nContent-Type: %s\n", page.URL, page.Status, page.ContentType)
	if page.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", page.Title)
	}
	if page.Author != "" {
		fmt.Fprintf(&b, "Author: %s\n", page.Author)
	}
	if page.Date != "" {
		fmt.Fprintf(&b, "Date: %s\n", page.Date)
	}
	fmt.Fprintln(&b)
	b.WriteString(page.Content)
	if page.Truncated {
		fmt.Fprintf(&b, "\n\n... (truncated after %d bytes)", page.Bytes)
	}
	return b.String()
}

func fetchDetails(page *FetchPage, output any) map[string]any {
	if page == nil {
		return map[string]any{"output": output}
	}
	return map[string]any{
		"url":           page.URL,
		"status":        page.Status,
		"content_type":  page.ContentType,
		"bytes":         page.Bytes,
		"truncated":     page.Truncated,
		"rendered_html": page.RenderedHTML,
		"extracted":     page.Extracted,
		"title":         page.Title,
		"author":        page.Author,
		"date":          page.Date,
		"site_name":     page.SiteName,
		"description":   page.Description,
		"output":        output,
	}
}

type SearchTool struct {
	client     HTTPClient
	endpoint   string
	provider   string
	apiKey     string
	searchType string
}

type SearchConfig struct {
	Provider   string
	Endpoint   string
	APIKey     string
	APIKeyEnv  string
	SearchType string
}

func NewSearchTool() types.Tool {
	return NewSearchToolWithConfig(SearchConfig{})
}

func NewSearchToolWithConfig(cfg SearchConfig) types.Tool {
	provider := strings.ToLower(strings.TrimSpace(firstNonEmpty(cfg.Provider, os.Getenv("MODU_WEB_SEARCH_PROVIDER"))))
	apiKey := providerAPIKey(cfg.APIKey, cfg.APIKeyEnv, "MODU_EXA_API_KEY", "EXA_API_KEY")
	if provider == "exa" || (provider == "" && apiKey != "") {
		endpoint := strings.TrimSpace(firstNonEmpty(cfg.Endpoint, os.Getenv("MODU_EXA_SEARCH_ENDPOINT")))
		if endpoint == "" {
			endpoint = defaultExaEndpoint
		}
		searchType := strings.TrimSpace(firstNonEmpty(cfg.SearchType, os.Getenv("MODU_EXA_SEARCH_TYPE")))
		if searchType == "" {
			searchType = "fast"
		}
		return newExaSearchTool(endpoint, apiKey, searchType)
	}
	switch provider {
	case "tavily":
		return newProviderSearchTool("tavily", firstNonEmpty(cfg.Endpoint, os.Getenv("MODU_TAVILY_SEARCH_ENDPOINT"), defaultTavilyEndpoint), providerAPIKey(cfg.APIKey, cfg.APIKeyEnv, "TAVILY_API_KEY", "MODU_TAVILY_API_KEY"), firstNonEmpty(cfg.SearchType, os.Getenv("MODU_TAVILY_SEARCH_DEPTH"), "basic"))
	case "brave":
		return newProviderSearchTool("brave", firstNonEmpty(cfg.Endpoint, os.Getenv("MODU_BRAVE_SEARCH_ENDPOINT"), defaultBraveEndpoint), providerAPIKey(cfg.APIKey, cfg.APIKeyEnv, "BRAVE_SEARCH_API_KEY", "MODU_BRAVE_SEARCH_API_KEY"), cfg.SearchType)
	case "firecrawl":
		return newProviderSearchTool("firecrawl", firstNonEmpty(cfg.Endpoint, os.Getenv("MODU_FIRECRAWL_SEARCH_ENDPOINT"), defaultFirecrawlSearchEndpoint), providerAPIKey(cfg.APIKey, cfg.APIKeyEnv, "FIRECRAWL_API_KEY", "MODU_FIRECRAWL_API_KEY"), cfg.SearchType)
	}
	endpoint := strings.TrimSpace(firstNonEmpty(cfg.Endpoint, os.Getenv("MODU_WEB_SEARCH_ENDPOINT")))
	if endpoint == "" {
		endpoint = defaultSearchEndpoint
	}
	return NewSearchToolWithEndpoint(endpoint)
}

func NewSearchToolWithEndpoint(endpoint string) types.Tool {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = defaultSearchEndpoint
	}
	return &SearchTool{
		client:   &http.Client{Timeout: 15 * time.Second},
		endpoint: endpoint,
		provider: "html",
	}
}

func NewExaSearchToolWithEndpoint(endpoint, apiKey string) types.Tool {
	return newExaSearchTool(endpoint, apiKey, firstNonEmpty(os.Getenv("MODU_EXA_SEARCH_TYPE"), "fast"))
}

func newExaSearchTool(endpoint, apiKey, searchType string) types.Tool {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = defaultExaEndpoint
	}
	if strings.TrimSpace(searchType) == "" {
		searchType = "fast"
	}
	return &SearchTool{
		client:     &http.Client{Timeout: 15 * time.Second},
		endpoint:   endpoint,
		provider:   "exa",
		apiKey:     strings.TrimSpace(apiKey),
		searchType: strings.TrimSpace(searchType),
	}
}

func newProviderSearchTool(provider, endpoint, apiKey, searchType string) types.Tool {
	return &SearchTool{
		client:     &http.Client{Timeout: 15 * time.Second},
		endpoint:   strings.TrimSpace(endpoint),
		provider:   provider,
		apiKey:     strings.TrimSpace(apiKey),
		searchType: strings.TrimSpace(searchType),
	}
}

func (t *SearchTool) Name() string  { return "web_search" }
func (t *SearchTool) Label() string { return "Web Search" }
func (t *SearchTool) Description() string {
	return "Search the web and return result titles, URLs, and snippets. Use this before web_fetch when researching current or external information."
}
func (t *SearchTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum results to return, capped at 10. Defaults to 5.",
			},
		},
		"required": []string{"query"},
	}
}
func (t *SearchTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return common.ErrorResult("query is required"), nil
	}
	maxResults := boundedInt(args["max_results"], defaultSearchResults, maxSearchResults)
	switch t.provider {
	case "exa":
		return t.executeExa(ctx, query, maxResults)
	case "tavily":
		return t.executeTavily(ctx, query, maxResults)
	case "brave":
		return t.executeBrave(ctx, query, maxResults)
	case "firecrawl":
		return t.executeFirecrawlSearch(ctx, query, maxResults)
	}
	searchURL, err := buildSearchURL(t.endpoint, query)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("invalid search endpoint: %v", err)), nil
	}
	body, status, contentType, truncated, err := fetch(ctx, t.client, searchURL, defaultSearchMaxBytes)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}
	if statusCode(status) >= 400 {
		return common.ErrorResult(fmt.Sprintf("search failed: %s", status)), nil
	}
	results := parseSearchResults(string(body), searchURL, maxResults)
	if len(results) == 0 {
		return textResult(fmt.Sprintf("No search results found for %q.", query), map[string]any{
			"query":        query,
			"status":       status,
			"content_type": contentType,
			"truncated":    truncated,
			"results":      []map[string]string{},
		}), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Search results for %q:\n", query)
	for i, result := range results {
		fmt.Fprintf(&b, "\n%d. %s\nURL: %s\n", i+1, result.Title, result.URL)
		if result.Snippet != "" {
			fmt.Fprintf(&b, "Snippet: %s\n", result.Snippet)
		}
	}
	if truncated {
		fmt.Fprintf(&b, "\n... (search response truncated after %d bytes)", defaultSearchMaxBytes)
	}
	details := make([]map[string]string, 0, len(results))
	for _, result := range results {
		details = append(details, map[string]string{
			"title":   result.Title,
			"url":     result.URL,
			"snippet": result.Snippet,
		})
	}
	return textResult(b.String(), map[string]any{
		"provider":     "html",
		"query":        query,
		"status":       status,
		"content_type": contentType,
		"truncated":    truncated,
		"results":      details,
	}), nil
}

type searchResult struct {
	Title         string
	URL           string
	Snippet       string
	PublishedDate string
	Author        string
}

func (t *SearchTool) executeExa(ctx context.Context, query string, maxResults int) (types.ToolResult, error) {
	if strings.TrimSpace(t.apiKey) == "" {
		return common.ErrorResult("exa api key is required; set MODU_EXA_API_KEY or EXA_API_KEY"), nil
	}
	searchType := strings.TrimSpace(t.searchType)
	if searchType == "" {
		searchType = "fast"
	}
	payload := map[string]any{
		"query":      query,
		"numResults": maxResults,
		"type":       searchType,
		"contents": map[string]any{
			"highlights": true,
		},
	}
	body, status, contentType, truncated, err := postJSON(ctx, t.client, t.endpoint, t.apiKey, payload, defaultSearchMaxBytes)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}
	if statusCode(status) >= 400 {
		return common.ErrorResult(fmt.Sprintf("search failed: %s: %s", status, common.PreviewText(string(body), common.TextPreviewOptions{MaxLines: 3, MaxBytes: 512}).Text)), nil
	}
	resp, err := parseExaSearchResponse(body, maxResults)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("search failed: invalid exa response: %v", err)), nil
	}
	if len(resp.Results) == 0 {
		return textResult(fmt.Sprintf("No search results found for %q.", query), map[string]any{
			"provider":     "exa",
			"query":        query,
			"status":       status,
			"content_type": contentType,
			"truncated":    truncated,
			"request_id":   resp.RequestID,
			"search_type":  searchType,
			"exa_type":     resp.ResolvedType,
			"cost_dollars": resp.CostDollars,
			"results":      []map[string]string{},
		}), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Search results for %q:\nProvider: exa\n", query)
	for i, result := range resp.Results {
		fmt.Fprintf(&b, "\n%d. %s\nURL: %s\n", i+1, result.Title, result.URL)
		if result.PublishedDate != "" {
			fmt.Fprintf(&b, "Published: %s\n", result.PublishedDate)
		}
		if result.Author != "" {
			fmt.Fprintf(&b, "Author: %s\n", result.Author)
		}
		if result.Snippet != "" {
			fmt.Fprintf(&b, "Snippet: %s\n", result.Snippet)
		}
	}
	if truncated {
		fmt.Fprintf(&b, "\n... (search response truncated after %d bytes)", defaultSearchMaxBytes)
	}
	details := make([]map[string]string, 0, len(resp.Results))
	for _, result := range resp.Results {
		details = append(details, map[string]string{
			"title":          result.Title,
			"url":            result.URL,
			"snippet":        result.Snippet,
			"published_date": result.PublishedDate,
			"author":         result.Author,
		})
	}
	return textResult(b.String(), map[string]any{
		"provider":     "exa",
		"query":        query,
		"status":       status,
		"content_type": contentType,
		"truncated":    truncated,
		"request_id":   resp.RequestID,
		"search_type":  searchType,
		"exa_type":     resp.ResolvedType,
		"cost_dollars": resp.CostDollars,
		"results":      details,
	}), nil
}

type exaSearchResponse struct {
	Results      []exaSearchResult `json:"results"`
	RequestID    string            `json:"requestId"`
	ResolvedType string            `json:"resolvedSearchType"`
	CostDollars  map[string]any    `json:"costDollars"`
}

type exaSearchResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	PublishedDate string   `json:"publishedDate"`
	Author        string   `json:"author"`
	Text          string   `json:"text"`
	Summary       string   `json:"summary"`
	Highlights    []string `json:"highlights"`
	Snippet       string
}

func parseExaSearchResponse(body []byte, maxResults int) (exaSearchResponse, error) {
	var resp exaSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return exaSearchResponse{}, err
	}
	out := make([]exaSearchResult, 0, len(resp.Results))
	seen := map[string]bool{}
	for _, result := range resp.Results {
		if len(out) >= maxResults {
			break
		}
		result.Title = normalizeSpace(result.Title)
		result.URL = strings.TrimSpace(result.URL)
		if result.Title == "" || result.URL == "" || seen[result.URL] {
			continue
		}
		seen[result.URL] = true
		result.Snippet = exaSnippet(result)
		out = append(out, result)
	}
	resp.Results = out
	return resp, nil
}

func exaSnippet(result exaSearchResult) string {
	for _, highlight := range result.Highlights {
		if snippet := common.TruncateLine(normalizeSpace(highlight), 360); snippet != "" {
			return snippet
		}
	}
	if snippet := common.TruncateLine(normalizeSpace(result.Summary), 360); snippet != "" {
		return snippet
	}
	return common.TruncateLine(normalizeSpace(result.Text), 360)
}

func (t *SearchTool) executeTavily(ctx context.Context, query string, maxResults int) (types.ToolResult, error) {
	if strings.TrimSpace(t.apiKey) == "" {
		return common.ErrorResult("tavily api key is required; set TAVILY_API_KEY or settings.webSearch.apiKeyEnv"), nil
	}
	searchDepth := firstNonEmpty(t.searchType, "basic")
	payload := map[string]any{
		"query":        query,
		"max_results":  maxResults,
		"search_depth": searchDepth,
	}
	body, status, contentType, truncated, err := postJSONWithHeaders(ctx, t.client, t.endpoint, payload, defaultSearchMaxBytes, map[string]string{
		"Authorization": "Bearer " + t.apiKey,
	})
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}
	if statusCode(status) >= 400 {
		return common.ErrorResult(fmt.Sprintf("search failed: %s: %s", status, common.PreviewText(string(body), common.TextPreviewOptions{MaxLines: 3, MaxBytes: 512}).Text)), nil
	}
	var resp tavilySearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return common.ErrorResult(fmt.Sprintf("search failed: invalid tavily response: %v", err)), nil
	}
	results := make([]searchResult, 0, len(resp.Results))
	seen := map[string]bool{}
	for _, result := range resp.Results {
		if len(results) >= maxResults {
			break
		}
		title := normalizeSpace(result.Title)
		u := strings.TrimSpace(result.URL)
		if title == "" || u == "" || seen[u] {
			continue
		}
		seen[u] = true
		results = append(results, searchResult{
			Title:         title,
			URL:           u,
			Snippet:       common.TruncateLine(normalizeSpace(result.Content), 360),
			PublishedDate: result.PublishedDate,
		})
	}
	return formatSearchResults("tavily", query, status, contentType, truncated, results, map[string]any{
		"request_id":    resp.RequestID,
		"response_time": resp.ResponseTime,
		"search_depth":  searchDepth,
		"usage":         resp.Usage,
	}), nil
}

type tavilySearchResponse struct {
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Content       string `json:"content"`
		PublishedDate string `json:"published_date"`
	} `json:"results"`
	ResponseTime any            `json:"response_time"`
	RequestID    string         `json:"request_id"`
	Usage        map[string]any `json:"usage"`
}

func (t *SearchTool) executeBrave(ctx context.Context, query string, maxResults int) (types.ToolResult, error) {
	if strings.TrimSpace(t.apiKey) == "" {
		return common.ErrorResult("brave api key is required; set BRAVE_SEARCH_API_KEY or settings.webSearch.apiKeyEnv"), nil
	}
	searchURL, err := buildBraveSearchURL(t.endpoint, query, maxResults)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("invalid brave search endpoint: %v", err)), nil
	}
	body, status, contentType, truncated, err := fetchWithHeaders(ctx, t.client, searchURL, defaultSearchMaxBytes, map[string]string{
		"Accept":               "application/json",
		"X-Subscription-Token": t.apiKey,
	})
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}
	if statusCode(status) >= 400 {
		return common.ErrorResult(fmt.Sprintf("search failed: %s: %s", status, common.PreviewText(string(body), common.TextPreviewOptions{MaxLines: 3, MaxBytes: 512}).Text)), nil
	}
	var resp braveSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return common.ErrorResult(fmt.Sprintf("search failed: invalid brave response: %v", err)), nil
	}
	results := make([]searchResult, 0, len(resp.Web.Results))
	seen := map[string]bool{}
	for _, result := range resp.Web.Results {
		if len(results) >= maxResults {
			break
		}
		title := normalizeSpace(result.Title)
		u := strings.TrimSpace(result.URL)
		if title == "" || u == "" || seen[u] {
			continue
		}
		seen[u] = true
		snippet := result.Description
		if snippet == "" && len(result.ExtraSnippets) > 0 {
			snippet = result.ExtraSnippets[0]
		}
		results = append(results, searchResult{
			Title:         title,
			URL:           u,
			Snippet:       common.TruncateLine(normalizeSpace(snippet), 360),
			PublishedDate: result.Age,
		})
	}
	return formatSearchResults("brave", query, status, contentType, truncated, results, nil), nil
}

type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			Description   string   `json:"description"`
			Age           string   `json:"age"`
			ExtraSnippets []string `json:"extra_snippets"`
		} `json:"results"`
	} `json:"web"`
}

func (t *SearchTool) executeFirecrawlSearch(ctx context.Context, query string, maxResults int) (types.ToolResult, error) {
	if strings.TrimSpace(t.apiKey) == "" {
		return common.ErrorResult("firecrawl api key is required; set FIRECRAWL_API_KEY or settings.webSearch.apiKeyEnv"), nil
	}
	payload := map[string]any{
		"query":   query,
		"limit":   maxResults,
		"sources": []any{"web"},
	}
	body, status, contentType, truncated, err := postJSONWithHeaders(ctx, t.client, t.endpoint, payload, defaultSearchMaxBytes, map[string]string{
		"Authorization": "Bearer " + t.apiKey,
	})
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}
	if statusCode(status) >= 400 {
		return common.ErrorResult(fmt.Sprintf("search failed: %s: %s", status, common.PreviewText(string(body), common.TextPreviewOptions{MaxLines: 3, MaxBytes: 512}).Text)), nil
	}
	var resp firecrawlSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return common.ErrorResult(fmt.Sprintf("search failed: invalid firecrawl response: %v", err)), nil
	}
	if !resp.Success && resp.Error != "" {
		return common.ErrorResult("search failed: " + resp.Error), nil
	}
	results := make([]searchResult, 0, len(resp.Data))
	seen := map[string]bool{}
	for _, result := range resp.Data {
		if len(results) >= maxResults {
			break
		}
		u := firstNonEmpty(result.URL, result.Metadata.SourceURL, result.Metadata.URL)
		title := firstNonEmpty(result.Title, result.Metadata.Title)
		if title == "" || u == "" || seen[u] {
			continue
		}
		seen[u] = true
		results = append(results, searchResult{
			Title:   normalizeSpace(title),
			URL:     strings.TrimSpace(u),
			Snippet: common.TruncateLine(normalizeSpace(firstNonEmpty(result.Description, result.Markdown, result.Metadata.Description)), 360),
		})
	}
	return formatSearchResults("firecrawl", query, status, contentType, truncated, results, map[string]any{
		"id":           resp.ID,
		"warning":      resp.Warning,
		"credits_used": resp.CreditsUsed,
	}), nil
}

type firecrawlSearchResponse struct {
	Success bool `json:"success"`
	Data    []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Markdown    string `json:"markdown"`
		Metadata    struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			SourceURL   string `json:"sourceURL"`
			URL         string `json:"url"`
		} `json:"metadata"`
	} `json:"data"`
	ID          string `json:"id"`
	Warning     string `json:"warning"`
	CreditsUsed int    `json:"creditsUsed"`
	Error       string `json:"error"`
}

func formatSearchResults(provider, query, status, contentType string, truncated bool, results []searchResult, extra map[string]any) types.ToolResult {
	details := map[string]any{
		"provider":     provider,
		"query":        query,
		"status":       status,
		"content_type": contentType,
		"truncated":    truncated,
	}
	if extra != nil {
		for k, v := range extra {
			if detailValuePresent(v) {
				details[k] = v
			}
		}
	}
	if len(results) == 0 {
		details["results"] = []map[string]string{}
		return textResult(fmt.Sprintf("No search results found for %q.", query), details)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Search results for %q:\n", query)
	if provider != "html" {
		fmt.Fprintf(&b, "Provider: %s\n", provider)
	}
	resultDetails := make([]map[string]string, 0, len(results))
	for i, result := range results {
		fmt.Fprintf(&b, "\n%d. %s\nURL: %s\n", i+1, result.Title, result.URL)
		if result.PublishedDate != "" {
			fmt.Fprintf(&b, "Published: %s\n", result.PublishedDate)
		}
		if result.Author != "" {
			fmt.Fprintf(&b, "Author: %s\n", result.Author)
		}
		if result.Snippet != "" {
			fmt.Fprintf(&b, "Snippet: %s\n", result.Snippet)
		}
		resultDetails = append(resultDetails, map[string]string{
			"title":          result.Title,
			"url":            result.URL,
			"snippet":        result.Snippet,
			"published_date": result.PublishedDate,
			"author":         result.Author,
		})
	}
	if truncated {
		fmt.Fprintf(&b, "\n... (search response truncated after %d bytes)", defaultSearchMaxBytes)
	}
	details["results"] = resultDetails
	return textResult(b.String(), details)
}

func detailValuePresent(v any) bool {
	if v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	return true
}

func validateHTTPURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("url must use http or https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("url host is required")
	}
	return u, nil
}

func fetch(ctx context.Context, client HTTPClient, target string, maxBytes int) ([]byte, string, string, bool, error) {
	return fetchWithHeaders(ctx, client, target, maxBytes, nil)
}

func fetchWithHeaders(ctx context.Context, client HTTPClient, target string, maxBytes int, headers map[string]string) ([]byte, string, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", "", false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain,application/json;q=0.8,*/*;q=0.7")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	for k, v := range headers {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	if req.URL != nil && strings.EqualFold(req.URL.Host, "mp.weixin.qq.com") {
		req.Header.Set("Referer", "https://mp.weixin.qq.com/")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", false, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes+1)))
	if err != nil {
		return nil, "", "", false, err
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return data, resp.Status, resp.Header.Get("Content-Type"), truncated, nil
}

func postJSON(ctx context.Context, client HTTPClient, target, apiKey string, payload any, maxBytes int) ([]byte, string, string, bool, error) {
	return postJSONWithHeaders(ctx, client, target, payload, maxBytes, map[string]string{"x-api-key": apiKey})
}

func postJSONWithHeaders(ctx context.Context, client HTTPClient, target string, payload any, maxBytes int, headers map[string]string) ([]byte, string, string, bool, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, "", "", false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return nil, "", "", false, err
	}
	req.Header.Set("User-Agent", defaultFetchUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes+1)))
	if err != nil {
		return nil, "", "", false, err
	}
	truncated := len(body) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}
	return body, resp.Status, resp.Header.Get("Content-Type"), truncated, nil
}

func buildBraveSearchURL(endpoint, query string, maxResults int) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", strconv.Itoa(maxResults))
	q.Set("result_filter", "web")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func buildSearchURL(endpoint, query string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if strings.Contains(endpoint, "{query}") {
		return strings.ReplaceAll(endpoint, "{query}", url.QueryEscape(query)), nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("q", query)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func parseSearchResults(doc, baseURL string, maxResults int) []searchResult {
	root, err := nethtml.Parse(strings.NewReader(doc))
	if err != nil {
		return nil
	}
	base, _ := url.Parse(baseURL)
	results := make([]searchResult, 0, maxResults)
	seen := map[string]bool{}
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if len(results) >= maxResults {
			return
		}
		if n.Type == nethtml.ElementNode && n.Data == "a" {
			href := attr(n, "href")
			title := normalizeSpace(nodeText(n))
			resolved := normalizeResultURL(href, base)
			if title != "" && resolved != "" && !seen[resolved] {
				seen[resolved] = true
				results = append(results, searchResult{
					Title:   title,
					URL:     resolved,
					Snippet: nearbySnippet(n),
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return results
}

func normalizeResultURL(raw string, base *url.URL) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(strings.ToLower(raw), "javascript:") {
		return ""
	}
	u, err := url.Parse(stdhtml.UnescapeString(raw))
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if strings.Contains(u.Host, "duckduckgo.com") && strings.HasPrefix(u.Path, "/l/") {
		if target := u.Query().Get("uddg"); target != "" {
			if decoded, err := url.QueryUnescape(target); err == nil {
				u, err = url.Parse(decoded)
				if err != nil {
					return ""
				}
			}
		}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func nearbySnippet(n *nethtml.Node) string {
	for p := n.Parent; p != nil; p = p.Parent {
		text := normalizeSpace(nodeText(p))
		title := normalizeSpace(nodeText(n))
		text = strings.TrimSpace(strings.Replace(text, title, "", 1))
		if len(text) > 20 {
			if len(text) > 240 {
				text = text[:240] + "..."
			}
			return text
		}
	}
	return ""
}

func visibleHTMLText(doc string) string {
	root, err := nethtml.Parse(strings.NewReader(doc))
	if err != nil {
		return normalizeSpace(stripTagsFallback(doc))
	}
	return normalizeSpace(nodeText(root))
}

func htmlTitle(doc string) string {
	root, err := nethtml.Parse(strings.NewReader(doc))
	if err != nil {
		return ""
	}
	var title string
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if title != "" {
			return
		}
		if n.Type == nethtml.ElementNode && n.Data == "title" {
			title = normalizeSpace(nodeText(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return title
}

func nodeText(n *nethtml.Node) string {
	if n.Type == nethtml.ElementNode {
		switch n.Data {
		case "script", "style", "noscript", "svg":
			return ""
		}
	}
	if n.Type == nethtml.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text := nodeText(c)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text)
	}
	return b.String()
}

func attr(n *nethtml.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func normalizeSpace(s string) string {
	s = stdhtml.UnescapeString(s)
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func envValue(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return os.Getenv(name)
}

func providerAPIKey(value, envName string, fallbackEnvNames ...string) string {
	values := []string{value, envValue(envName)}
	for _, name := range fallbackEnvNames {
		values = append(values, os.Getenv(name))
	}
	return strings.TrimSpace(firstNonEmpty(values...))
}

func stripTagsFallback(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
			b.WriteByte(' ')
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func boundedInt(v any, fallback, max int) int {
	n := common.ToInt(v)
	if n <= 0 {
		n = fallback
	}
	if n > max {
		n = max
	}
	return n
}

func boundedDurationMS(v any, fallback, max time.Duration) time.Duration {
	n := common.ToInt(v)
	if n <= 0 {
		return fallback
	}
	return boundedDuration(time.Duration(n)*time.Millisecond, fallback, max)
}

func boundedDuration(v time.Duration, fallback, max time.Duration) time.Duration {
	if v <= 0 {
		v = fallback
	}
	if v > max {
		v = max
	}
	return v
}

func statusCode(status string) int {
	parts := strings.Fields(status)
	if len(parts) == 0 {
		return 0
	}
	code, _ := strconv.Atoi(parts[0])
	return code
}

func textResult(text string, details any) types.ToolResult {
	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
		Details: details,
	}
}
