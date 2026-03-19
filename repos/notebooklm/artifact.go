package notebooklm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	vo "github.com/openmodu/modu/vo/notebooklm_vo"
	"golang.org/x/net/publicsuffix"
)

// ========== Artifact Operations ==========

// GenerateAudio generates an audio podcast from notebook sources
func (c *Client) GenerateAudio(ctx context.Context, notebookID string, format vo.AudioFormat, length vo.AudioLength) (*vo.GenerationStatus, error) {
	// Get source IDs first
	sourceIDs, err := c.getSourceIDs(ctx, notebookID)
	if err != nil {
		return nil, fmt.Errorf("failed to get source IDs: %w", err)
	}

	if len(sourceIDs) == 0 {
		return nil, fmt.Errorf("notebook has no sources to generate audio from")
	}

	// Build source arrays
	// source_ids_triple: [[[sid]] for sid in source_ids]
	sourceIDsTriple := make([]any, len(sourceIDs))
	for i, sid := range sourceIDs {
		sourceIDsTriple[i] = []any{[]any{sid}}
	}

	// source_ids_double: [[sid] for sid in source_ids]
	sourceIDsDouble := make([]any, len(sourceIDs))
	for i, sid := range sourceIDs {
		sourceIDsDouble[i] = []any{sid}
	}

	// Python format:
	// [[2], notebook_id, [None, None, 1, source_ids_triple, None, None, [None, [instructions, length, None, source_ids_double, "en", None, format]]]]
	var formatCode, lengthCode any
	if format != 0 {
		formatCode = int(format)
	}
	if length != 0 {
		lengthCode = int(length)
	}

	params := []any{
		[]any{2},
		notebookID,
		[]any{
			nil,
			nil,
			1, // StudioContentType.AUDIO
			sourceIDsTriple,
			nil,
			nil,
			[]any{
				nil,
				[]any{
					nil, // instructions
					lengthCode,
					nil,
					sourceIDsDouble,
					"en", // language
					nil,
					formatCode,
				},
			},
		},
	}

	// Use RPCCreateVideo for all artifact generation (same as Python implementation)
	result, err := c.rpcCall(ctx, vo.RPCCreateVideo, params, "/notebook/"+notebookID)
	if err != nil {
		return nil, err
	}

	return parseGenerationStatus(result)
}

// GenerateVideo generates a video
func (c *Client) GenerateVideo(ctx context.Context, notebookID string, format vo.VideoFormat, style vo.VideoStyle) (*vo.GenerationStatus, error) {
	params := []any{notebookID, []any{int(format), int(style)}, []any{2}}
	result, err := c.rpcCall(ctx, vo.RPCCreateVideo, params, "/notebook/"+notebookID)
	if err != nil {
		return nil, err
	}

	return parseGenerationStatus(result)
}

// PollGeneration checks the status of artifact generation
func (c *Client) PollGeneration(ctx context.Context, notebookID, taskID string) (*vo.GenerationStatus, error) {
	// Note: parameter order is [taskID, notebookID, [2]] - same as Python
	params := []any{taskID, notebookID, []any{2}}
	result, err := c.rpcCall(ctx, vo.RPCPollStudio, params, "/notebook/"+notebookID)
	if err != nil {
		return nil, err
	}

	return parsePollStatus(result, taskID)
}

// ListArtifacts lists all artifacts in a notebook
func (c *Client) ListArtifacts(ctx context.Context, notebookID string) ([]vo.Artifact, error) {
	// Python format: [[2], notebook_id, 'NOT artifact.status = "ARTIFACT_STATUS_SUGGESTED"']
	params := []any{[]any{2}, notebookID, `NOT artifact.status = "ARTIFACT_STATUS_SUGGESTED"`}
	result, err := c.rpcCall(ctx, vo.RPCListArtifacts, params, "/notebook/"+notebookID)
	if err != nil {
		return nil, err
	}

	return parseArtifactList(result)
}

// DownloadAudio downloads a completed audio artifact to a file
func (c *Client) DownloadAudio(ctx context.Context, notebookID, outputPath string, artifactID string) (string, error) {
	artifacts, err := c.ListArtifacts(ctx, notebookID)
	if err != nil {
		return "", fmt.Errorf("failed to list artifacts: %w", err)
	}

	// Find audio artifact (type 1 = audio)
	var audioArtifact *vo.Artifact
	for i := range artifacts {
		a := &artifacts[i]
		if a.ArtifactType == 1 && a.Status == "completed" {
			if artifactID == "" || a.ID == artifactID {
				audioArtifact = a
				break
			}
		}
	}

	if audioArtifact == nil {
		return "", fmt.Errorf("no completed audio artifact found")
	}

	if audioArtifact.DownloadURL == "" {
		return "", fmt.Errorf("audio artifact has no download URL")
	}

	// Download the file
	if err := c.downloadFile(ctx, audioArtifact.DownloadURL, outputPath); err != nil {
		return "", fmt.Errorf("failed to download audio: %w", err)
	}

	return outputPath, nil
}

// DownloadVideo downloads a completed video artifact to a file
func (c *Client) DownloadVideo(ctx context.Context, notebookID, outputPath string, artifactID string) (string, error) {
	artifacts, err := c.ListArtifacts(ctx, notebookID)
	if err != nil {
		return "", fmt.Errorf("failed to list artifacts: %w", err)
	}

	// Find video artifact (type 3 = video)
	var videoArtifact *vo.Artifact
	for i := range artifacts {
		a := &artifacts[i]
		if a.ArtifactType == 3 && a.Status == "completed" {
			if artifactID == "" || a.ID == artifactID {
				videoArtifact = a
				break
			}
		}
	}

	if videoArtifact == nil {
		return "", fmt.Errorf("no completed video artifact found")
	}

	if videoArtifact.DownloadURL == "" {
		return "", fmt.Errorf("video artifact has no download URL")
	}

	// Download the file
	if err := c.downloadFile(ctx, videoArtifact.DownloadURL, outputPath); err != nil {
		return "", fmt.Errorf("failed to download video: %w", err)
	}

	return outputPath, nil
}

// downloadFile downloads a file from URL to local path
func (c *Client) downloadFile(ctx context.Context, downloadURL, outputPath string) error {
	// Create a cookie jar with proper domain handling
	jar, err := cookiejar.New(&cookiejar.Options{
		PublicSuffixList: publicsuffix.List,
	})
	if err != nil {
		return fmt.Errorf("failed to create cookie jar: %w", err)
	}

	// Add cookies to the jar with proper domain scoping
	// This is critical for cross-domain redirects to work correctly
	for _, cookie := range c.auth.CookiesWithDomain {
		domain := cookie.Domain
		// Determine the URL scheme based on domain
		scheme := "https"
		host := strings.TrimPrefix(domain, ".")
		if host == "" {
			host = "google.com"
		}

		cookieURL, _ := url.Parse(fmt.Sprintf("%s://%s/", scheme, host))
		jar.SetCookies(cookieURL, []*http.Cookie{{
			Name:   cookie.Name,
			Value:  cookie.Value,
			Domain: domain,
			Path:   "/",
		}})
	}

	// Create a custom client with cookie jar
	downloadClient := &http.Client{
		Timeout: 120 * time.Second,
		Jar:     jar,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := downloadClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/html") {
		return fmt.Errorf("received HTML instead of media file (authentication may have failed)")
	}

	// Create output file
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
