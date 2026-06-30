package session

import (
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestIsVisibleEntry(t *testing.T) {
	visible := []EntryType{EntryTypeMessage, EntryTypeBranchSummary, EntryTypeCompaction, EntryTypeModelChange}
	for _, et := range visible {
		if !IsVisibleEntry(SessionEntry{Type: et}) {
			t.Fatalf("%s should be visible", et)
		}
	}
	if IsVisibleEntry(SessionEntry{Type: EntryTypeLabel}) {
		t.Fatal("label entry should not be visible")
	}
}

func TestNearestVisibleParent(t *testing.T) {
	// chain: a(visible) <- b(hidden) <- c
	lookup := map[string]SessionEntry{
		"a": {ID: "a", ParentID: ""},
		"b": {ID: "b", ParentID: "a"},
		"c": {ID: "c", ParentID: "b"},
	}
	visible := map[string]struct{}{"a": {}}
	if got := NearestVisibleParent("b", lookup, visible); got != "a" {
		t.Fatalf("expected nearest visible parent 'a', got %q", got)
	}
	if got := NearestVisibleParent("", lookup, visible); got != "" {
		t.Fatalf("empty parent should resolve to empty, got %q", got)
	}
	// parent not in lookup -> ""
	if got := NearestVisibleParent("missing", lookup, visible); got != "" {
		t.Fatalf("unknown parent should resolve to empty, got %q", got)
	}
}

func TestEntryRole(t *testing.T) {
	if got := EntryRole(SessionEntry{Type: EntryTypeMessage, Data: MessageData{Role: "user"}}); got != "user" {
		t.Fatalf("typed message role = %q", got)
	}
	if got := EntryRole(SessionEntry{Type: EntryTypeMessage, Data: map[string]any{"role": "assistant"}}); got != "assistant" {
		t.Fatalf("map message role = %q", got)
	}
	if got := EntryRole(SessionEntry{Type: EntryTypeCompaction}); got != "" {
		t.Fatalf("non-message role should be empty, got %q", got)
	}
}

func TestTreeNodeLabel(t *testing.T) {
	entry := SessionEntry{Type: EntryTypeBranchSummary, Data: BranchSummaryData{FromID: "1234567890abcdef"}}
	if got := TreeNodeLabel(entry, ""); got != "from #12345678" {
		t.Fatalf("branch summary fallback label = %q", got)
	}
	if got := TreeNodeLabel(entry, "manual"); got != "manual" {
		t.Fatalf("explicit label should win, got %q", got)
	}
	if got := TreeNodeLabel(SessionEntry{Type: EntryTypeMessage}, ""); got != "" {
		t.Fatalf("non-branch entry without explicit label should be empty, got %q", got)
	}
	// map-decoded branch summary
	mapEntry := SessionEntry{Type: EntryTypeBranchSummary, Data: map[string]any{"fromId": "abcdefgh...."}}
	if got := TreeNodeLabel(mapEntry, ""); got != "from #abcdefgh" {
		t.Fatalf("map branch label = %q", got)
	}
}

func TestEntryPreview(t *testing.T) {
	cases := []struct {
		name  string
		entry SessionEntry
		want  string
	}{
		{"string content", SessionEntry{Type: EntryTypeMessage, Data: MessageData{Content: "hello"}}, "hello"},
		{"typed blocks", SessionEntry{Type: EntryTypeMessage, Data: MessageData{Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "a"}, &types.TextContent{Type: "text", Text: "b"},
		}}}, "a b"},
		{"branch summary", SessionEntry{Type: EntryTypeBranchSummary, Data: BranchSummaryData{Summary: "sum"}}, "sum"},
		{"compaction", SessionEntry{Type: EntryTypeCompaction, Data: CompactionData{Summary: "compact"}}, "compact"},
		{"compaction metadata", SessionEntry{Type: EntryTypeCompaction, Data: CompactionData{Summary: "compact", OriginalCount: 10, NewCount: 4, TokensBefore: 9000, PreservedUserMessages: 2, ReadFiles: []string{"a.go", "b.go"}, ModifiedFiles: []string{"c.go"}}}, "compact (messages 10->4, tokens before 9000, user anchors 2, read files 2, modified files 1)"},
		{"model change", SessionEntry{Type: EntryTypeModelChange, Data: ModelChangeData{Provider: "p", ModelID: "m"}}, "p/m"},
		{"map content", SessionEntry{Type: EntryTypeMessage, Data: map[string]any{"content": "mc"}}, "mc"},
		{"map summary", SessionEntry{Type: EntryTypeBranchSummary, Data: map[string]any{"summary": "ms"}}, "ms"},
		{"map compaction metadata", SessionEntry{Type: EntryTypeCompaction, Data: map[string]any{"summary": "mc", "originalCount": float64(8), "newCount": float64(3), "tokensBefore": float64(1200), "preservedUserMessages": float64(1), "readFiles": []any{"a.go"}, "modifiedFiles": []any{"b.go", "c.go"}}}, "mc (messages 8->3, tokens before 1200, user anchors 1, read files 1, modified files 2)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EntryPreview(tc.entry); got != tc.want {
				t.Fatalf("EntryPreview = %q, want %q", got, tc.want)
			}
		})
	}
}
