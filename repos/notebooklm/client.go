package notebooklm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openmodu/modu/repos/notebooklm/rpc"
	vo "github.com/openmodu/modu/vo/notebooklm_vo"
)

const (
	defaultTimeout = 120 * time.Second
	maxRetries     = 3
	retryDelay     = 2 * time.Second
)

// Client is the NotebookLM API client
type Client struct {
	auth       *vo.AuthTokens
	httpClient *http.Client
	reqCounter int
}

// NewClient creates a new NotebookLM client
func NewClient(auth *vo.AuthTokens) *Client {
	transport := &http.Transport{
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}

	// Check for proxy from environment
	if proxyURL := os.Getenv("HTTPS_PROXY"); proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	} else if proxyURL := os.Getenv("HTTP_PROXY"); proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}

	return &Client{
		auth: auth,
		httpClient: &http.Client{
			Timeout:   defaultTimeout,
			Transport: transport,
		},
		reqCounter: 100000,
	}
}

// NewClientFromStorage creates a client from stored auth
func NewClientFromStorage(storagePath string) (*Client, error) {
	auth, err := LoadAuthTokens(storagePath)
	if err != nil {
		return nil, err
	}
	return NewClient(auth), nil
}

// RefreshTokens fetches fresh CSRF token and session ID from homepage
func (c *Client) RefreshTokens(ctx context.Context) error {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := c.doRefreshTokens(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if it's a network/timeout error worth retrying
		if isRetryableError(err) && attempt < maxRetries {
			time.Sleep(retryDelay * time.Duration(attempt))
			continue
		}

		break
	}

	return lastErr
}

// doRefreshTokens performs a single refresh attempt
func (c *Client) doRefreshTokens(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", rpc.BaseURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Cookie", c.auth.CookieHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch homepage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("homepage returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read homepage: %w", err)
	}

	html := string(body)

	csrf, err := ExtractCSRFToken(html)
	if err != nil {
		return err
	}

	sessionID, err := ExtractSessionID(html)
	if err != nil {
		return err
	}

	c.auth.CSRFToken = csrf
	c.auth.SessionID = sessionID

	return nil
}

// isRetryableError checks if error is worth retrying
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "TLS handshake") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "network is unreachable")
}

// rpcCall makes an RPC call to batchexecute with retry
func (c *Client) rpcCall(ctx context.Context, method vo.RPCMethod, params []any, sourcePath string) (any, error) {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		result, err := c.doRPCCall(ctx, method, params, sourcePath)
		if err == nil {
			return result, nil
		}

		lastErr = err

		if isRetryableError(err) && attempt < maxRetries {
			time.Sleep(retryDelay * time.Duration(attempt))
			continue
		}

		break
	}

	return nil, lastErr
}

// doRPCCall performs a single RPC call attempt
func (c *Client) doRPCCall(ctx context.Context, method vo.RPCMethod, params []any, sourcePath string) (any, error) {
	// Ensure we have tokens
	if c.auth.CSRFToken == "" {
		if err := c.RefreshTokens(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh tokens: %w", err)
		}
	}

	// Encode request
	rpcReq, err := rpc.EncodeRPCRequest(method, params)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	body, err := rpc.BuildRequestBody(rpcReq, c.auth.CSRFToken)
	if err != nil {
		return nil, fmt.Errorf("failed to build request body: %w", err)
	}

	// Build URL
	reqURL := rpc.BuildURL(method, c.auth.SessionID, sourcePath)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Cookie", c.auth.CookieHeader())

	// Execute
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check for auth errors
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: status %d", rpc.ErrAuthError, resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d", resp.StatusCode)
	}

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Decode response
	return rpc.DecodeResponse(string(respBody), method)
}

// newRequest creates a new HTTP request with context
func (c *Client) newRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, url, body)
}
