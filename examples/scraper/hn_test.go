package main

import (
	"fmt"
	"testing"

	"github.com/openmodu/modu/repos/scraper"
)

func TestScrapeHN(t *testing.T) {
	items, err := scraper.ScrapeHN(5)
	if err != nil {
		t.Fatalf("ScrapeHN failed: %v", err)
	}

	if len(items) == 0 {
		t.Fatal("ScrapeHN returned no items")
	}

	fmt.Printf("HN 获取 %d 条:\n", len(items))
	for i, item := range items {
		fmt.Printf("%d. %s\n", i+1, item.Title)
		fmt.Printf("   URL: %s\n", item.URL)
		if item.Score != nil {
			fmt.Printf("   Score: %d\n", *item.Score)
		}
	}
}
