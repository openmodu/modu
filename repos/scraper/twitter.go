package scraper

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/openmodu/modu/pkg/playwright"
)

// TwitterScraper handles Twitter/X scraping with authentication
type TwitterScraper struct {
	browser     *playwright.Browser
	cookieStore *playwright.CookieStore
}

// NewTwitterScraper creates a new Twitter scraper
func NewTwitterScraper() (*TwitterScraper, error) {
	// Check if we have saved cookies
	homeDir, _ := os.UserHomeDir()
	cookiePath := filepath.Join(homeDir, ".modu-scraper", "twitter-cookies.json")
	cookieStore := playwright.NewCookieStore(cookiePath)

	// Launch browser in visible mode if no cookies
	headless := cookieStore.Exists()

	browser, err := playwright.New(playwright.WithHeadless(headless))
	if err != nil {
		return nil, err
	}

	return &TwitterScraper{
		browser:     browser,
		cookieStore: cookieStore,
	}, nil
}

// Close closes the browser
func (s *TwitterScraper) Close() {
	if s.browser != nil {
		s.browser.Close()
	}
}

// ensureAuth ensures Twitter authentication
func (s *TwitterScraper) ensureAuth(ctx *playwright.Context) error {
	// Try to load existing cookies
	if s.cookieStore.Exists() {
		if err := s.cookieStore.Load(ctx); err == nil {
			fmt.Fprintln(os.Stderr, "Loaded saved Twitter session")
			return nil
		}
		fmt.Fprintln(os.Stderr, "Failed to load saved session, will need to login")
	}

	// No valid cookies, need to login manually
	fmt.Fprintln(os.Stderr, "No saved session found, opening browser for login...")

	page, err := ctx.NewPage()
	if err != nil {
		return err
	}
	defer page.Close()

	fmt.Fprintln(os.Stderr, "\n==========================================================")
	fmt.Fprintln(os.Stderr, "Please log in to Twitter/X in the browser window")
	fmt.Fprintln(os.Stderr, "After logging in, the scraper will continue automatically")
	fmt.Fprintln(os.Stderr, "==========================================================")

	if err := page.Goto("https://x.com/i/flow/login", playwright.WithTimeout(30000)); err != nil {
		return fmt.Errorf("failed to load login page: %w", err)
	}

	// Wait for user to complete login
	fmt.Fprintln(os.Stderr, "Waiting for you to complete login...")
	if _, err := page.WaitForSelector("nav[role='navigation']", 300000); err != nil {
		return fmt.Errorf("login timeout or failed: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Successfully logged in to Twitter/X")

	// Save cookies
	if err := s.cookieStore.Save(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save cookies: %v\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "Session cookies saved")
	}

	return nil
}

// ScrapeTrending scrapes Twitter/X trending topics
func (s *TwitterScraper) ScrapeTrending(limit int) ([]NewsItem, error) {
	ctx, err := s.browser.NewContext()
	if err != nil {
		return nil, err
	}
	defer ctx.Close()

	if err := s.ensureAuth(ctx); err != nil {
		return nil, err
	}

	page, err := ctx.NewPage()
	if err != nil {
		return nil, err
	}
	defer page.Close()

	if err := page.Goto("https://x.com/explore/tabs/trending", playwright.WithTimeout(90000)); err != nil {
		return nil, fmt.Errorf("failed to load X explore page: %w", err)
	}

	page.Wait(5000)

	// Scroll to load more
	for i := 0; i < 2; i++ {
		page.Scroll(0, 800)
		page.Wait(1000)
	}

	// Extract trending topics using JavaScript
	result, err := page.Evaluate(fmt.Sprintf(`() => {
		const trends = [];
		const cells = document.querySelectorAll('[data-testid="trend"], [data-testid="cellInnerDiv"]');

		cells.forEach((cell) => {
			if (trends.length >= %d) return;

			const spans = cell.querySelectorAll('span');
			let trendName = '';
			let tweetCount = '';
			let category = '';

			spans.forEach(span => {
				const text = span.textContent.trim();
				if (text.startsWith('#') || (text.length > 2 && text.length < 100 && !text.includes('·') && !text.toLowerCase().includes('trending'))) {
					if (!trendName && text.length > 1) {
						trendName = text;
					}
				}
				if (text.includes('posts') || text.includes('tweets') || text.match(/\d+[KMB]?\s*(posts|tweets)/i)) {
					tweetCount = text;
				}
				if (text.includes('Trending in') || text.includes('trending')) {
					category = text;
				}
			});

			if (trendName && trendName.length > 1) {
				trends.push({
					name: trendName,
					count: tweetCount,
					category: category
				});
			}
		});

		return trends;
	}`, limit))

	if err != nil {
		return nil, fmt.Errorf("failed to extract trends: %w", err)
	}

	var items []NewsItem
	if trends, ok := result.([]interface{}); ok {
		for i, t := range trends {
			if i >= limit {
				break
			}
			if trend, ok := t.(map[string]interface{}); ok {
				name, _ := trend["name"].(string)
				count, _ := trend["count"].(string)
				category, _ := trend["category"].(string)

				if name == "" {
					continue
				}

				var tagline string
				if count != "" {
					tagline = count
				}
				if category != "" {
					if tagline != "" {
						tagline = category + " - " + tagline
					} else {
						tagline = category
					}
				}

				items = append(items, NewsItem{
					Title:   name,
					URL:     fmt.Sprintf("https://x.com/search?q=%s", url.QueryEscape(name)),
					Source:  "twitter-trending",
					Tagline: tagline,
				})
			}
		}
	}

	return items, nil
}

// ScrapeUser scrapes a user's timeline
func (s *TwitterScraper) ScrapeUser(username string, limit int) ([]NewsItem, error) {
	ctx, err := s.browser.NewContext()
	if err != nil {
		return nil, err
	}
	defer ctx.Close()

	if err := s.ensureAuth(ctx); err != nil {
		return nil, err
	}

	page, err := ctx.NewPage()
	if err != nil {
		return nil, err
	}
	defer page.Close()

	userURL := fmt.Sprintf("https://x.com/%s", username)
	if err := page.Goto(userURL, playwright.WithTimeout(90000)); err != nil {
		return nil, fmt.Errorf("failed to load user timeline: %w", err)
	}

	page.Wait(5000)
	page.WaitForSelector("article[data-testid=\"tweet\"]", 15000)

	// Scroll to load more tweets
	for i := 0; i < 3; i++ {
		page.Scroll(0, 1000)
		page.Wait(1000)
	}

	// Extract tweets using JavaScript
	result, err := page.Evaluate(fmt.Sprintf(`(username) => {
		const tweets = [];
		const articles = document.querySelectorAll('article[data-testid="tweet"]');

		articles.forEach((article) => {
			if (tweets.length >= %d) return;

			const tweetTextEl = article.querySelector('[data-testid="tweetText"]');
			const tweetText = tweetTextEl ? tweetTextEl.textContent.trim() : '';

			if (!tweetText) return;

			// Extract author
			const userLinks = article.querySelectorAll('a[href^="/"]');
			let author = '';
			userLinks.forEach(link => {
				const href = link.getAttribute('href');
				if (href && href.startsWith('/') && !href.includes('/status/') && href.split('/').length === 2) {
					const spans = link.querySelectorAll('span');
					spans.forEach(span => {
						const text = span.textContent.trim();
						if (text.startsWith('@')) {
							author = text.replace('@', '');
						}
					});
				}
			});

			// Extract tweet URL
			let tweetURL = '';
			const timeLink = article.querySelector('a[href*="/status/"] time');
			if (timeLink) {
				const parentLink = timeLink.closest('a');
				if (parentLink) {
					tweetURL = 'https://x.com' + parentLink.getAttribute('href');
				}
			}

			// Extract time
			const timeEl = article.querySelector('time');
			const timestamp = timeEl ? timeEl.getAttribute('datetime') : '';

			// Extract stats
			const statsGroup = article.querySelector('[role="group"]');
			let likes = '';
			let retweets = '';

			if (statsGroup) {
				const buttons = statsGroup.querySelectorAll('button');
				buttons.forEach(btn => {
					const ariaLabel = btn.getAttribute('aria-label') || '';
					if (ariaLabel.includes('like') || ariaLabel.includes('Like')) {
						const match = ariaLabel.match(/(\d+)/);
						if (match) likes = match[1];
					}
					if (ariaLabel.includes('repost') || ariaLabel.includes('Repost')) {
						const match = ariaLabel.match(/(\d+)/);
						if (match) retweets = match[1];
					}
				});
			}

			tweets.push({
				text: tweetText,
				author: author || username,
				url: tweetURL,
				timestamp: timestamp,
				likes: likes,
				retweets: retweets
			});
		});

		return tweets;
	}`, limit), username)

	if err != nil {
		return nil, fmt.Errorf("failed to extract tweets: %w", err)
	}

	var items []NewsItem
	if tweets, ok := result.([]interface{}); ok {
		for i, t := range tweets {
			if i >= limit {
				break
			}
			if tweet, ok := t.(map[string]interface{}); ok {
				text, _ := tweet["text"].(string)
				author, _ := tweet["author"].(string)
				tweetURL, _ := tweet["url"].(string)
				timestamp, _ := tweet["timestamp"].(string)
				likes, _ := tweet["likes"].(string)
				retweets, _ := tweet["retweets"].(string)

				if text == "" {
					continue
				}

				// Truncate title
				title := text
				if len(title) > 100 {
					title = title[:97] + "..."
				}

				// Build tagline
				var taglineParts []string
				if likes != "" && likes != "0" {
					taglineParts = append(taglineParts, "Likes: "+likes)
				}
				if retweets != "" && retweets != "0" {
					taglineParts = append(taglineParts, "Retweets: "+retweets)
				}

				item := NewsItem{
					Title:     title,
					URL:       tweetURL,
					Source:    "twitter-user",
					Author:    author,
					Timestamp: timestamp,
				}
				if len(taglineParts) > 0 {
					item.Tagline = strings.Join(taglineParts, " | ")
				}

				items = append(items, item)
			}
		}
	}

	return items, nil
}

// ScrapeTwitterTrending is a standalone function for Twitter trending
func ScrapeTwitterTrending(limit int) ([]NewsItem, error) {
	scraper, err := NewTwitterScraper()
	if err != nil {
		return nil, err
	}
	defer scraper.Close()

	return scraper.ScrapeTrending(limit)
}

// ScrapeTwitterUser is a standalone function for Twitter user timeline
func ScrapeTwitterUser(username string, limit int) ([]NewsItem, error) {
	scraper, err := NewTwitterScraper()
	if err != nil {
		return nil, err
	}
	defer scraper.Close()

	return scraper.ScrapeUser(username, limit)
}
