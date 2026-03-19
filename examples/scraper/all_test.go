package main

import (
	"fmt"
	"testing"

	"github.com/openmodu/modu/repos/scraper"
)

// TestAllSources 测试所有数据源并汇总输出
func TestAllSources(t *testing.T) {
	var allItems []scraper.NewsItem

	// HN
	t.Log("爬取 Hacker News...")
	if items, err := scraper.ScrapeHN(5); err == nil {
		allItems = append(allItems, items...)
		t.Logf("HN: 获取 %d 条", len(items))
	} else {
		t.Logf("HN 失败: %v", err)
	}

	// PH
	t.Log("爬取 Product Hunt...")
	if items, err := scraper.ScrapePH(5); err == nil {
		allItems = append(allItems, items...)
		t.Logf("PH: 获取 %d 条", len(items))
	} else {
		t.Logf("PH 失败: %v", err)
	}

	// TLDR
	t.Log("爬取 TLDR Tech...")
	if items, err := scraper.ScrapeTLDR("tech", 5); err == nil {
		allItems = append(allItems, items...)
		t.Logf("TLDR: 获取 %d 条", len(items))
	} else {
		t.Logf("TLDR 失败: %v", err)
	}

	// 汇总
	t.Logf("\n总计获取 %d 条数据", len(allItems))

	// 输出 Markdown
	output, _ := scraper.FormatOutput(allItems, scraper.FormatMarkdown)
	fmt.Println("\n" + output)
}

// TestOutputFormats 测试不同输出格式
func TestOutputFormats(t *testing.T) {
	items, err := scraper.ScrapeHN(3)
	if err != nil {
		t.Fatalf("ScrapeHN failed: %v", err)
	}

	formats := []struct {
		name   string
		format scraper.OutputFormat
	}{
		{"Text", scraper.FormatText},
		{"Markdown", scraper.FormatMarkdown},
		{"JSON", scraper.FormatJSON},
	}

	for _, f := range formats {
		t.Run(f.name, func(t *testing.T) {
			output, err := scraper.FormatOutput(items, f.format)
			if err != nil {
				t.Fatalf("FormatOutput failed: %v", err)
			}
			fmt.Printf("\n=== %s Format ===\n%s\n", f.name, output)
		})
	}
}
