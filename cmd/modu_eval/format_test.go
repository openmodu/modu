package main

import (
	"testing"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

func TestTruncateIsRuneSafeAndWidthBounded(t *testing.T) {
	cases := []struct {
		name  string
		value string
		max   int
	}{
		{"ascii under", "hello", 10},
		{"ascii over", "hello world", 8},
		{"cjk over", "法国的首都是巴黎", 6},
		{"cjk exact-ish", "中文测试", 5},
		{"max below ellipsis", "abcdef", 2},
		{"max one", "中文", 1},
		{"max zero", "anything", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.value, tc.max) // must never panic
			if !utf8.ValidString(got) {
				t.Fatalf("truncate(%q,%d)=%q is not valid UTF-8", tc.value, tc.max, got)
			}
			if w := runewidth.StringWidth(got); tc.max > 0 && w > tc.max {
				t.Fatalf("truncate(%q,%d)=%q width %d exceeds max %d", tc.value, tc.max, got, w, tc.max)
			}
		})
	}
}

func TestTruncateDisplayRuneSafe(t *testing.T) {
	got := truncateDisplay("中文测试内容很长", 5)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateDisplay produced invalid UTF-8: %q", got)
	}
	if w := runewidth.StringWidth(got); w > 5 {
		t.Fatalf("truncateDisplay width %d exceeds 5: %q", w, got)
	}
	if utf8.RuneCountInString(got) == 0 || []rune(got)[utf8.RuneCountInString(got)-1] != '…' {
		t.Fatalf("expected trailing ellipsis, got %q", got)
	}
}

func TestCellPadsToExactDisplayWidth(t *testing.T) {
	cases := []struct {
		value string
		width int
	}{
		{"ab", 5},
		{"中文", 6},    // 2 wide runes = 4 cells, pad to 6
		{"中文测试内", 6}, // overflow -> truncated+padded back to 6
		{"", 4},
	}
	for _, tc := range cases {
		got := cell(tc.value, tc.width)
		if !utf8.ValidString(got) {
			t.Fatalf("cell(%q,%d)=%q invalid UTF-8", tc.value, tc.width, got)
		}
		if w := runewidth.StringWidth(got); w != tc.width {
			t.Fatalf("cell(%q,%d) display width = %d, want %d (%q)", tc.value, tc.width, w, tc.width, got)
		}
	}
}
