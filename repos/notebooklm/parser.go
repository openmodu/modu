package notebooklm

import (
	"fmt"
	"time"

	vo "github.com/openmodu/modu/vo/notebooklm_vo"
)

// parseNotebookList parses the list notebooks response
func parseNotebookList(data any) ([]vo.Notebook, error) {
	arr, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid notebook list response")
	}

	if len(arr) == 0 {
		return []vo.Notebook{}, nil
	}

	// Notebooks are in the first element
	nbList, ok := arr[0].([]any)
	if !ok {
		return []vo.Notebook{}, nil
	}

	var notebooks []vo.Notebook
	for _, item := range nbList {
		nb, err := parseNotebook(item)
		if err != nil {
			continue // Skip malformed entries
		}
		notebooks = append(notebooks, *nb)
	}

	return notebooks, nil
}

// parseNotebook parses a single notebook from API response
func parseNotebook(data any) (*vo.Notebook, error) {
	arr, ok := data.([]any)
	if !ok || len(arr) < 3 {
		return nil, fmt.Errorf("invalid notebook data")
	}

	nb := &vo.Notebook{}

	// Find ID (UUID format) and Title (human readable)
	// The response structure has ID and Title in first few positions
	// but order may vary - ID is always UUID format
	for i := 0; i < len(arr) && i < 5; i++ {
		if str, ok := arr[i].(string); ok && str != "" {
			if isUUID(str) {
				nb.ID = str
			} else if nb.Title == "" {
				nb.Title = str
			}
		}
	}

	// Parse timestamps if available
	nb.CreatedAt = time.Now()
	nb.UpdatedAt = time.Now()

	// Count sources if available
	if len(arr) > 1 {
		if sources, ok := arr[1].([]any); ok {
			nb.SourceCount = len(sources)
		}
	}

	return nb, nil
}

// parseSourceList parses all sources from notebook response
func parseSourceList(data any, notebookID string) ([]vo.Source, error) {
	arr, ok := data.([]any)
	if !ok || len(arr) == 0 {
		return nil, fmt.Errorf("invalid notebook response")
	}

	// Notebook data is in first element
	nbData, ok := arr[0].([]any)
	if !ok || len(nbData) < 2 {
		return []vo.Source{}, nil
	}

	// Sources are in second element
	sourcesList, ok := nbData[1].([]any)
	if !ok {
		return []vo.Source{}, nil
	}

	var sources []vo.Source
	for _, src := range sourcesList {
		srcArr, ok := src.([]any)
		if !ok || len(srcArr) < 2 {
			continue
		}

		source := vo.Source{
			NotebookID: notebookID,
			Status:     "ready",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		// Extract ID from src[0] - can be nested: [id] or [[id]]
		if idData, ok := srcArr[0].([]any); ok && len(idData) > 0 {
			if id, ok := idData[0].(string); ok {
				source.ID = id
			} else if nestedID, ok := idData[0].([]any); ok && len(nestedID) > 0 {
				if id, ok := nestedID[0].(string); ok {
					source.ID = id
				}
			}
		}

		// Extract title from src[1]
		if title, ok := srcArr[1].(string); ok {
			source.Title = title
		}

		// Extract URL from src[2][7] if present
		if len(srcArr) > 2 {
			if meta, ok := srcArr[2].([]any); ok && len(meta) > 7 {
				if urlArr, ok := meta[7].([]any); ok && len(urlArr) > 0 {
					if url, ok := urlArr[0].(string); ok {
						source.URL = url
					}
				}
			}
		}

		// Extract status from src[3][1]
		// Status: 1=processing, 2=ready, 3=error
		if len(srcArr) > 3 {
			if statusArr, ok := srcArr[3].([]any); ok && len(statusArr) > 1 {
				if statusCode, ok := statusArr[1].(float64); ok {
					switch int(statusCode) {
					case 1:
						source.Status = "processing"
					case 2:
						source.Status = "ready"
					case 3:
						source.Status = "error"
					}
				}
			}
		}

		// Determine source type
		if source.URL != "" {
			source.SourceType = "url"
			if containsYouTube(source.URL) {
				source.SourceType = "youtube"
			}
		} else if source.Title != "" {
			// Check file extension
			title := source.Title
			if len(title) > 4 {
				ext := title[len(title)-4:]
				switch ext {
				case ".pdf":
					source.SourceType = "pdf"
				case ".txt", ".csv":
					source.SourceType = "file"
				}
			}
			if source.SourceType == "" {
				source.SourceType = "text"
			}
		}

		if source.ID != "" {
			sources = append(sources, source)
		}
	}

	return sources, nil
}

// parseSource parses a source from API response (for list operations)
func parseSource(data any, notebookID string) (*vo.Source, error) {
	arr, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid source data")
	}

	source := &vo.Source{
		NotebookID: notebookID,
		Status:     "processing",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// Source structure varies, try to extract key fields
	if len(arr) > 0 {
		// First element often contains source info array
		if sourceArr, ok := arr[0].([]any); ok && len(sourceArr) > 0 {
			// ID is often triple-nested
			if idArr, ok := sourceArr[0].([]any); ok && len(idArr) > 0 {
				if idArr2, ok := idArr[0].([]any); ok && len(idArr2) > 0 {
					if id, ok := idArr2[0].(string); ok {
						source.ID = id
					}
				} else if id, ok := idArr[0].(string); ok {
					source.ID = id
				}
			}

			// Title is often at index 1
			if len(sourceArr) > 1 {
				if title, ok := sourceArr[1].(string); ok {
					source.Title = title
				}
			}

			// Look for URL in metadata
			if len(sourceArr) > 2 {
				if meta, ok := sourceArr[2].([]any); ok {
					source.URL = findURLInMeta(meta)
				}
			}
		}
	}

	// Determine source type from URL
	if source.URL != "" {
		source.SourceType = "url"
		if containsYouTube(source.URL) {
			source.SourceType = "youtube"
		}
	} else {
		source.SourceType = "text"
	}

	return source, nil
}

// parseSourceFromAdd parses source from add_source API response
// Structure: [[[[id], title, metadata, ...]]] (deeply nested)
func parseSourceFromAdd(data any, notebookID string) (*vo.Source, error) {
	source := &vo.Source{
		NotebookID: notebookID,
		Status:     "processing",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// Navigate the nested structure
	arr, ok := data.([]any)
	if !ok || len(arr) == 0 {
		return nil, fmt.Errorf("invalid source response: not an array")
	}

	// First level: [[[[id], title, ...]]]
	level1, ok := arr[0].([]any)
	if !ok || len(level1) == 0 {
		return nil, fmt.Errorf("invalid source response: level 1")
	}

	// Second level: [[[id], title, ...]]
	level2, ok := level1[0].([]any)
	if !ok || len(level2) == 0 {
		return nil, fmt.Errorf("invalid source response: level 2")
	}

	// Entry level: [[id], title, metadata...]
	// Could be at level2 directly or one more level
	var entry []any

	// Check if level2[0] is an array containing the ID
	if idArr, ok := level2[0].([]any); ok {
		// level2 is the entry: [[id], title, ...]
		entry = level2
		// Extract ID from nested array
		if len(idArr) > 0 {
			if id, ok := idArr[0].(string); ok {
				source.ID = id
			}
		}
	} else if innerArr, ok := level2[0].([]any); ok && len(innerArr) > 0 {
		// One more level: [[[id], title, ...]]
		entry = innerArr
		if idArr, ok := entry[0].([]any); ok && len(idArr) > 0 {
			if id, ok := idArr[0].(string); ok {
				source.ID = id
			}
		}
	}

	if entry == nil {
		entry = level2
	}

	// Extract title from entry[1]
	if len(entry) > 1 {
		if title, ok := entry[1].(string); ok {
			source.Title = title
		}
	}

	// Extract URL from entry[2][7] if present
	if len(entry) > 2 {
		if meta, ok := entry[2].([]any); ok && len(meta) > 7 {
			if urlArr, ok := meta[7].([]any); ok && len(urlArr) > 0 {
				if url, ok := urlArr[0].(string); ok {
					source.URL = url
				}
			}
		}
	}

	// Determine source type based on URL or title
	if source.URL != "" {
		source.SourceType = "url"
		if containsYouTube(source.URL) {
			source.SourceType = "youtube"
		}
	} else if source.Title != "" && (contains(source.Title, "YouTube") || contains(source.Title, "youtube")) {
		// YouTube sources may not have URL in immediate response
		source.SourceType = "youtube"
	} else {
		source.SourceType = "text"
	}

	// If still no ID found, try recursive extraction
	if source.ID == "" {
		source.ID = extractNestedString(data)
	}

	if source.ID == "" {
		return nil, fmt.Errorf("could not extract source ID from response")
	}

	return source, nil
}

// extractNestedString recursively finds the first UUID string
func extractNestedString(data any) string {
	switch v := data.(type) {
	case string:
		if isUUID(v) {
			return v
		}
	case []any:
		for _, item := range v {
			if id := extractNestedString(item); id != "" {
				return id
			}
		}
	}
	return ""
}

// findURLInMeta searches for URL in metadata array
func findURLInMeta(meta []any) string {
	for _, item := range meta {
		if urlArr, ok := item.([]any); ok && len(urlArr) > 0 {
			if url, ok := urlArr[0].(string); ok {
				if isURL(url) {
					return url
				}
			}
		}
	}
	return ""
}

// isURL checks if a string looks like a URL
func isURL(s string) bool {
	return len(s) > 8 && (s[:7] == "http://" || s[:8] == "https://")
}

// isUUID checks if a string looks like a UUID
func isUUID(s string) bool {
	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (36 chars with 4 dashes)
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

// containsYouTube checks if URL is a YouTube link
func containsYouTube(url string) bool {
	return len(url) > 0 && (contains(url, "youtube.com") || contains(url, "youtu.be"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr) >= 0
}

func findSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// parseGenerationStatus parses artifact generation status
// Response format: [[artifact_id, ..., ..., ..., status_code, ...], ...]
func parseGenerationStatus(data any) (*vo.GenerationStatus, error) {
	arr, ok := data.([]any)
	if !ok || len(arr) == 0 {
		return nil, fmt.Errorf("invalid generation status data")
	}

	// Response is nested: arr[0] contains the artifact data
	artifactData, ok := arr[0].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid artifact data structure")
	}

	status := &vo.GenerationStatus{
		Status: "pending",
	}

	// TaskID is at artifactData[0]
	if len(artifactData) > 0 {
		if taskID, ok := artifactData[0].(string); ok {
			status.TaskID = taskID
		}
	}

	// Status code is at artifactData[4]
	if len(artifactData) > 4 {
		if statusCode, ok := artifactData[4].(float64); ok {
			switch int(statusCode) {
			case 1:
				status.Status = "in_progress"
			case 2:
				status.Status = "pending"
			case 3:
				status.Status = "completed"
			}
		}
	}

	return status, nil
}

// parsePollStatus parses the poll studio response
// Response format: [?, status_string, url, error]
func parsePollStatus(data any, taskID string) (*vo.GenerationStatus, error) {
	status := &vo.GenerationStatus{
		TaskID: taskID,
		Status: "pending",
	}

	// Handle nil response - artifact might not be ready yet
	if data == nil {
		return status, nil
	}

	arr, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid generation status data")
	}

	// Status is at index 1
	if len(arr) > 1 {
		if statusStr, ok := arr[1].(string); ok {
			status.Status = statusStr
		}
	}

	// URL is at index 2
	if len(arr) > 2 {
		if url, ok := arr[2].(string); ok && url != "" {
			status.DownloadURL = url
		}
	}

	// Error is at index 3
	if len(arr) > 3 {
		if errStr, ok := arr[3].(string); ok && errStr != "" {
			status.Error = errStr
		}
	}

	return status, nil
}

// parseArtifactList parses the list artifacts response
// Response is nested: result[0] contains the actual artifact list
func parseArtifactList(data any) ([]vo.Artifact, error) {
	arr, ok := data.([]any)
	if !ok || len(arr) == 0 {
		return nil, nil // No artifacts
	}

	// Response is nested: result[0] is the artifact list
	artifactList, ok := arr[0].([]any)
	if !ok {
		// If not nested, try using arr directly
		artifactList = arr
	}

	var artifacts []vo.Artifact
	for _, item := range artifactList {
		artifact, err := parseArtifact(item)
		if err != nil {
			continue
		}
		artifacts = append(artifacts, *artifact)
	}

	return artifacts, nil
}

// parseArtifact parses a single artifact
// Structure: [id, title, type, ?, status, ?, metadata, ...]
// metadata[5] contains media URLs: [[url, ?, mime_type], ...]
func parseArtifact(data any) (*vo.Artifact, error) {
	arr, ok := data.([]any)
	if !ok || len(arr) < 2 {
		return nil, fmt.Errorf("invalid artifact data")
	}

	artifact := &vo.Artifact{
		Status:    "pending",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// ID at index 0
	if len(arr) > 0 {
		if id, ok := arr[0].(string); ok {
			artifact.ID = id
		}
	}

	// Title at index 1
	if len(arr) > 1 {
		if title, ok := arr[1].(string); ok {
			artifact.Title = title
		}
	}

	// Type at index 2
	if len(arr) > 2 {
		if artType, ok := arr[2].(float64); ok {
			artifact.ArtifactType = int(artType)
		}
	}

	// Status at index 4 (1=processing, 2=pending, 3=completed)
	if len(arr) > 4 {
		if statusCode, ok := arr[4].(float64); ok {
			switch int(statusCode) {
			case 1:
				artifact.Status = "in_progress"
			case 2:
				artifact.Status = "pending"
			case 3:
				artifact.Status = "completed"
			}
		}
	}

	// Extract download URL from metadata[6][5]
	if len(arr) > 6 {
		artifact.DownloadURL = extractMediaURL(arr[6])
	}

	return artifact, nil
}

// extractMediaURL extracts the download URL from artifact metadata
// Structure: metadata[5] = [[url, ?, mime_type], ...]
func extractMediaURL(metadata any) string {
	metaArr, ok := metadata.([]any)
	if !ok || len(metaArr) <= 5 {
		return ""
	}

	mediaList, ok := metaArr[5].([]any)
	if !ok || len(mediaList) == 0 {
		return ""
	}

	// Try to find audio/mp4 or video/mp4 first
	for _, item := range mediaList {
		itemArr, ok := item.([]any)
		if !ok || len(itemArr) < 3 {
			continue
		}
		if url, ok := itemArr[0].(string); ok {
			if mimeType, ok := itemArr[2].(string); ok {
				if mimeType == "audio/mp4" || mimeType == "video/mp4" {
					return url
				}
			}
		}
	}

	// Fallback: use first URL
	if len(mediaList) > 0 {
		if firstItem, ok := mediaList[0].([]any); ok && len(firstItem) > 0 {
			if url, ok := firstItem[0].(string); ok {
				return url
			}
		}
	}

	return ""
}
