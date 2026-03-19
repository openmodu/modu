package notebooklm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openmodu/modu/repos/notebooklm/rpc"
	vo "github.com/openmodu/modu/vo/notebooklm_vo"
)

// ========== Chat Operations ==========

// Ask sends a question to the notebook
func (c *Client) Ask(ctx context.Context, notebookID, question string, sourceIDs []string) (*vo.AskResult, error) {
	// Ensure we have tokens
	if c.auth.CSRFToken == "" {
		if err := c.RefreshTokens(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh tokens: %w", err)
		}
	}

	// If no source IDs provided, get all sources from notebook
	if len(sourceIDs) == 0 {
		ids, err := c.getSourceIDs(ctx, notebookID)
		if err != nil {
			return nil, fmt.Errorf("failed to get source IDs: %w", err)
		}
		sourceIDs = ids
	}

	if len(sourceIDs) == 0 {
		return nil, fmt.Errorf("notebook has no sources to query")
	}

	// Generate new conversation ID
	conversationID := generateUUID()

	// Build chat request with CSRF token
	body, err := rpc.EncodeChatRequest(question, sourceIDs, conversationID, nil, c.auth.CSRFToken)
	if err != nil {
		return nil, fmt.Errorf("failed to encode chat request: %w", err)
	}

	// Build URL
	c.reqCounter += 100000
	reqURL := rpc.BuildChatURL(c.auth.SessionID, c.reqCounter)

	// Create request
	req, err := c.newRequest(ctx, "POST", reqURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Cookie", c.auth.CookieHeader())

	// Execute with retry
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := c.httpClient.Do(req)
		if err == nil {
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				respBody, _ := readLimitedBody(resp.Body, 200)
				return nil, fmt.Errorf("chat request failed with status %d: %s", resp.StatusCode, respBody)
			}

			// Read response
			respBody, err := readAllBody(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("failed to read response: %w", err)
			}

			// Parse chat response
			answer, _, err := rpc.ParseChatResponse(string(respBody))
			if err != nil {
				preview := string(respBody)
				if len(preview) > 500 {
					preview = preview[:500]
				}
				return nil, fmt.Errorf("failed to parse response: %w (response preview: %s)", err, preview)
			}

			return &vo.AskResult{
				Answer:         answer,
				ConversationID: conversationID,
				TurnNumber:     1,
			}, nil
		}

		lastErr = err
		if isRetryableError(err) && attempt < maxRetries {
			time.Sleep(retryDelay * time.Duration(attempt))
			// Recreate request for retry
			req, _ = c.newRequest(ctx, "POST", reqURL, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
			req.Header.Set("Cookie", c.auth.CookieHeader())
			continue
		}
		break
	}

	return nil, fmt.Errorf("chat request failed: %w", lastErr)
}
