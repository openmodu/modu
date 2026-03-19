package scraper

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/playwright"
	pw "github.com/playwright-community/playwright-go"
)

// ScrapeDouyinLive scrapes douyin live stream information
func ScrapeDouyinLive(roomURL string) (*DouyinLiveInfo, error) {
	// Extract room ID from URL
	roomID, err := extractRoomID(roomURL)
	if err != nil {
		return nil, fmt.Errorf("invalid room URL: %w", err)
	}

	browser, err := playwright.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create browser: %w", err)
	}
	defer browser.Close()

	page, err := browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("failed to create page: %w", err)
	}
	defer page.Close()

	rawPage := page.Raw()

	// Channel to capture stream URL from network requests
	streamURLChan := make(chan string, 1)

	// Listen for network requests to capture stream URL
	rawPage.OnRequest(func(request pw.Request) {
		url := request.URL()
		// Look for FLV or M3U8 stream URLs
		if strings.Contains(url, ".flv") || strings.Contains(url, ".m3u8") {
			select {
			case streamURLChan <- url:
			default:
			}
		}
	})

	// Navigate to room URL (anti-detection is enabled by default)
	if err := page.Goto(roomURL); err != nil {
		return nil, fmt.Errorf("failed to navigate to room: %w", err)
	}

	// Wait for the page to load and stream to start
	time.Sleep(5 * time.Second)

	info := &DouyinLiveInfo{
		RoomID: roomID,
		URL:    roomURL,
	}

	// Extract live information from page
	if err := extractLiveInfo(page, info); err != nil {
		return nil, fmt.Errorf("failed to extract live info: %w", err)
	}

	// Try to get stream URL from captured network requests
	select {
	case streamURL := <-streamURLChan:
		info.StreamURL = streamURL
	default:
		// If no stream URL captured from network, try alternative methods
		if info.IsLive {
			streamURL, err := extractStreamURL(page)
			if err == nil && streamURL != "" && !strings.HasPrefix(streamURL, "blob:") {
				info.StreamURL = streamURL
			}
		}
	}

	return info, nil
}

// extractRoomID extracts room ID from douyin live URL
func extractRoomID(roomURL string) (string, error) {
	// Pattern: https://live.douyin.com/123456789
	re := regexp.MustCompile(`live\.douyin\.com/(\d+)`)
	matches := re.FindStringSubmatch(roomURL)
	if len(matches) < 2 {
		return "", fmt.Errorf("invalid douyin live URL format")
	}
	return matches[1], nil
}

// extractLiveInfo extracts live information from the page
func extractLiveInfo(page *playwright.Page, info *DouyinLiveInfo) error {
	rawPage := page.Raw()

	// First, try to extract from window.__RENDER_DATA__ or similar global variables
	dataScript := `
		(() => {
			try {
				const findData = (obj, depth = 0) => {
					if (depth > 10 || !obj || typeof obj !== 'object') return null;

					const result = {};

					// Look for room info
					if (obj.roomInfo || obj.room_info || obj.room) {
						const room = obj.roomInfo || obj.room_info || obj.room;
						if (room.title) result.title = room.title;
						if (room.room_title) result.title = room.room_title;
						if (room.owner) {
							result.streamer = room.owner.nickname || room.owner.name || room.owner.nick_name;
						}
						if (room.user) {
							result.streamer = room.user.nickname || room.user.name || room.user.nick_name;
						}
						if (room.status !== undefined) {
							result.isLive = room.status === 2 || room.status === '2' || room.status === 'live';
						}
						if (room.stats) {
							result.viewerCount = room.stats.total_user || room.stats.user_count;
						}
						if (room.cover) {
							result.cover = room.cover.url_list?.[0] || room.cover;
						}
					}

					// Look for anchor/owner info
					if (obj.owner || obj.anchor || obj.user) {
						const user = obj.owner || obj.anchor || obj.user;
						if (user.nickname || user.nick_name || user.name) {
							result.streamer = user.nickname || user.nick_name || user.name;
						}
					}

					// Look for status
					if (obj.status !== undefined) {
						result.isLive = obj.status === 2 || obj.status === '2' || obj.status === 'live';
					}

					// Look for title
					if (obj.title && typeof obj.title === 'string') {
						result.title = obj.title;
					}

					if (Object.keys(result).length > 0) return result;

					// Recursively search
					for (let key in obj) {
						if (obj.hasOwnProperty(key)) {
							const nested = findData(obj[key], depth + 1);
							if (nested && Object.keys(nested).length > 0) {
								return nested;
							}
						}
					}

					return null;
				};

				// Try different global variables
				if (window.__RENDER_DATA__) {
					try {
						const data = JSON.parse(window.__RENDER_DATA__);
						const result = findData(data);
						if (result) return result;
					} catch (e) {}
				}

				if (window.INITIAL_STATE) {
					const result = findData(window.INITIAL_STATE);
					if (result) return result;
				}

				if (window.$ROOM) {
					const result = findData(window.$ROOM);
					if (result) return result;
				}

				// Search all global variables
				for (let key in window) {
					if (key.includes('ROOM') || key.includes('STATE') || key.includes('DATA')) {
						try {
							const result = findData(window[key]);
							if (result && Object.keys(result).length > 0) return result;
						} catch (e) {}
					}
				}

				return {};
			} catch (e) {
				console.error('Error extracting data:', e);
				return {};
			}
		})()
	`

	result, err := rawPage.Evaluate(dataScript)
	if err == nil {
		if data, ok := result.(map[string]interface{}); ok {
			if title, ok := data["title"].(string); ok && title != "" {
				info.Title = title
			}
			if streamer, ok := data["streamer"].(string); ok && streamer != "" {
				info.Streamer = streamer
			}
			if isLive, ok := data["isLive"].(bool); ok {
				info.IsLive = isLive
			}
			if viewerCount, ok := data["viewerCount"].(float64); ok && viewerCount > 0 {
				count := int(viewerCount)
				info.ViewerCount = &count
			}
			if cover, ok := data["cover"].(string); ok && cover != "" {
				info.Cover = cover
			}
		}
	}

	// Fallback: try DOM selectors if data not found from global variables
	if info.Title == "" {
		titleSelectors := []string{
			"h1.Title",
			".live-title",
			"[data-e2e='living-title']",
			"title",
			"h1",
		}

		for _, selector := range titleSelectors {
			titleEl, err := rawPage.QuerySelector(selector)
			if err == nil && titleEl != nil {
				title, err := titleEl.InnerText()
				if err == nil && strings.TrimSpace(title) != "" {
					info.Title = strings.TrimSpace(title)
					break
				}
			}
		}
	}

	if info.Streamer == "" {
		streamerSelectors := []string{
			"[data-e2e='living-nickname']",
			".author-name",
			".nickname",
			".anchor-name",
		}

		for _, selector := range streamerSelectors {
			streamerEl, err := rawPage.QuerySelector(selector)
			if err == nil && streamerEl != nil {
				streamer, err := streamerEl.InnerText()
				if err == nil && strings.TrimSpace(streamer) != "" {
					info.Streamer = strings.TrimSpace(streamer)
					break
				}
			}
		}
	}

	// Check if live by video element presence
	if !info.IsLive {
		videoEl, err := rawPage.QuerySelector("video")
		if err == nil && videoEl != nil {
			info.IsLive = true
		}
	}

	return nil
}

// extractStreamURL tries to extract the stream URL from the page
func extractStreamURL(page *playwright.Page) (string, error) {
	rawPage := page.Raw()

	// Enhanced script to find real stream URLs (not blob URLs)
	streamURLScript := `
		(() => {
			try {
				const findStreamURL = (obj, depth = 0) => {
					if (depth > 10) return null;
					if (!obj || typeof obj !== 'object') return null;

					// Check if current object has stream URL properties
					if (obj.flv_pull_url) {
						const urls = obj.flv_pull_url;
						return urls.FULL_HD1 || urls.HD1 || urls.SD1 || urls.SD2;
					}
					if (obj.hls_pull_url_map) {
						const urls = obj.hls_pull_url_map;
						return urls.FULL_HD1 || urls.HD1 || urls.SD1 || urls.SD2;
					}
					if (obj.hls_pull_url) {
						return obj.hls_pull_url;
					}
					if (obj.rtmp_pull_url) {
						return obj.rtmp_pull_url;
					}

					// Look for URLs that contain .flv or .m3u8
					if (typeof obj === 'string') {
						if (obj.includes('.flv') || obj.includes('.m3u8')) {
							return obj;
						}
					}

					// Recursively search in object properties
					for (let key in obj) {
						if (obj.hasOwnProperty(key)) {
							const result = findStreamURL(obj[key], depth + 1);
							if (result) return result;
						}
					}

					return null;
				};

				// Try window.__RENDER_DATA__
				if (window.__RENDER_DATA__) {
					try {
						const data = JSON.parse(window.__RENDER_DATA__);
						const url = findStreamURL(data);
						if (url) return url;
					} catch (e) {}
				}

				// Try window.INITIAL_STATE
				if (window.INITIAL_STATE) {
					const url = findStreamURL(window.INITIAL_STATE);
					if (url) return url;
				}

				// Try window.$ROOM
				if (window.$ROOM) {
					const url = findStreamURL(window.$ROOM);
					if (url) return url;
				}

				// Try all global variables
				for (let key in window) {
					if (key.includes('ROOM') || key.includes('STREAM') || key.includes('DATA')) {
						try {
							const url = findStreamURL(window[key]);
							if (url) return url;
						} catch (e) {}
					}
				}

				return '';
			} catch (e) {
				console.error('Error extracting stream URL:', e);
				return '';
			}
		})()
	`

	result, err := rawPage.Evaluate(streamURLScript)
	if err != nil {
		return "", err
	}

	streamURL, ok := result.(string)
	if !ok || streamURL == "" || strings.HasPrefix(streamURL, "blob:") {
		return "", fmt.Errorf("no valid stream URL found")
	}

	return streamURL, nil
}

// parseViewerCount parses viewer count from text (handles formats like "1.2万", "1234人")
func parseViewerCount(text string) int {
	text = strings.TrimSpace(text)

	// Remove common suffixes
	text = strings.ReplaceAll(text, "人", "")
	text = strings.ReplaceAll(text, "观看", "")
	text = strings.TrimSpace(text)

	// Handle Chinese number format (万)
	if strings.Contains(text, "万") {
		re := regexp.MustCompile(`([\d.]+)万`)
		matches := re.FindStringSubmatch(text)
		if len(matches) >= 2 {
			var num float64
			fmt.Sscanf(matches[1], "%f", &num)
			return int(num * 10000)
		}
	}

	// Try direct number parsing
	re := regexp.MustCompile(`\d+`)
	matches := re.FindString(text)
	if matches != "" {
		var count int
		fmt.Sscanf(matches, "%d", &count)
		return count
	}

	return 0
}

// DebugDouyinLivePage is a debug helper that prints all network requests and page data
// Useful for understanding the page structure and finding the correct stream URL
func DebugDouyinLivePage(roomURL string) error {
	roomID, err := extractRoomID(roomURL)
	if err != nil {
		return fmt.Errorf("invalid room URL: %w", err)
	}

	fmt.Printf("=== 调试抖音直播间: %s ===\n\n", roomID)

	browser, err := playwright.New()
	if err != nil {
		return fmt.Errorf("failed to create browser: %w", err)
	}
	defer browser.Close()

	page, err := browser.NewPage()
	if err != nil {
		return fmt.Errorf("failed to create page: %w", err)
	}
	defer page.Close()

	rawPage := page.Raw()

	// Log all network requests
	fmt.Println("=== 网络请求监听 ===")
	rawPage.OnRequest(func(request pw.Request) {
		url := request.URL()
		if strings.Contains(url, ".flv") || strings.Contains(url, ".m3u8") ||
			strings.Contains(url, "stream") || strings.Contains(url, "live") {
			fmt.Printf("[请求] %s: %s\n", request.ResourceType(), url)
		}
	})

	rawPage.OnResponse(func(response pw.Response) {
		url := response.URL()
		if strings.Contains(url, ".flv") || strings.Contains(url, ".m3u8") {
			fmt.Printf("[响应] %d %s\n", response.Status(), url)
		}
	})

	if err := page.Goto(roomURL); err != nil {
		return fmt.Errorf("failed to navigate to room: %w", err)
	}

	fmt.Println("\n等待页面加载...")
	time.Sleep(8 * time.Second)

	// Print all global variables that might contain stream data
	fmt.Println("\n=== 全局变量检查 ===")
	script := `
		(() => {
			const result = {};
			const keys = ['__RENDER_DATA__', 'INITIAL_STATE', '$ROOM', '__INITIAL_STATE__'];
			for (const key of keys) {
				if (window[key]) {
					try {
						result[key] = typeof window[key] === 'string' ? window[key] : JSON.stringify(window[key]);
					} catch (e) {
						result[key] = '[无法序列化]';
					}
				}
			}
			return result;
		})()
	`

	result, err := rawPage.Evaluate(script)
	if err == nil {
		if data, ok := result.(map[string]interface{}); ok {
			for key, value := range data {
				fmt.Printf("\n--- window.%s ---\n", key)
				if str, ok := value.(string); ok {
					if len(str) > 500 {
						fmt.Printf("%s...\n(总共 %d 字符)\n", str[:500], len(str))
					} else {
						fmt.Println(str)
					}
				}
			}
		}
	}

	return nil
}
