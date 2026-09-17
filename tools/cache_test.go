package tools

import (
	"fmt"
	"testing"
	"time"
)

func TestCacheGetSet(t *testing.T) {
	c := NewCache(10)
	if _, ok := c.Get("missing"); ok {
		t.Fatal("empty cache should miss")
	}

	c.Set("k", []byte("v"), time.Minute)
	got, ok := c.Get("k")
	if !ok || string(got) != "v" {
		t.Fatalf("Get = %q, %v; want \"v\", true", got, ok)
	}
	if c.Size() != 1 {
		t.Fatalf("Size = %d, want 1", c.Size())
	}
}

func TestCacheExpiry(t *testing.T) {
	c := NewCache(10)
	c.Set("k", []byte("v"), -time.Second) // already expired
	if _, ok := c.Get("k"); ok {
		t.Fatal("expired entry must not be returned")
	}
	if c.Size() != 0 {
		t.Fatalf("Size = %d, want 0 after expiry", c.Size())
	}
}

func TestCacheInvalidatePrefix(t *testing.T) {
	c := NewCache(10)
	c.Set("search:melk:10", []byte("a"), time.Minute)
	c.Set("search:kaas:10", []byte("b"), time.Minute)
	c.Set("product:123", []byte("c"), time.Minute)

	c.Invalidate("search:")

	if _, ok := c.Get("search:melk:10"); ok {
		t.Fatal("prefixed entry should have been invalidated")
	}
	if _, ok := c.Get("product:123"); !ok {
		t.Fatal("non-matching entry should survive invalidation")
	}
}

// A long-running server must not grow the cache without bound.
func TestCacheEvictsWhenFull(t *testing.T) {
	const max = 8
	c := NewCache(max)
	for i := 0; i < max*4; i++ {
		c.Set(fmt.Sprintf("key:%d", i), []byte("v"), time.Minute)
	}
	if got := c.Size(); got > max {
		t.Fatalf("Size = %d, want at most %d", got, max)
	}
}

func TestCacheEvictsExpiredFirst(t *testing.T) {
	c := NewCache(2)
	c.Set("stale", []byte("v"), -time.Second)
	c.Set("fresh", []byte("v"), time.Minute)
	c.Set("new", []byte("v"), time.Minute)

	if _, ok := c.Get("fresh"); !ok {
		t.Fatal("a live entry was evicted while an expired one remained")
	}
	if _, ok := c.Get("new"); !ok {
		t.Fatal("newly inserted entry missing")
	}
}

func TestCacheOverwriteDoesNotEvict(t *testing.T) {
	c := NewCache(2)
	c.Set("a", []byte("1"), time.Minute)
	c.Set("b", []byte("1"), time.Minute)
	c.Set("a", []byte("2"), time.Minute) // overwrite, not a new key

	got, ok := c.Get("a")
	if !ok || string(got) != "2" {
		t.Fatalf("a = %q, %v; want \"2\", true", got, ok)
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("overwriting an existing key should not evict another")
	}
}

func TestCacheKeysAreDistinct(t *testing.T) {
	if SearchCacheKey("melk", 10) == SearchCacheKey("melk", 20) {
		t.Fatal("search keys must include the limit")
	}
	if ProductCacheKey(1) == ProductFullCacheKey(1) {
		t.Fatal("plain and full product keys must differ")
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	c := NewCache(64)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 200; j++ {
				key := fmt.Sprintf("k:%d:%d", i, j)
				c.Set(key, []byte("v"), time.Minute)
				c.Get(key)
				c.Size()
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
