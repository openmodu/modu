package notebooklm

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"

	vo "github.com/openmodu/modu/vo/notebooklm_vo"
)

// ========== Utility Functions ==========

// getSourceIDs extracts source IDs from a notebook
func (c *Client) getSourceIDs(ctx context.Context, notebookID string) ([]string, error) {
	params := []any{notebookID, nil, []any{2}, nil, 0}
	result, err := c.rpcCall(ctx, vo.RPCGetNotebook, params, "/notebook/"+notebookID)
	if err != nil {
		return nil, err
	}

	return extractSourceIDs(result), nil
}

// extractSourceIDs extracts source IDs from notebook response
func extractSourceIDs(data any) []string {
	var ids []string

	arr, ok := data.([]any)
	if !ok || len(arr) == 0 {
		return ids
	}

	// Notebook data is in first element
	nbData, ok := arr[0].([]any)
	if !ok || len(nbData) < 2 {
		return ids
	}

	// Sources are in second element
	sources, ok := nbData[1].([]any)
	if !ok {
		return ids
	}

	for _, source := range sources {
		sourceArr, ok := source.([]any)
		if !ok || len(sourceArr) == 0 {
			continue
		}

		// Source ID is nested: source[0][0][0] or source[0][0]
		id := extractNestedID(sourceArr[0])
		if id != "" {
			ids = append(ids, id)
		}
	}

	return ids
}

// extractNestedID extracts ID from nested structure
func extractNestedID(data any) string {
	switch v := data.(type) {
	case string:
		if isUUIDFormat(v) {
			return v
		}
	case []any:
		for _, item := range v {
			if id := extractNestedID(item); id != "" {
				return id
			}
		}
	}
	return ""
}

// isUUIDFormat checks if string is UUID format
func isUUIDFormat(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// generateUUID generates a new UUID v4
func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	// Set version 4
	b[6] = (b[6] & 0x0f) | 0x40
	// Set variant
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// readLimitedBody reads up to n bytes from the body
func readLimitedBody(body io.Reader, n int) (string, error) {
	buf := make([]byte, n)
	read, err := body.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return string(buf[:read]), nil
}

// readAllBody reads the entire body
func readAllBody(body io.Reader) ([]byte, error) {
	return io.ReadAll(body)
}
