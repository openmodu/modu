package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	vo "github.com/openmodu/modu/vo/notebooklm_vo"
)

var (
	ErrNoResult      = errors.New("no result found for RPC ID")
	ErrRPCError      = errors.New("RPC error")
	ErrAuthError     = errors.New("authentication error")
	ErrRateLimited   = errors.New("rate limited")
	ErrInvalidFormat = errors.New("invalid response format")
)

// DecodeResponse parses the batchexecute response
func DecodeResponse(response string, rpcID vo.RPCMethod) (any, error) {
	// Strip anti-XSSI prefix
	cleaned := stripAntiXSSI(response)

	// Parse chunked response
	chunks, err := parseChunkedResponse(cleaned)
	if err != nil {
		return nil, fmt.Errorf("failed to parse chunked response: %w", err)
	}

	// Extract result for the RPC ID
	return extractRPCResult(chunks, string(rpcID))
}

// stripAntiXSSI removes Google's anti-XSSI prefix
func stripAntiXSSI(response string) string {
	// Google prefixes with )]}'\n or similar
	prefixes := []string{")]}'", ")]}'\n"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(response, prefix) {
			return strings.TrimPrefix(response, prefix)
		}
	}
	return response
}

// parseChunkedResponse parses the alternating byte-count/json format
func parseChunkedResponse(response string) ([]any, error) {
	var chunks []any
	lines := strings.Split(strings.TrimSpace(response), "\n")

	i := 0
	for i < len(lines) {
		// Skip empty lines
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}

		// Try to parse as byte count (integer line)
		_, err := strconv.Atoi(strings.TrimSpace(lines[i]))
		if err != nil {
			// Not a byte count, try parsing as JSON directly
			var chunk any
			if err := json.Unmarshal([]byte(lines[i]), &chunk); err == nil {
				chunks = append(chunks, chunk)
			}
			i++
			continue
		}

		// Next line should be JSON payload
		i++
		if i >= len(lines) {
			break
		}

		jsonLine := lines[i]
		var chunk any
		if err := json.Unmarshal([]byte(jsonLine), &chunk); err == nil {
			chunks = append(chunks, chunk)
		}
		i++
	}

	return chunks, nil
}

// extractRPCResult finds the result for a specific RPC ID
func extractRPCResult(chunks []any, rpcID string) (any, error) {
	var foundIDs []string

	for _, chunk := range chunks {
		arr, ok := chunk.([]any)
		if !ok {
			continue
		}

		for _, item := range arr {
			itemArr, ok := item.([]any)
			if !ok || len(itemArr) < 2 {
				continue
			}

			itemType, _ := itemArr[0].(string)
			itemID, _ := itemArr[1].(string)

			if itemID != "" {
				foundIDs = append(foundIDs, itemID)
			}

			// Check for error response
			if itemType == "er" && itemID == rpcID {
				errMsg := "RPC error"
				if len(itemArr) > 2 {
					errMsg = fmt.Sprintf("RPC error: %v", itemArr[2])
				}
				return nil, fmt.Errorf("%w: %s", ErrRPCError, errMsg)
			}

			// Check for success response
			if itemType == "wrb.fr" && itemID == rpcID {
				if len(itemArr) < 3 {
					return nil, nil
				}

				result := itemArr[2]

				// If result is a string, it's JSON that needs to be parsed again
				if strResult, ok := result.(string); ok {
					var parsed any
					if err := json.Unmarshal([]byte(strResult), &parsed); err == nil {
						return parsed, nil
					}
					return strResult, nil
				}

				// Check for UserDisplayableError (rate limiting)
				if len(itemArr) > 5 && itemArr[5] != nil {
					if containsUserDisplayableError(itemArr[5]) {
						return nil, ErrRateLimited
					}
				}

				return result, nil
			}
		}
	}

	return nil, fmt.Errorf("%w: %s (found IDs: %v)", ErrNoResult, rpcID, foundIDs)
}

// containsUserDisplayableError checks for rate limit errors
func containsUserDisplayableError(data any) bool {
	str := fmt.Sprintf("%v", data)
	return strings.Contains(str, "UserDisplayableError")
}

// IsAuthError checks if an error is authentication-related
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, ErrAuthError) {
		return true
	}

	errStr := strings.ToLower(err.Error())
	authKeywords := []string{
		"authentication", "unauthorized", "401", "403",
		"expired", "login", "re-authenticate",
	}

	for _, kw := range authKeywords {
		if strings.Contains(errStr, kw) {
			return true
		}
	}

	return false
}

// ParseChatResponse extracts the answer from chat streaming response
func ParseChatResponse(response string) (string, []any, error) {
	cleaned := stripAntiXSSI(response)
	chunks, err := parseChunkedResponse(cleaned)
	if err != nil {
		return "", nil, err
	}

	var longestAnswer string
	var references []any

	for _, chunk := range chunks {
		arr, ok := chunk.([]any)
		if !ok {
			continue
		}

		// Process each item in the chunk looking for wrb.fr response
		for _, item := range arr {
			itemArr, ok := item.([]any)
			if !ok || len(itemArr) < 3 {
				continue
			}

			// Check for wrb.fr marker
			marker, _ := itemArr[0].(string)
			if marker != "wrb.fr" {
				continue
			}

			// The answer JSON is in position 2
			innerJSON, ok := itemArr[2].(string)
			if !ok {
				continue
			}

			// Parse the nested JSON
			var innerData []any
			if err := json.Unmarshal([]byte(innerJSON), &innerData); err != nil {
				continue
			}

			// Extract answer from inner structure
			text := extractAnswerFromInner(innerData)
			if text != "" && len(text) > len(longestAnswer) {
				longestAnswer = text
			}
		}
	}

	if longestAnswer == "" {
		return "", nil, errors.New("no answer found in response")
	}

	return longestAnswer, references, nil
}

// extractAnswerFromInner extracts answer text from parsed inner JSON
// Structure: [[answer_text, null, [ids], null, [metadata]], ...]
func extractAnswerFromInner(data []any) string {
	if len(data) == 0 {
		return ""
	}

	// First element contains the answer structure
	first, ok := data[0].([]any)
	if !ok || len(first) == 0 {
		return ""
	}

	// Answer text is at position 0
	text, ok := first[0].(string)
	if !ok {
		return ""
	}

	return text
}
