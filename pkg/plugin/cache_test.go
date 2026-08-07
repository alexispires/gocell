package plugin_test

import (
	"sync"
	"testing"

	"github.com/alexispires/gocell/pkg/plugin"
)

func TestCacheGetMissOnEmptyCache(t *testing.T) {
	c := plugin.NewCache()
	if _, ok := c.Get("nonexistent"); ok {
		t.Fatalf("Expected a miss on an empty cache")
	}
}

func TestCachePutThenGetReturnsTheSameValue(t *testing.T) {
	c := plugin.NewCache()
	c.Put("hash1", "/tmp/cell_1.so")

	soPath, ok := c.Get("hash1")
	if !ok {
		t.Fatalf("Expected a hit after Put")
	}
	if soPath != "/tmp/cell_1.so" {
		t.Fatalf("Expected '/tmp/cell_1.so', got %q", soPath)
	}
}

func TestCachePutOverwritesAnExistingHash(t *testing.T) {
	c := plugin.NewCache()
	c.Put("hash1", "/tmp/cell_1.so")
	c.Put("hash1", "/tmp/cell_1_rebuilt.so")

	soPath, ok := c.Get("hash1")
	if !ok || soPath != "/tmp/cell_1_rebuilt.so" {
		t.Fatalf("Expected the second Put to overwrite the first, got soPath=%q ok=%v", soPath, ok)
	}
}

// Regression guard for the Cache's stated thread-safety (its RWMutex): concurrent Put/Get on
// distinct keys must not race or corrupt the map. Run with `go test -race` to be meaningful.
func TestCacheConcurrentPutAndGet(t *testing.T) {
	c := plugin.NewCache()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			hash := plugin.ComputeHash("code", string(rune(i)))
			c.Put(hash, "/tmp/cell.so")
			c.Get(hash)
		}(i)
	}
	wg.Wait()
}

func TestComputeHashIsDeterministic(t *testing.T) {
	h1 := plugin.ComputeHash("x := 1", "sig-a")
	h2 := plugin.ComputeHash("x := 1", "sig-a")
	if h1 != h2 {
		t.Fatalf("Expected identical inputs to hash identically, got %q vs %q", h1, h2)
	}
}

func TestComputeHashDiffersWhenCodeDiffers(t *testing.T) {
	h1 := plugin.ComputeHash("x := 1", "sig-a")
	h2 := plugin.ComputeHash("x := 2", "sig-a")
	if h1 == h2 {
		t.Fatalf("Expected different code to hash differently")
	}
}

func TestComputeHashDiffersWhenStateSignatureDiffers(t *testing.T) {
	h1 := plugin.ComputeHash("x := 1", "sig-a")
	h2 := plugin.ComputeHash("x := 1", "sig-b")
	if h1 == h2 {
		t.Fatalf("Expected different state signatures to hash differently, even with identical code")
	}
}

// Regression guard: ComputeHash joins code and stateSig with a "::" separator specifically so
// that ("ab", "c") and ("a", "bc") -- which would collide under naive concatenation -- hash
// differently.
func TestComputeHashDoesNotCollideAcrossTheCodeStateSigBoundary(t *testing.T) {
	h1 := plugin.ComputeHash("ab", "c")
	h2 := plugin.ComputeHash("a", "bc")
	if h1 == h2 {
		t.Fatalf("Expected no collision across the code/stateSig boundary")
	}
}
