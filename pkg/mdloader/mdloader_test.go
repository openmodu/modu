package mdloader

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

type item struct {
	Name    string
	Source  string
	Content string
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readDirInto loads every *.md file in dir as an item named by its basename.
func readDirInto(dst map[string]*item, dir, source string, overwrite bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if _, exists := dst[name]; exists && !overwrite {
			continue
		}
		dst[name] = &item{Name: name, Source: source, Content: string(data)}
	}
	return nil
}

type overwriteParser struct{}

func (overwriteParser) ParseDir(dst map[string]*item, dir, src string) error {
	return readDirInto(dst, dir, src, true)
}
func (overwriteParser) ParsePath(dst map[string]*item, path, src string) error {
	return readDirInto(dst, path, src, true)
}

type firstWinsParser struct{}

func (firstWinsParser) ParseDir(dst map[string]*item, dir, src string) error {
	return readDirInto(dst, dir, src, false)
}
func (firstWinsParser) ParsePath(dst map[string]*item, path, src string) error {
	return readDirInto(dst, path, src, false)
}

func TestOrderedRootsOverwriteWins(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(a, "dup.md"), "from-a")
	writeFile(t, filepath.Join(b, "dup.md"), "from-b")

	// Roots scanned a then b; overwrite parser => later root (b) wins.
	m := New([]Ref{{a, "a"}, {b, "b"}}, overwriteParser{})
	got, ok := m.Lookup("dup")
	if !ok || got.Source != "b" || got.Content != "from-b" {
		t.Fatalf("expected later root to win, got %#v", got)
	}
}

func TestOrderedRootsFirstWins(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(a, "dup.md"), "from-a")
	writeFile(t, filepath.Join(b, "dup.md"), "from-b")

	// Same order, but keep-first parser => earlier root (a) wins.
	m := New([]Ref{{a, "a"}, {b, "b"}}, firstWinsParser{})
	got, ok := m.Lookup("dup")
	if !ok || got.Source != "a" {
		t.Fatalf("expected earlier root to win, got %#v", got)
	}
}

func TestExtraRefsAppliedAfterRoots(t *testing.T) {
	root, extra := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "x.md"), "builtin")
	writeFile(t, filepath.Join(extra, "x.md"), "extra")

	m := New([]Ref{{root, "root"}}, overwriteParser{})
	m.SetExtraRefs([]Ref{{extra, "package"}})
	got, _ := m.Lookup("x")
	if got.Source != "package" {
		t.Fatalf("extra ref should override builtin under overwrite parser, got %#v", got)
	}
}

func TestMissingRootIsNotAnError(t *testing.T) {
	m := New([]Ref{{filepath.Join(t.TempDir(), "does-not-exist"), "x"}}, overwriteParser{})
	if err := m.Discover(); err != nil {
		t.Fatalf("missing root should be ignored, got %v", err)
	}
	if len(m.Snapshot()) != 0 {
		t.Fatal("expected no items")
	}
}

func TestSnapshotRediscovers(t *testing.T) {
	root := t.TempDir()
	m := New([]Ref{{root, "root"}}, overwriteParser{})
	if len(m.Snapshot()) != 0 {
		t.Fatal("expected empty before any file")
	}
	writeFile(t, filepath.Join(root, "a.md"), "a")
	writeFile(t, filepath.Join(root, "b.md"), "b")
	snap := m.Snapshot()
	names := make([]string, 0, len(snap))
	for _, it := range snap {
		names = append(names, it.Name)
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("expected rediscovery of a,b; got %v", names)
	}
}

func TestConcurrentDiscoverAndLookup(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.md"), "a")
	m := New([]Ref{{root, "root"}}, overwriteParser{})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Discover()
			_, _ = m.Lookup("a")
			_ = m.Snapshot()
		}()
	}
	wg.Wait()
}
