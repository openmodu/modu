package scraper

import (
	"fmt"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/playwright"
)

const TopHubURL = "https://tophub.today/hot"

// ScrapeTopHub scrapes TopHub hot topics using Playwright
func ScrapeTopHub(limit int) ([]NewsItem, error) {
	// 创建浏览器实例
	browser, err := playwright.New(
		playwright.WithHeadless(true),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create browser: %w", err)
	}
	defer browser.Close()

	// 创建页面
	page, err := browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("failed to create page: %w", err)
	}
	defer page.Close()

	// 注入反检测脚本
	if err := page.InjectAntiDetect(); err != nil {
		return nil, fmt.Errorf("failed to inject anti-detect: %w", err)
	}

	// 导航到 TopHub
	if err := page.Goto(TopHubURL, playwright.WithWaitUntil("networkidle"), playwright.WithTimeout(30000)); err != nil {
		return nil, fmt.Errorf("failed to navigate to TopHub: %w", err)
	}

	// 等待页面加载
	page.Wait(5 * time.Second)

	// 获取热点数据
	items, err := extractTopHubItems(page, limit)
	if err != nil {
		return nil, err
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no topics found, TopHub structure may have changed")
	}

	return items, nil
}

// extractTopHubItems 从页面提取热点话题
// TopHub /hot 页面结构:
// li.child-item
//
//	├── .left-item span[class^="index-"] (排名)
//	└── .center-item .item-info .info-content
//	    ├── p.medium-txt a (标题和链接)
//	    └── p.small-txt (来源 ‧ 热度)
func extractTopHubItems(page *playwright.Page, limit int) ([]NewsItem, error) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	items := make([]NewsItem, 0, limit)

	rawPage := page.Raw()

	// 使用 Locator-based API
	itemLocator := rawPage.Locator("li.child-item")
	count, err := itemLocator.Count()
	if err != nil {
		return nil, fmt.Errorf("failed to count child-items: %w", err)
	}

	for i := 0; i < count && len(items) < limit; i++ {
		el := itemLocator.Nth(i)

		// 获取排名
		rank := fmt.Sprintf("%d", i+1)
		rankLocator := el.Locator(".left-item span")
		if rankCount, _ := rankLocator.Count(); rankCount > 0 {
			if rankText, err := rankLocator.First().InnerText(); err == nil && rankText != "" {
				rank = strings.TrimSpace(rankText)
			}
		}

		// 获取标题和链接
		titleLocator := el.Locator(".center-item .item-info .info-content p.medium-txt a")
		if titleCount, _ := titleLocator.Count(); titleCount == 0 {
			// 尝试备用选择器
			titleLocator = el.Locator(".info-content a")
			if titleCount, _ := titleLocator.Count(); titleCount == 0 {
				continue
			}
		}

		title, err := titleLocator.First().InnerText()
		if err != nil || title == "" {
			continue
		}
		title = strings.TrimSpace(title)

		link, _ := titleLocator.First().GetAttribute("href")

		// 获取来源和热度
		source := "tophub"
		hot := ""
		infoLocator := el.Locator(".center-item .item-info .info-content p.small-txt")
		if infoCount, _ := infoLocator.Count(); infoCount > 0 {
			infoText, _ := infoLocator.First().InnerText()
			infoText = strings.TrimSpace(infoText)
			// 格式: "知乎 ‧ 4329万热度"
			parts := strings.Split(infoText, "‧")
			if len(parts) >= 2 {
				source = strings.TrimSpace(parts[0])
				hot = strings.TrimSpace(parts[1])
			} else if infoText != "" {
				source = infoText
			}
		}

		// Convert to NewsItem format
		items = append(items, NewsItem{
			Title:     fmt.Sprintf("[%s] %s", rank, title),
			URL:       link,
			Source:    "tophub",
			Author:    source, // Use Author field to store original source
			Tagline:   hot,    // Use Tagline field to store hot info
			Timestamp: timestamp,
		})
	}

	return items, nil
}

// TopHubTopic represents a TopHub hot topic (for extended use)
type TopHubTopic struct {
	Rank      string `json:"rank"`
	Title     string `json:"title"`
	Link      string `json:"link"`
	Hot       string `json:"hot"`
	Source    string `json:"source"`
	Timestamp string `json:"timestamp"`
}

// ScrapeTopHubRaw scrapes TopHub and returns raw TopHubTopic items
func ScrapeTopHubRaw(limit int) ([]TopHubTopic, error) {
	// 创建浏览器实例
	browser, err := playwright.New(
		playwright.WithHeadless(true),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create browser: %w", err)
	}
	defer browser.Close()

	// 创建页面
	page, err := browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("failed to create page: %w", err)
	}
	defer page.Close()

	// 注入反检测脚本
	if err := page.InjectAntiDetect(); err != nil {
		return nil, fmt.Errorf("failed to inject anti-detect: %w", err)
	}

	// 导航到 TopHub
	if err := page.Goto(TopHubURL, playwright.WithWaitUntil("networkidle"), playwright.WithTimeout(30000)); err != nil {
		return nil, fmt.Errorf("failed to navigate to TopHub: %w", err)
	}

	// 等待页面加载
	page.Wait(5 * time.Second)

	// 获取热点数据
	topics, err := extractTopHubTopics(page, limit)
	if err != nil {
		return nil, err
	}

	if len(topics) == 0 {
		return nil, fmt.Errorf("no topics found, TopHub structure may have changed")
	}

	return topics, nil
}

// extractTopHubTopics 从页面提取原始热点话题
func extractTopHubTopics(page *playwright.Page, limit int) ([]TopHubTopic, error) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	topics := make([]TopHubTopic, 0, limit)

	rawPage := page.Raw()

	// 使用 Locator-based API
	itemLocator := rawPage.Locator("li.child-item")
	count, err := itemLocator.Count()
	if err != nil {
		return nil, fmt.Errorf("failed to count child-items: %w", err)
	}

	for i := 0; i < count && len(topics) < limit; i++ {
		el := itemLocator.Nth(i)

		// 获取排名
		rank := fmt.Sprintf("%d", i+1)
		rankLocator := el.Locator(".left-item span")
		if rankCount, _ := rankLocator.Count(); rankCount > 0 {
			if rankText, err := rankLocator.First().InnerText(); err == nil && rankText != "" {
				rank = strings.TrimSpace(rankText)
			}
		}

		// 获取标题和链接
		titleLocator := el.Locator(".center-item .item-info .info-content p.medium-txt a")
		if titleCount, _ := titleLocator.Count(); titleCount == 0 {
			// 尝试备用选择器
			titleLocator = el.Locator(".info-content a")
			if titleCount, _ := titleLocator.Count(); titleCount == 0 {
				continue
			}
		}

		title, err := titleLocator.First().InnerText()
		if err != nil || title == "" {
			continue
		}
		title = strings.TrimSpace(title)

		link, _ := titleLocator.First().GetAttribute("href")

		// 获取来源和热度
		source := "tophub"
		hot := ""
		infoLocator := el.Locator(".center-item .item-info .info-content p.small-txt")
		if infoCount, _ := infoLocator.Count(); infoCount > 0 {
			infoText, _ := infoLocator.First().InnerText()
			infoText = strings.TrimSpace(infoText)
			// 格式: "知乎 ‧ 4329万热度"
			parts := strings.Split(infoText, "‧")
			if len(parts) >= 2 {
				source = strings.TrimSpace(parts[0])
				hot = strings.TrimSpace(parts[1])
			} else if infoText != "" {
				source = infoText
			}
		}

		topics = append(topics, TopHubTopic{
			Rank:      rank,
			Title:     title,
			Link:      link,
			Hot:       hot,
			Source:    source,
			Timestamp: timestamp,
		})
	}

	return topics, nil
}

// DeduplicateTopHubTopics 去重热点话题
func DeduplicateTopHubTopics(topics []TopHubTopic) []TopHubTopic {
	seen := make(map[string]bool)
	result := make([]TopHubTopic, 0, len(topics))

	for _, topic := range topics {
		// 基于标题去重
		key := strings.ToLower(strings.TrimSpace(topic.Title))
		if !seen[key] {
			seen[key] = true
			result = append(result, topic)
		}
	}

	return result
}

// FilterTopHubByKeywords 根据关键词过滤话题
func FilterTopHubByKeywords(topics []TopHubTopic, keywords []string) []TopHubTopic {
	if len(keywords) == 0 {
		return topics
	}

	result := make([]TopHubTopic, 0)
	for _, topic := range topics {
		for _, keyword := range keywords {
			if strings.Contains(strings.ToLower(topic.Title), strings.ToLower(keyword)) {
				result = append(result, topic)
				break
			}
		}
	}

	return result
}
