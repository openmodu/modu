package notebooklm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openmodu/modu/repos/notebooklm/rpc"
	vo "github.com/openmodu/modu/vo/notebooklm_vo"
)

// ========== Source Operations ==========

// ListSources returns all sources in a notebook
func (c *Client) ListSources(ctx context.Context, notebookID string) ([]vo.Source, error) {
	params := []any{notebookID, nil, []any{2}, nil, 0}
	result, err := c.rpcCall(ctx, vo.RPCGetNotebook, params, "/notebook/"+notebookID)
	if err != nil {
		return nil, err
	}

	return parseSourceList(result, notebookID)
}

// AddSourceURL adds a URL source to a notebook
// Automatically detects YouTube URLs and uses the appropriate method
func (c *Client) AddSourceURL(ctx context.Context, notebookID, sourceURL string) (*vo.Source, error) {
	var params []any

	if isYouTubeURL(sourceURL) {
		// YouTube format: URL at position 7, with extra params
		// [[[None, None, None, None, None, None, None, [url], None, None, 1]], notebook_id, [2], [1, None, None, None, None, None, None, None, None, None, [1]]]
		params = []any{
			[]any{[]any{nil, nil, nil, nil, nil, nil, nil, []any{sourceURL}, nil, nil, 1}},
			notebookID,
			[]any{2},
			[]any{1, nil, nil, nil, nil, nil, nil, nil, nil, nil, []any{1}},
		}
	} else {
		// Regular URL format: URL at position 2
		// [[[None, None, [url], None, None, None, None, None]], notebook_id, [2], None, None]
		params = []any{
			[]any{[]any{nil, nil, []any{sourceURL}, nil, nil, nil, nil, nil}},
			notebookID,
			[]any{2},
			nil,
			nil,
		}
	}

	result, err := c.rpcCall(ctx, vo.RPCAddSource, params, "/notebook/"+notebookID)
	if err != nil {
		return nil, err
	}

	source, err := parseSourceFromAdd(result, notebookID)
	if err != nil {
		return nil, err
	}

	// Set URL and type from input since response may not include it
	if source.URL == "" {
		source.URL = sourceURL
	}
	if isYouTubeURL(sourceURL) {
		source.SourceType = "youtube"
	}

	return source, nil
}

// isYouTubeURL checks if URL is a YouTube video link
func isYouTubeURL(url string) bool {
	return strings.Contains(url, "youtube.com/watch") ||
		strings.Contains(url, "youtu.be/") ||
		strings.Contains(url, "youtube.com/shorts/")
}

// AddSourceFile adds a local file as a source to a notebook
// Uses Google's resumable upload protocol
func (c *Client) AddSourceFile(ctx context.Context, notebookID, filePath string) (*vo.Source, error) {
	// Check file exists
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}
	if fileInfo.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file")
	}

	filename := filepath.Base(filePath)
	fileSize := fileInfo.Size()

	// Ensure we have tokens
	if c.auth.CSRFToken == "" {
		if err := c.RefreshTokens(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh tokens: %w", err)
		}
	}

	// Step 1: Register source intent → get SOURCE_ID
	sourceID, err := c.registerFileSource(ctx, notebookID, filename)
	if err != nil {
		return nil, fmt.Errorf("failed to register file source: %w", err)
	}

	// Step 2: Start resumable upload → get upload URL
	uploadURL, err := c.startResumableUpload(ctx, notebookID, filename, fileSize, sourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to start upload: %w", err)
	}

	// Step 3: Upload file content
	if err := c.uploadFile(ctx, uploadURL, filePath); err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	return &vo.Source{
		ID:         sourceID,
		NotebookID: notebookID,
		Title:      filename,
		SourceType: "file",
		Status:     "processing",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

// registerFileSource registers a file source intent and gets SOURCE_ID
func (c *Client) registerFileSource(ctx context.Context, notebookID, filename string) (string, error) {
	params := []any{
		[]any{[]any{filename}},
		notebookID,
		[]any{2},
		[]any{1, nil, nil, nil, nil, nil, nil, nil, nil, nil, []any{1}},
	}

	result, err := c.rpcCall(ctx, vo.RPCAddSourceFile, params, "/notebook/"+notebookID)
	if err != nil {
		return "", err
	}

	// Extract SOURCE_ID from nested response
	sourceID := extractNestedString(result)
	if sourceID == "" {
		return "", fmt.Errorf("failed to get source ID from response")
	}

	return sourceID, nil
}

// startResumableUpload starts a resumable upload and returns the upload URL
func (c *Client) startResumableUpload(ctx context.Context, notebookID, filename string, fileSize int64, sourceID string) (string, error) {
	uploadURL := rpc.UploadURL + "?authuser=0"

	body := fmt.Sprintf(`{"PROJECT_ID":"%s","SOURCE_NAME":"%s","SOURCE_ID":"%s"}`,
		notebookID, filename, sourceID)

	req, err := c.newRequest(ctx, "POST", uploadURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Cookie", c.auth.CookieHeader())
	req.Header.Set("Origin", "https://notebooklm.google.com")
	req.Header.Set("Referer", "https://notebooklm.google.com/")
	req.Header.Set("x-goog-authuser", "0")
	req.Header.Set("x-goog-upload-command", "start")
	req.Header.Set("x-goog-upload-header-content-length", fmt.Sprintf("%d", fileSize))
	req.Header.Set("x-goog-upload-protocol", "resumable")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("upload start failed with status %d", resp.StatusCode)
	}

	resultURL := resp.Header.Get("x-goog-upload-url")
	if resultURL == "" {
		return "", fmt.Errorf("no upload URL in response headers")
	}

	return resultURL, nil
}

// uploadFile uploads file content to the resumable upload URL
func (c *Client) uploadFile(ctx context.Context, uploadURL, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	req, err := c.newRequest(ctx, "POST", uploadURL, file)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("Cookie", c.auth.CookieHeader())
	req.Header.Set("Origin", "https://notebooklm.google.com")
	req.Header.Set("Referer", "https://notebooklm.google.com/")
	req.Header.Set("x-goog-authuser", "0")
	req.Header.Set("x-goog-upload-command", "upload, finalize")
	req.Header.Set("x-goog-upload-offset", "0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := readLimitedBody(resp.Body, 200)
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, body)
	}

	return nil
}

// AddSourceText adds a text source to a notebook
func (c *Client) AddSourceText(ctx context.Context, notebookID, title, content string) (*vo.Source, error) {
	// Python format: [[[None, [title, content], None, None, None, None, None, None]], notebook_id, [2], None, None]
	params := []any{
		[]any{[]any{nil, []any{title, content}, nil, nil, nil, nil, nil, nil}},
		notebookID,
		[]any{2},
		nil,
		nil,
	}
	result, err := c.rpcCall(ctx, vo.RPCAddSource, params, "/notebook/"+notebookID)
	if err != nil {
		return nil, err
	}

	return parseSourceFromAdd(result, notebookID)
}

// DeleteSource deletes a source from a notebook
func (c *Client) DeleteSource(ctx context.Context, notebookID, sourceID string) error {
	// Python format: [[[source_id]]]
	params := []any{[]any{[]any{sourceID}}}
	_, err := c.rpcCall(ctx, vo.RPCDeleteSource, params, "/notebook/"+notebookID)
	return err
}
