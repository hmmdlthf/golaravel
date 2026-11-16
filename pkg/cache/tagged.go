package cache

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// TagManager coordinates tag versions and reverse indices for O(1) tag invalidation.
type TagManager struct {
	mu          sync.RWMutex
	tagVersions map[string]int64
	tagKeys     map[string]map[string]struct{}
	// shared, when set, holds tag versions outside this process (in the
	// cache backend), so a flush on one instance reaches all of them.
	shared tagVersionSource
}

type tagVersionSource interface {
	version(tag string) int64
	bump(tag string)
}

func newSharedTagManager(src tagVersionSource) *TagManager {
	tm := NewTagManager()
	tm.shared = src
	return tm
}

func (tm *TagManager) sharedKey(tags []string, key string) string {
	sortedTags := make([]string, len(tags))
	copy(sortedTags, tags)
	sort.Strings(sortedTags)
	var sb strings.Builder
	sb.WriteString("tag:")
	for _, t := range sortedTags {
		sb.WriteString(t)
		sb.WriteString(":")
		sb.WriteString(fmt.Sprint(tm.shared.version(t)))
		sb.WriteString(":")
	}
	sb.WriteString("key:")
	sb.WriteString(key)
	return sb.String()
}

// NewTagManager creates a tag manager.
func NewTagManager() *TagManager {
	return &TagManager{
		tagVersions: make(map[string]int64),
		tagKeys:     make(map[string]map[string]struct{}),
	}
}

// TaggedKey generates an O(1) versioned cache key incorporating the current versions of all tags.
func (tm *TagManager) TaggedKey(tags []string, key string) string {
	if tm.shared != nil {
		return tm.sharedKey(tags, key)
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()

	sortedTags := make([]string, len(tags))
	copy(sortedTags, tags)
	sort.Strings(sortedTags)

	var sb strings.Builder
	sb.WriteString("tag:")
	for _, t := range sortedTags {
		v, exists := tm.tagVersions[t]
		if !exists {
			v = 1
			tm.tagVersions[t] = 1
		}
		sb.WriteString(t)
		sb.WriteString(":")
		sb.WriteString(fmt.Sprint(v))
		sb.WriteString(":")
	}
	sb.WriteString("key:")
	sb.WriteString(key)

	taggedKey := sb.String()

	// Record reverse key mapping for memory pruning on flush
	for _, t := range sortedTags {
		if tm.tagKeys[t] == nil {
			tm.tagKeys[t] = make(map[string]struct{})
		}
		tm.tagKeys[t][taggedKey] = struct{}{}
	}

	return taggedKey
}

// ReadTaggedKey generates the versioned key for reading without modifying the tag mapping.
func (tm *TagManager) ReadTaggedKey(tags []string, key string) string {
	if tm.shared != nil {
		return tm.sharedKey(tags, key)
	}
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	sortedTags := make([]string, len(tags))
	copy(sortedTags, tags)
	sort.Strings(sortedTags)

	var sb strings.Builder
	sb.WriteString("tag:")
	for _, t := range sortedTags {
		v := tm.tagVersions[t]
		if v == 0 {
			v = 1
		}
		sb.WriteString(t)
		sb.WriteString(":")
		sb.WriteString(fmt.Sprint(v))
		sb.WriteString(":")
	}
	sb.WriteString("key:")
	sb.WriteString(key)

	return sb.String()
}

// FlushTag increments the tag version to immediately invalidate all current tagged keys in O(1)
// and returns all mapped keys for underlying memory eviction.
func (tm *TagManager) FlushTag(tag string) []string {
	if tm.shared != nil {
		// Old entries are unreachable after the bump and expire with
		// their TTL.
		tm.shared.bump(tag)
		return nil
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.tagVersions[tag]++

	keysSet := tm.tagKeys[tag]
	if keysSet == nil {
		return nil
	}

	keys := make([]string, 0, len(keysSet))
	for k := range keysSet {
		keys = append(keys, k)
	}
	delete(tm.tagKeys, tag)

	return keys
}

// TaggedStore represents a cache store scoped to one or more tags.
type TaggedStore struct {
	store      Store
	tagManager *TagManager
	tags       []string
}

// NewTaggedStore creates a tagged store instance.
func NewTaggedStore(store Store, tm *TagManager, tags ...string) *TaggedStore {
	cleanTags := make([]string, 0, len(tags))
	seen := make(map[string]struct{})
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" {
			if _, exists := seen[t]; !exists {
				seen[t] = struct{}{}
				cleanTags = append(cleanTags, t)
			}
		}
	}
	sort.Strings(cleanTags)

	return &TaggedStore{
		store:      store,
		tagManager: tm,
		tags:       cleanTags,
	}
}

// Tags chains additional tags onto the current tagged store.
func (ts *TaggedStore) Tags(names ...string) *TaggedStore {
	combined := append(ts.tags, names...)
	return NewTaggedStore(ts.store, ts.tagManager, combined...)
}

// Put stores an item under the scoped tags.
func (ts *TaggedStore) Put(key string, val any, ttl time.Duration) error {
	taggedKey := ts.tagManager.TaggedKey(ts.tags, key)
	return ts.store.Put(taggedKey, val, ttl)
}

// Add stores an item if the key does not already exist.
func (ts *TaggedStore) Add(key string, val any, ttl time.Duration) (bool, error) {
	taggedKey := ts.tagManager.TaggedKey(ts.tags, key)
	return ts.store.Add(taggedKey, val, ttl)
}

// Get retrieves an item under the scoped tags.
func (ts *TaggedStore) Get(key string) (any, bool) {
	taggedKey := ts.tagManager.ReadTaggedKey(ts.tags, key)
	return ts.store.Get(taggedKey)
}

// GetString retrieves a string attribute.
func (ts *TaggedStore) GetString(key string, defaultVal ...string) string {
	taggedKey := ts.tagManager.ReadTaggedKey(ts.tags, key)
	return ts.store.GetString(taggedKey, defaultVal...)
}

// GetInt retrieves an integer attribute.
func (ts *TaggedStore) GetInt(key string, defaultVal ...int) int {
	taggedKey := ts.tagManager.ReadTaggedKey(ts.tags, key)
	return ts.store.GetInt(taggedKey, defaultVal...)
}

// GetBool retrieves a boolean attribute.
func (ts *TaggedStore) GetBool(key string, defaultVal ...bool) bool {
	taggedKey := ts.tagManager.ReadTaggedKey(ts.tags, key)
	return ts.store.GetBool(taggedKey, defaultVal...)
}

// Remember retrieves an item or computes and caches it under the scoped tags.
func (ts *TaggedStore) Remember(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	if val, ok := ts.Get(key); ok {
		return val, nil
	}

	val, err := fn()
	if err != nil {
		return nil, err
	}

	_ = ts.Put(key, val, ttl)
	return val, nil
}

// RememberForever stores an item indefinitely under the scoped tags.
func (ts *TaggedStore) RememberForever(key string, fn func() (any, error)) (any, error) {
	return ts.Remember(key, 0, fn)
}

// Forget removes a specific key under the scoped tags.
func (ts *TaggedStore) Forget(key string) error {
	taggedKey := ts.tagManager.ReadTaggedKey(ts.tags, key)
	return ts.store.Forget(taggedKey)
}

// Flush invalidates ALL items associated with ANY of the scoped tags.
func (ts *TaggedStore) Flush() error {
	for _, tag := range ts.tags {
		keysToEvict := ts.tagManager.FlushTag(tag)
		for _, k := range keysToEvict {
			_ = ts.store.Forget(k)
		}
	}
	return nil
}
