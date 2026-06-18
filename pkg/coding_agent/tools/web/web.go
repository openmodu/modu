package webtools

import (
	"context"
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

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
	nethtml "golang.org/x/net/html"
)

const (
	defaultFetchMaxBytes  = 50 * 1024
	maxFetchBytes         = 256 * 1024
	defaultSearchEndpoint = "https://duckduckgo.com/html/?q={query}"
	defaultSearchMaxBytes = 128 * 1024
	defaultSearchResults  = 5
	maxSearchResults      = 10
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type FetchTool struct {
	client HTTPClient
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
				"description": "Maximum response bytes to read, capped at 262144. Defaults to 51200.",
			},
			"raw": map[string]any{
				"type":        "boolean",
				"description": "Return raw response text instead of extracting visible HTML text.",
			},
		},
		"required": []string{"url"},
	}
}
func (t *FetchTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	target, _ := args["url"].(string)
	u, err := validateHTTPURL(target)
	if err != nil {
		return common.ErrorResult(err.Error()), nil
	}
	maxBytes := boundedInt(args["max_bytes"], defaultFetchMaxBytes, maxFetchBytes)
	body, status, contentType, truncated, err := fetch(ctx, t.client, u.String(), maxBytes)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("fetch failed: %v", err)), nil
	}
	raw, _ := args["raw"].(bool)
	text := string(body)
	rendered := false
	title := ""
	if !raw && strings.Contains(strings.ToLower(contentType), "html") {
		title = htmlTitle(text)
		text = visibleHTMLText(text)
		rendered = true
	}
	if truncated {
		text += fmt.Sprintf("\n\n... (truncated after %d bytes)", maxBytes)
	}
	prefix := fmt.Sprintf("URL: %s\nStatus: %s\nContent-Type: %s\n\n", u.String(), status, contentType)
	return textResult(prefix+text, map[string]any{
		"url":           u.String(),
		"status":        status,
		"content_type":  contentType,
		"bytes":         len(body),
		"truncated":     truncated,
		"rendered_html": rendered,
		"title":         title,
	}), nil
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
	req.Header.Set("User-Agent", "modu-code/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml,text/plain,application/json;q=0.9,*/*;q=0.8")
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
