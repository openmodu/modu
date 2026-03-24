# Scraper

基于 Playwright 的网页爬虫集合，支持多种数据源。

## 安装

```go
import "github.com/openmodu/modu/repos/scraper"
```

## 支持的数据源

| 数据源 | 函数 | 说明 |
|--------|------|------|
| Hacker News | `ScrapeHN` | 爬取 HN 首页 |
| Product Hunt | `ScrapePH` | 爬取 PH 首页 |
| Newsletter | `ScrapeNewsletter` | 通用 Newsletter 爬虫 |
| Substack | `ScrapeSubstack` | Substack 订阅 |
| TLDR | `ScrapeTLDR` | TLDR Newsletter |
| Twitter Trending | `ScrapeTwitterTrending` | Twitter/X 热门话题 |
| Twitter User | `ScrapeTwitterUser` | Twitter/X 用户时间线 |

## 使用

### Hacker News

```go
items, err := scraper.ScrapeHN(20)
if err != nil {
    log.Fatal(err)
}

for _, item := range items {
    fmt.Printf("%s - %s\n", item.Title, item.URL)
    if item.Score != nil {
        fmt.Printf("  Score: %d\n", *item.Score)
    }
}
```

### Product Hunt

```go
items, err := scraper.ScrapePH(20)
if err != nil {
    log.Fatal(err)
}

for _, item := range items {
    fmt.Printf("%s - %s\n", item.Title, item.URL)
    fmt.Printf("  %s\n", item.Tagline)
}
```

### Newsletter

```go
// 通用 Newsletter
items, err := scraper.ScrapeNewsletter("https://example.com/archive", nil, 20)

// 自定义选择器
selectors := &scraper.Selectors{
    Container: ".post-item",
    Title:     "h2",
    Link:      "a",
    Date:      ".date",
}
items, err := scraper.ScrapeNewsletter("https://example.com/archive", selectors, 20)
```

### Substack

```go
// 爬取 Substack 订阅
items, err := scraper.ScrapeSubstack("stratechery", 20)
```

### TLDR Newsletter

```go
// 支持的类别: tech, ai, webdev, crypto, devops, founders
items, err := scraper.ScrapeTLDR("tech", 20)
items, err := scraper.ScrapeTLDR("ai", 20)
```

### Twitter/X

```go
// 热门话题
items, err := scraper.ScrapeTwitterTrending(20)

// 用户时间线
items, err := scraper.ScrapeTwitterUser("elonmusk", 20)
```

**注意**: 首次运行 Twitter 爬虫时会打开浏览器窗口，需要手动登录。登录后 Cookie 会保存到 `~/.modu-scraper/twitter-cookies.json`，后续运行无需再次登录。

### 使用 TwitterScraper 实例

```go
// 创建实例（可复用）
ts, err := scraper.NewTwitterScraper()
if err != nil {
    log.Fatal(err)
}
defer ts.Close()

// 多次爬取
trending, _ := ts.ScrapeTrending(20)
user1, _ := ts.ScrapeUser("elonmusk", 10)
user2, _ := ts.ScrapeUser("sama", 10)
```

## 输出格式化

```go
items, _ := scraper.ScrapeHN(20)

// JSON 格式
json, _ := scraper.FormatOutput(items, scraper.FormatJSON)

// Markdown 格式
md, _ := scraper.FormatOutput(items, scraper.FormatMarkdown)

// 纯文本格式
text, _ := scraper.FormatOutput(items, scraper.FormatText)
```

## 数据结构

### NewsItem

```go
type NewsItem struct {
    Title     string `json:"title"`
    URL       string `json:"url"`
    Source    string `json:"source"`
    Score     *int   `json:"score,omitempty"`
    Comments  *int   `json:"comments,omitempty"`
    Author    string `json:"author,omitempty"`
    Tagline   string `json:"tagline,omitempty"`
    Timestamp string `json:"timestamp,omitempty"`
}
```

### Selectors

```go
type Selectors struct {
    Container string // 文章容器选择器
    Title     string // 标题选择器
    Link      string // 链接选择器
    Date      string // 日期选择器
}

// 默认选择器
scraper.DefaultSelectors()
```

## 示例：完整爬虫程序

```go
package main

import (
    "fmt"
    "log"

    "github.com/openmodu/modu/repos/scraper"
)

func main() {
    // 爬取多个数据源
    var allItems []scraper.NewsItem

    // HN
    if items, err := scraper.ScrapeHN(10); err == nil {
        allItems = append(allItems, items...)
    }

    // PH
    if items, err := scraper.ScrapePH(10); err == nil {
        allItems = append(allItems, items...)
    }

    // 格式化输出
    output, _ := scraper.FormatOutput(allItems, scraper.FormatMarkdown)
    fmt.Println(output)
}
```
