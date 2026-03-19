package scraper

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/openmodu/modu/pkg/playwright"
)

// ScrapeNewsletter scrapes a newsletter archive with configurable selectors
func ScrapeNewsletter(archiveURL string, selectors *Selectors, limit int) ([]NewsItem, error) {
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

	if err := page.Goto(archiveURL, playwright.WithWaitUntil("networkidle"), playwright.WithTimeout(30000)); err != nil {
		return nil, fmt.Errorf("failed to load newsletter: %w", err)
	}

	page.Wait(2000)

	sel := DefaultSelectors()
	if selectors != nil {
		if selectors.Container != "" {
			sel.Container = selectors.Container
		}
		if selectors.Title != "" {
			sel.Title = selectors.Title
		}
		if selectors.Link != "" {
			sel.Link = selectors.Link
		}
		if selectors.Date != "" {
			sel.Date = selectors.Date
		}
	}

	parsedURL, err := url.Parse(archiveURL)
	if err != nil {
		return nil, err
	}
	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	containers, err := page.QuerySelectorAll(sel.Container)
	if err != nil {
		return nil, err
	}

	var items []NewsItem
	seen := make(map[string]bool)

	for _, container := range containers {
		if len(items) >= limit {
			break
		}

		titleEl, err := container.QuerySelector(sel.Title)
		if err != nil || titleEl == nil {
			continue
		}

		title, err := titleEl.InnerText()
		if err != nil {
			continue
		}
		title = strings.TrimSpace(title)

		if title == "" || len(title) < 3 {
			continue
		}

		if seen[title] {
			continue
		}
		seen[title] = true

		// Find link - use Evaluate to avoid deprecated GetAttribute
		var itemURL string
		linkEl, err := container.QuerySelector(sel.Link)
		if err == nil && linkEl != nil {
			if href, err := linkEl.Evaluate("el => el.getAttribute('href')", nil); err == nil {
				if hrefStr, ok := href.(string); ok && hrefStr != "" {
					itemURL = resolveURL(baseURL, hrefStr)
				}
			}
		}

		// Date - use Evaluate to avoid deprecated GetAttribute
		var timestamp string
		dateEl, err := container.QuerySelector(sel.Date)
		if err == nil && dateEl != nil {
			if dt, err := dateEl.Evaluate("el => el.getAttribute('datetime')", nil); err == nil {
				if dtStr, ok := dt.(string); ok && dtStr != "" {
					timestamp = dtStr
				}
			}
			if timestamp == "" {
				if text, err := dateEl.InnerText(); err == nil {
					timestamp = strings.TrimSpace(text)
				}
			}
		}

		items = append(items, NewsItem{
			Title:     title,
			URL:       itemURL,
			Source:    "newsletter",
			Timestamp: timestamp,
		})
	}

	return items, nil
}

// ScrapeSubstack scrapes a Substack publication's archive
func ScrapeSubstack(publication string, limit int) ([]NewsItem, error) {
	archiveURL := fmt.Sprintf("https://%s.substack.com/archive", publication)
	return ScrapeNewsletter(archiveURL, nil, limit)
}

// ScrapeTLDR scrapes TLDR newsletter latest issue
func ScrapeTLDR(category string, limit int) ([]NewsItem, error) {
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

	// Get archive page
	archiveURL := fmt.Sprintf("https://tldr.tech/%s/archives", category)
	if err := page.Goto(archiveURL); err != nil {
		return nil, fmt.Errorf("failed to fetch TLDR archives: %w", err)
	}

	// Find latest issue link
	latestLink, err := page.QuerySelector("a[href*='/archives/']")
	if err != nil || latestLink == nil {
		return nil, fmt.Errorf("no archive links found")
	}

	// Use Evaluate to avoid deprecated GetAttribute
	hrefResult, err := latestLink.Evaluate("el => el.getAttribute('href')", nil)
	if err != nil {
		return nil, fmt.Errorf("no href in archive link")
	}
	latestHref, ok := hrefResult.(string)
	if !ok || latestHref == "" {
		return nil, fmt.Errorf("no href in archive link")
	}

	latestURL := resolveURL("https://tldr.tech", latestHref)

	// Fetch latest issue
	if err := page.Goto(latestURL); err != nil {
		return nil, fmt.Errorf("failed to fetch latest issue: %w", err)
	}

	page.Wait(1000)

	var items []NewsItem
	source := fmt.Sprintf("tldr-%s", category)

	// Try articles first
	articles, err := page.QuerySelectorAll("article, .article-link, [class*='article']")
	if err == nil {
		for _, article := range articles {
			if len(items) >= limit {
				break
			}

			titleEl, err := article.QuerySelector("h3, h4, .title, strong")
			if err != nil || titleEl == nil {
				continue
			}

			title, err := titleEl.InnerText()
			if err != nil {
				continue
			}
			title = strings.TrimSpace(title)

			if title == "" || len(title) <= 5 {
				continue
			}

			var itemURL string
			link, err := article.QuerySelector("a[href^='http']")
			if err == nil && link != nil {
				// Use Evaluate to avoid deprecated GetAttribute
				if href, err := link.Evaluate("el => el.getAttribute('href')", nil); err == nil {
					if hrefStr, ok := href.(string); ok {
						itemURL = hrefStr
					}
				}
			}

			items = append(items, NewsItem{
				Title:  title,
				URL:    itemURL,
				Source: source,
			})
		}
	}

	// Fallback to h3/h4 elements
	if len(items) == 0 {
		headings, err := page.QuerySelectorAll("h3, h4")
		if err == nil {
			for _, heading := range headings {
				if len(items) >= limit {
					break
				}

				title, err := heading.InnerText()
				if err != nil {
					continue
				}
				title = strings.TrimSpace(title)

				if title == "" || len(title) <= 5 {
					continue
				}

				items = append(items, NewsItem{
					Title:  title,
					Source: source,
				})
			}
		}
	}

	return items, nil
}

// resolveURL resolves a relative URL against a base URL
func resolveURL(baseURL, href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return href
	}

	ref, err := url.Parse(href)
	if err != nil {
		return href
	}

	return base.ResolveReference(ref).String()
}
