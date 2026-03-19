package main

import (
	"fmt"
	"testing"

	"github.com/openmodu/modu/repos/scraper"
)

func TestScrapeTLDR(t *testing.T) {
	categories := []string{"tech", "ai"}

	for _, category := range categories {
		t.Run(category, func(t *testing.T) {
			items, err := scraper.ScrapeTLDR(category, 5)
			if err != nil {
				t.Fatalf("ScrapeTLDR(%s) failed: %v", category, err)
			}

			fmt.Printf("\nTLDR %s 获取 %d 条:\n", category, len(items))
			for i, item := range items {
				fmt.Printf("%d. %s\n", i+1, item.Title)
				if item.URL != "" {
					fmt.Printf("   URL: %s\n", item.URL)
				}
			}
		})
	}
}
