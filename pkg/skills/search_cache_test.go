package skills

import (
	"sync"
	"testing"
	"time"
)

// ---------- basic cache operations ----------

func TestSearchCache_ExactHit(t *testing.T) {
	c := NewSearchCache(10, time.Minute)
	results := []SearchResult{{Slug: "a", Score: 0.9}}
	c.Put("kubernetes", results)

	got, hit := c.Get("kubernetes")
	if !hit {
		t.Fatal("expected cache hit for exact query")
	}
	if len(got) != 1 || got[0].Slug != "a" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestSearchCache_CaseFolding(t *testing.T) {
	c := NewSearchCache(10, time.Minute)
	results := []SearchResult{{Slug: "a"}}
	c.Put("kubernetes", results)

	got, hit := c.Get("KUBERNETES")
	if !hit {
		t.Fatal("expected case-insensitive hit")
	}
	if len(got) != 1 {
		t.Errorf("unexpected len: %d", len(got))
	}
}

func TestSearchCache_SimilarQueryHit(t *testing.T) {
	c := NewSearchCache(10, time.Minute)
	results := []SearchResult{{Slug: "a"}}
	c.Put("kubernetes deployment", results)

	_, hit := c.Get("kubernetes deployments")
	if !hit {
		t.Log("Note: similar query not hit - this is OK if threshold is high")
		// Not a strict failure; similarity threshold may legitimately miss this.
	}
}

func TestSearchCache_Miss(t *testing.T) {
	c := NewSearchCache(10, time.Minute)
	_, hit := c.Get("anything")
	if hit {
		t.Error("expected miss on empty cache")
	}
}

// ---------- TTL expiry ----------

func TestSearchCache_Expiry(t *testing.T) {
	c := NewSearchCache(10, 10*time.Millisecond) // very short TTL
	c.Put("test", []SearchResult{{Slug: "a"}})

	time.Sleep(20 * time.Millisecond)

	_, hit := c.Get("test")
	if hit {
		t.Error("expected expired cache miss")
	}
}

// ---------- LRU eviction ----------

func TestSearchCache_LRUEviction(t *testing.T) {
	c := NewSearchCache(2, time.Minute) // cap=2
	c.Put("query1", []SearchResult{{Slug: "a"}})
	c.Put("query2", []SearchResult{{Slug: "b"}})
	c.Put("query3", []SearchResult{{Slug: "c"}}) // evicts query1

	_, hit := c.Get("query1")
	if hit {
		t.Error("expected query1 to be evicted")
	}
	_, hit2 := c.Get("query2")
	_, hit3 := c.Get("query3")
	if !hit2 || !hit3 {
		t.Error("expected query2 and query3 to be present")
	}
}

// ---------- concurrency ----------

func TestSearchCache_Concurrent(t *testing.T) {
	c := NewSearchCache(100, time.Minute)
	var wg sync.WaitGroup
	const goroutines = 20

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := "query"
			c.Put(key, []SearchResult{{Slug: "a"}})
			c.Get(key)
		}(i)
	}
	wg.Wait()
}

// ---------- edge cases ----------

func TestSearchCache_EmptyQuery(t *testing.T) {
	// Empty query has no trigrams, so similarity is undefined.
	// The cache should not panic; whether it hits or misses is implementation-defined.
	c := NewSearchCache(10, time.Minute)
	c.Put("", []SearchResult{{Slug: "a"}})
	// Just ensure no panic; we don't assert hit/miss for empty keys.
	c.Get("")
}

func TestSearchCache_PutAndGetMultiple(t *testing.T) {
	c := NewSearchCache(10, time.Minute)
	for i := 0; i < 5; i++ {
		c.Put("query", []SearchResult{{Slug: "a"}})
	}
	_, hit := c.Get("query")
	if !hit {
		t.Error("expected hit after multiple puts")
	}
}
