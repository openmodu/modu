package scraper

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/openmodu/modu/pkg/playwright"
)

// ScrapeHN scrapes Hacker News front page
func ScrapeHN(limit int) ([]NewsItem, error) {
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

	if err := page.Goto("https://news.ycombinator.com/"); err != nil {
		return nil, fmt.Errorf("failed to load HN: %w", err)
	}

	if _, err := page.WaitForSelector(".athing", 10000); err != nil {
		return nil, fmt.Errorf("failed to find items: %w", err)
	}

	rows, err := page.QuerySelectorAll(".athing")
	if err != nil {
		return nil, err
	}

	var items []NewsItem
	scoreRegex := regexp.MustCompile(`(\d+)`)
	rawPage := page.Raw()

	for i, row := range rows {
		if i >= limit {
			break
		}

		// Use Locator-based API instead of deprecated ElementHandle.GetAttribute
		rowLocator := rawPage.Locator(fmt.Sprintf(".athing:nth-child(%d)", (i*3)+1))
		itemID, _ := rowLocator.GetAttribute("id")

		titleEl, err := row.QuerySelector(".titleline > a")
		if err != nil || titleEl == nil {
			continue
		}

		title, err := titleEl.InnerText()
		if err != nil {
			continue
		}

		// Use Locator-based API for getting href attribute
		titleLocator := rowLocator.Locator(".titleline > a")
		itemURL, _ := titleLocator.GetAttribute("href")
		if strings.HasPrefix(itemURL, "item?") {
			itemURL = "https://news.ycombinator.com/" + itemURL
		}

		// Get score
		var score *int
		scoreSelector := fmt.Sprintf("#score_%s", itemID)
		scoreEl, err := rawPage.QuerySelector(scoreSelector)
		if err == nil && scoreEl != nil {
			scoreText, err := scoreEl.InnerText()
			if err == nil {
				if matches := scoreRegex.FindStringSubmatch(scoreText); len(matches) > 1 {
					if s, err := strconv.Atoi(matches[1]); err == nil {
						score = &s
					}
				}
			}
		}

		items = append(items, NewsItem{
			Title:  strings.TrimSpace(title),
			URL:    itemURL,
			Source: "hackernews",
			Score:  score,
		})
	}

	return items, nil
}
