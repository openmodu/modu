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
	defaultFetchMaxBytes  = 2 * 1024 * 1024
	maxFetchBytes         = 32 * 1024 * 1024
	defaultSearchEndpoint = "https://duckduckgo.com/html/?q={query}"
	defaultSearchMaxBytes = 128 * 1024
	defaultSearchResults  = 5
	maxSearchResults      = 10
	defaultJSWait         = 2 * time.Second
	maxJSWait             = 15 * time.Second
	defaultFetchUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type FetchTool struct {
	client HTTPClient
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
	page, err := Fetch(ctx, t.client, target, FetchOptions{
		MaxBytes: boundedInt(args["max_bytes"], defaultFetchMaxBytes, maxFetchBytes),
		Raw:      raw,
		JSRender: jsRender,
		JSWait:   boundedDurationMS(args["js_wait_ms"], defaultJSWait, maxJSWait),
	})
	if err != nil {
		return common.ErrorResult(err.Error()), nil
	}
	return textResult(formatFetchText(page), page), nil
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

type SearchTool struct {
	client   HTTPClient
	endpoint string
}

func NewSearchTool() types.Tool {
	endpoint := strings.TrimSpace(os.Getenv("MODU_WEB_SEARCH_ENDPOINT"))
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
		"query":        query,
		"status":       status,
		"content_type": contentType,
		"truncated":    truncated,
		"results":      details,
	}), nil
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", "", false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain,application/json;q=0.8,*/*;q=0.7")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
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
