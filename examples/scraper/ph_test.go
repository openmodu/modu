package main

import (
	"fmt"
	"testing"

	"github.com/openmodu/modu/repos/scraper"
)

func TestScrapePH(t *testing.T) {
	items, err := scraper.ScrapePH(5)
	if err != nil {
		t.Fatalf("ScrapePH failed: %v", err)
	}

	if len(items) == 0 {
		t.Fatal("ScrapePH returned no items")
	}

	fmt.Printf("Product Hunt 获取 %d 条:\n", len(items))
	for i, item := range items {
		fmt.Printf("%d. %s\n", i+1, item.Title)
		fmt.Printf("   %s\n", item.Tagline)
		fmt.Printf("   URL: %s\n", item.URL)
		if item.Score != nil {
			fmt.Printf("   Votes: %d\n", *item.Score)
		}
	}
}
