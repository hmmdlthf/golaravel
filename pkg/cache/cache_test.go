package cache

import (
	"testing"
	"time"
)

func TestCache_PutAndGet(t *testing.T) {
	c := NewMemoryCache()
	c.Put("key1", "value1", 1*time.Minute)

	val, found := c.Get("key1")
	if !found {
		t.Fatal("expected key1 to be found")
	}
	if val.(string) != "value1" {
		t.Fatalf("expected 'value1', got %v", val)
	}
}

func TestCache_Remember(t *testing.T) {
	c := NewMemoryCache()
	counter := 0

	val, err := c.Remember("computed_key", 1*time.Minute, func() (any, error) {
		counter++
		return "expensive_result", nil
	})
	if err != nil || val.(string) != "expensive_result" {
		t.Fatalf("unexpected result: %v, err: %v", val, err)
	}

	// Second fetch should use cached result
	val2, _ := c.Remember("computed_key", 1*time.Minute, func() (any, error) {
		counter++
		return "expensive_result", nil
	})
	if val2.(string) != "expensive_result" || counter != 1 {
		t.Fatalf("expected cached value without calling computation again, counter=%d", counter)
	}
}

func TestCache_TTL_Expiration(t *testing.T) {
	c := NewMemoryCache()
	c.Put("temp", "expiring", 10*time.Millisecond)

	time.Sleep(20 * time.Millisecond)

	_, found := c.Get("temp")
	if found {
		t.Fatal("expected expired key to not be found")
	}
}
