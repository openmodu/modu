package scraper

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/playwright"
)

// ScrapePH scrapes Product Hunt front page
func ScrapePH(limit int) ([]NewsItem, error) {
	browser, err := playwright.New()
	if err != nil {
		return nil, err
	}
	defer browser.Close()

	page, err := browser.NewPage()
	if err != nil {
		return nil, err
	}
	defer page.Close()

	// Retry loading page up to 3 times
	var items []NewsItem
	var lastErr string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			// Wait before retry
			page.Wait(2 * time.Second)
		}

		if err := page.Goto("https://www.producthunt.com/", playwright.WithTimeout(30000)); err != nil {
			lastErr = fmt.Sprintf("goto failed: %v", err)
			continue
		}

		// Wait for Apollo SSR data script to appear (up to 8 seconds per attempt)
		var html string
		for i := 0; i < 8; i++ {
			page.Wait(1 * time.Second)

			html, err = page.Content()
			if err != nil {
				break
			}

			// Check if Apollo data is present with homefeed
			if strings.Contains(html, `"homefeed"`) {
				items, err = extractPHFromHTML(html, limit)
				if err != nil {
					lastErr = fmt.Sprintf("extract failed: %v", err)
					break
				}
				if len(items) > 0 {
					return items, nil
				}
			}
		}

		if len(html) < 50000 {
			lastErr = fmt.Sprintf("attempt %d: page too small (%d bytes), likely blocked", attempt+1, len(html))
		} else {
			lastErr = fmt.Sprintf("attempt %d: no homefeed data found", attempt+1)
		}

		// Reload page for next attempt
		page.Reload()
	}

	return nil, fmt.Errorf("failed after 3 attempts: %s", lastErr)
}

// extractPHFromHTML extracts Product Hunt items from HTML content
func extractPHFromHTML(html string, limit int) ([]NewsItem, error) {
	var items []NewsItem

	// Extract the embedded Apollo GraphQL data
	pattern := regexp.MustCompile(`window\[Symbol\.for\("ApolloSSRDataTransport"\)\].*?\.push\((.*?)\);?</script>`)
	matches := pattern.FindStringSubmatch(html)

	if len(matches) < 2 {
		// Apollo data not found yet, return empty (caller will retry)
		return items, nil
	}

	jsonStr := matches[1]
	// Replace JavaScript undefined with null
	jsonStr = regexp.MustCompile(`\bundefined\b`).ReplaceAllString(jsonStr, "null")

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil, fmt.Errorf("failed to parse Apollo JSON: %w", err)
	}

	rehydrate, ok := data["rehydrate"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Apollo data missing 'rehydrate' key")
	}

	for _, value := range rehydrate {
		valueMap, ok := value.(map[string]interface{})
		if !ok {
			continue
		}

		feedData, ok := valueMap["data"].(map[string]interface{})
		if !ok {
			continue
		}

		homefeed, ok := feedData["homefeed"].(map[string]interface{})
		if !ok {
			continue
		}

		edges, ok := homefeed["edges"].([]interface{})
		if !ok {
			continue
		}

		for _, edge := range edges {
			edgeMap, ok := edge.(map[string]interface{})
			if !ok {
				continue
			}

			node, ok := edgeMap["node"].(map[string]interface{})
			if !ok {
				continue
			}

			posts, ok := node["items"].([]interface{})
			if !ok {
				continue
			}

			for _, post := range posts {
				if len(items) >= limit {
					break
				}

				postMap, ok := post.(map[string]interface{})
				if !ok {
					continue
				}

				name, _ := postMap["name"].(string)
				tagline, _ := postMap["tagline"].(string)
				slug, _ := postMap["slug"].(string)

				if name == "" || slug == "" {
					continue
				}

				itemURL := fmt.Sprintf("https://www.producthunt.com/posts/%s", slug)

				var score, comments *int
				if s, ok := postMap["latestScore"].(float64); ok {
					scoreInt := int(s)
					score = &scoreInt
				}
				if c, ok := postMap["commentsCount"].(float64); ok {
					commentsInt := int(c)
					comments = &commentsInt
				}

				items = append(items, NewsItem{
					Title:    name,
					URL:      itemURL,
					Source:   "producthunt",
					Score:    score,
					Comments: comments,
					Tagline:  tagline,
				})
			}

			if len(items) > 0 {
				break
			}
		}

		if len(items) > 0 {
			break
		}
	}

	return items, nil
}
