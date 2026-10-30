package ratelimit

import (
	"sync"
	"time"
)

// MemoryStore implements an in-memory sliding-window log rate limiter.
type MemoryStore struct {
	mu       sync.RWMutex
	records  map[string][]int64 // key -> list of unix nano timestamps
	stopChan chan struct{}
}

// NewMemoryStore initializes a new sliding-window memory store with automated background eviction.
func NewMemoryStore(cleanupInterval ...time.Duration) *MemoryStore {
	interval := 1 * time.Minute
	if len(cleanupInterval) > 0 && cleanupInterval[0] > 0 {
		interval = cleanupInterval[0]
	}

	s := &MemoryStore{
		records:  make(map[string][]int64, 1024),
		stopChan: make(chan struct{}),
	}

	go s.evictionLoop(interval)
	return s
}

// Hit records an attempt at now and calculates the sliding window count.
func (s *MemoryStore) Hit(key string, maxAttempts int, window time.Duration) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	nowNano := now.UnixNano()
	windowNano := window.Nanoseconds()
	cutoffNano := nowNano - windowNano

	// Filter timestamps strictly inside [now - window, now]
	timestamps := s.records[key]
	valid := make([]int64, 0, len(timestamps)+1)
	for _, ts := range timestamps {
		if ts > cutoffNano {
			valid = append(valid, ts)
		}
	}

	currentCount := len(valid)

	// If already at or above limit, reject without recording new timestamp
	if currentCount >= maxAttempts {
		oldestNano := valid[0]
		resetNano := oldestNano + windowNano
		retryAfterNano := resetNano - nowNano
		if retryAfterNano < 0 {
			retryAfterNano = 0
		}

		s.records[key] = valid
		return Result{
			Allowed:    false,
			Limit:      maxAttempts,
			Remaining:  0,
			ResetTime:  time.Unix(0, resetNano),
			RetryAfter: time.Duration(retryAfterNano),
		}, nil
	}

	// Under limit: record this attempt
	valid = append(valid, nowNano)
	s.records[key] = valid

	newCount := len(valid)
	remaining := maxAttempts - newCount
	oldestNano := valid[0]
	resetNano := oldestNano + windowNano

	return Result{
		Allowed:    true,
		Limit:      maxAttempts,
		Remaining:  remaining,
		ResetTime:  time.Unix(0, resetNano),
		RetryAfter: 0,
	}, nil
}

// Status inspects the current rate limit without appending a new attempt.
func (s *MemoryStore) Status(key string, maxAttempts int, window time.Duration) (Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	nowNano := now.UnixNano()
	windowNano := window.Nanoseconds()
	cutoffNano := nowNano - windowNano

	timestamps := s.records[key]
	var validCount int
	var oldestNano int64 = 0

	for _, ts := range timestamps {
		if ts > cutoffNano {
			if oldestNano == 0 {
				oldestNano = ts
			}
			validCount++
		}
	}

	if validCount >= maxAttempts {
		resetNano := oldestNano + windowNano
		retryAfterNano := resetNano - nowNano
		if retryAfterNano < 0 {
			retryAfterNano = 0
		}
		return Result{
			Allowed:    false,
			Limit:      maxAttempts,
			Remaining:  0,
			ResetTime:  time.Unix(0, resetNano),
			RetryAfter: time.Duration(retryAfterNano),
		}, nil
	}

	remaining := maxAttempts - validCount
	var resetNano int64
	if oldestNano > 0 {
		resetNano = oldestNano + windowNano
	} else {
		resetNano = nowNano + windowNano
	}

	return Result{
		Allowed:    true,
		Limit:      maxAttempts,
		Remaining:  remaining,
		ResetTime:  time.Unix(0, resetNano),
		RetryAfter: 0,
	}, nil
}

// Reset clears recorded timestamps for a key.
func (s *MemoryStore) Reset(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, key)
	return nil
}

// Clear flushes all keys from memory.
func (s *MemoryStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = make(map[string][]int64, 1024)
	return nil
}

// Close stops the background eviction ticker.
func (s *MemoryStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.stopChan:
		return
	default:
		close(s.stopChan)
	}
}

func (s *MemoryStore) evictionLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case now := <-ticker.C:
			s.pruneStale(now)
		}
	}
}

func (s *MemoryStore) pruneStale(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nowNano := now.UnixNano()
	// Prune any timestamps older than 24 hours
	cutoffNano := nowNano - int64(24*time.Hour)

	for key, timestamps := range s.records {
		var valid []int64
		for _, ts := range timestamps {
			if ts > cutoffNano {
				valid = append(valid, ts)
			}
		}
		if len(valid) == 0 {
			delete(s.records, key)
		} else if len(valid) < len(timestamps) {
			s.records[key] = valid
		}
	}
}
