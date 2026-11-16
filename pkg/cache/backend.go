package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Backend is the small set of primitives a shared cache store needs. The
// SQL and Redis stores implement it; BackendStore builds the full Store API
// (typed getters, Remember, Pull, tags, locks) on top.
type Backend interface {
	LockProvider
	GetRaw(key string) ([]byte, bool, error)
	SetRaw(key string, val []byte, ttl time.Duration) error
	// AddRaw stores val only if key is missing or expired.
	AddRaw(key string, val []byte, ttl time.Duration) (bool, error)
	// IncrBy adds by to the integer at key (missing counts as 0) and keeps
	// its expiry.
	IncrBy(key string, by int64) (int64, error)
	Delete(key string) error
	Flush() error
}

// BackendStore is a Store over a Backend. Values are stored as JSON, so
// Get returns them JSON-decoded: strings, bools, int64 (whole numbers),
// float64, map[string]any and []any. Tag versions live in the backend, so
// tag flushes reach every instance sharing it.
type BackendStore struct {
	b       Backend
	tags    *TagManager
	flights flightGroup
}

// Puller is implemented by backends that can read and delete a key in one
// atomic step; BackendStore.Pull uses it so two instances never both get
// the same value.
type Puller interface {
	PullRaw(key string) ([]byte, bool, error)
}

// NewBackendStore wraps a Backend into a Store.
func NewBackendStore(b Backend) *BackendStore {
	s := &BackendStore{b: b}
	s.tags = newSharedTagManager(backendTagVersions{b})
	return s
}

func encodeValue(v any) ([]byte, error) { return json.Marshal(v) }

func decodeValue(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return normalize(v), nil
}

// normalize turns json.Number into int64 (whole numbers) or float64.
func normalize(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = normalize(e)
		}
	case []any:
		for i, e := range x {
			x[i] = normalize(e)
		}
	}
	return v
}

func (s *BackendStore) Get(key string) (any, bool) {
	raw, ok, err := s.b.GetRaw(key)
	if err != nil || !ok {
		return nil, false
	}
	v, err := decodeValue(raw)
	if err != nil {
		return nil, false
	}
	return v, true
}

func (s *BackendStore) GetString(key string, defaultVal ...string) string {
	if v, ok := s.Get(key); ok {
		switch x := v.(type) {
		case string:
			return x
		case nil:
		default:
			return fmt.Sprint(x)
		}
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return ""
}

func (s *BackendStore) GetInt(key string, defaultVal ...int) int {
	if v, ok := s.Get(key); ok {
		switch x := v.(type) {
		case int64:
			return int(x)
		case float64:
			return int(x)
		case string:
			if i, err := strconv.Atoi(x); err == nil {
				return i
			}
		}
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return 0
}

func (s *BackendStore) GetBool(key string, defaultVal ...bool) bool {
	if v, ok := s.Get(key); ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return false
}

func (s *BackendStore) Put(key string, val any, ttl time.Duration) error {
	raw, err := encodeValue(val)
	if err != nil {
		return err
	}
	return s.b.SetRaw(key, raw, ttl)
}

func (s *BackendStore) Add(key string, val any, ttl time.Duration) (bool, error) {
	raw, err := encodeValue(val)
	if err != nil {
		return false, err
	}
	return s.b.AddRaw(key, raw, ttl)
}

func (s *BackendStore) Increment(key string, value ...int64) (int64, error) {
	by := int64(1)
	if len(value) > 0 {
		by = value[0]
	}
	return s.b.IncrBy(key, by)
}

func (s *BackendStore) Decrement(key string, value ...int64) (int64, error) {
	by := int64(1)
	if len(value) > 0 {
		by = value[0]
	}
	return s.b.IncrBy(key, -by)
}

func (s *BackendStore) Forget(key string) error { return s.b.Delete(key) }

func (s *BackendStore) Flush() error { return s.b.Flush() }

// Remember returns the cached value or computes, stores and returns it.
//
// Stampede protection: callers in this process that miss the same key
// share one computation, and across instances a lock in the backend lets
// one compute while the others wait (up to RememberLockTimeout) for its
// result.
func (s *BackendStore) Remember(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	if v, ok := s.Get(key); ok {
		return v, nil
	}
	return s.flights.do(key, func() (any, error) {
		lock := s.Lock("remember:"+key, RememberLockTimeout)
		got, _ := lock.Acquire()
		if got {
			defer func() { _ = lock.Release() }()
		} else {
			// Another instance is computing it: wait for its result.
			deadline := time.Now().Add(RememberLockTimeout)
			for wait := 10 * time.Millisecond; time.Now().Before(deadline); {
				if v, ok := s.Get(key); ok {
					return v, nil
				}
				if ok, _ := lock.Acquire(); ok {
					defer func() { _ = lock.Release() }()
					break
				}
				time.Sleep(wait)
				if wait < 200*time.Millisecond {
					wait *= 2
				}
			}
		}
		if v, ok := s.Get(key); ok {
			return v, nil
		}
		v, err := fn()
		if err != nil {
			return nil, err
		}
		if err := s.Put(key, v, ttl); err != nil {
			return nil, err
		}
		// Return what later reads will see (JSON-decoded), not fn's value.
		if got, ok := s.Get(key); ok {
			return got, nil
		}
		return v, nil
	})
}

func (s *BackendStore) RememberForever(key string, fn func() (any, error)) (any, error) {
	return s.Remember(key, 0, fn)
}

// Pull reads and deletes a value. With the SQL and Redis backends this is
// atomic: when several instances pull the same key, only one gets it.
func (s *BackendStore) Pull(key string, defaultVal ...any) any {
	if p, ok := s.b.(Puller); ok {
		if raw, found, err := p.PullRaw(key); err == nil && found {
			if v, err := decodeValue(raw); err == nil {
				return v
			}
		}
	} else if v, ok := s.Get(key); ok {
		_ = s.b.Delete(key)
		return v
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return nil
}

func (s *BackendStore) Tags(names ...string) *TaggedStore {
	return NewTaggedStore(s, s.tags, names...)
}

func (s *BackendStore) Lock(name string, duration time.Duration, owner ...string) *AtomicLock {
	return NewLock(s.b, name, duration, owner...)
}

// backendTagVersions keeps tag versions as counters in the backend.
type backendTagVersions struct{ b Backend }

func (v backendTagVersions) version(tag string) int64 {
	raw, ok, err := v.b.GetRaw("tagver:" + tag)
	if err != nil || !ok {
		return 1
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return 1
	}
	return n + 1
}

func (v backendTagVersions) bump(tag string) { _, _ = v.b.IncrBy("tagver:"+tag, 1) }

var _ Store = (*BackendStore)(nil)
