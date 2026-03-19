package main

import (
	"fmt"
	"testing"

	"github.com/openmodu/modu/repos/scraper"
)

func TestScrapeTwitterTrending(t *testing.T) {
	items, err := scraper.ScrapeTwitterTrending(10)
	if err != nil {
		t.Fatalf("ScrapeTwitterTrending failed: %v", err)
	}

	if len(items) == 0 {
		t.Fatal("ScrapeTwitterTrending returned no items")
	}

	fmt.Printf("Twitter Trending 获取 %d 条:\n", len(items))
	for i, item := range items {
		fmt.Printf("%d. %s\n", i+1, item.Title)
		if item.Tagline != "" {
			fmt.Printf("   %s\n", item.Tagline)
		}
		fmt.Printf("   URL: %s\n", item.URL)
	}
}

func TestScrapeTwitterUser(t *testing.T) {
	username := "elonmusk"

	items, err := scraper.ScrapeTwitterUser(username, 5)
	if err != nil {
		t.Fatalf("ScrapeTwitterUser failed: %v", err)
	}

	if len(items) == 0 {
		t.Fatal("ScrapeTwitterUser returned no items")
	}

	fmt.Printf("Twitter @%s 获取 %d 条:\n", username, len(items))
	for i, item := range items {
		fmt.Printf("%d. %s\n", i+1, item.Title)
		if item.Tagline != "" {
			fmt.Printf("   %s\n", item.Tagline)
		}
		fmt.Printf("   URL: %s\n", item.URL)
	}
}
