package cache

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// LockProvider handles acquiring and releasing named locks.
type LockProvider interface {
	Acquire(name, owner string, duration time.Duration) (bool, error)
	Release(name, owner string) error
}

// AtomicLock represents an atomic, distributed or in-process concurrency lock.
type AtomicLock struct {
	provider LockProvider
	name     string
	owner    string
	duration time.Duration
}

// NewLock creates a new lock instance with an owner identifier.
func NewLock(provider LockProvider, name string, duration time.Duration, owner ...string) *AtomicLock {
	o := generateOwnerID()
	if len(owner) > 0 && owner[0] != "" {
		o = owner[0]
	}

	return &AtomicLock{
		provider: provider,
		name:     name,
		owner:    o,
		duration: duration,
	}
}

// Name returns the lock name.
func (l *AtomicLock) Name() string {
	return l.name
}

// Owner returns the owner token assigned to this lock.
func (l *AtomicLock) Owner() string {
	return l.owner
}

// Acquire attempts to acquire the lock immediately.
func (l *AtomicLock) Acquire() (bool, error) {
	return l.provider.Acquire(l.name, l.owner, l.duration)
}

// Release releases the lock if owned by the current process.
func (l *AtomicLock) Release() error {
	return l.provider.Release(l.name, l.owner)
}

// Get attempts to acquire the lock and executes the provided function,
// automatically releasing the lock when finished (even if an error occurs).
func (l *AtomicLock) Get(fn func() error) error {
	acquired, err := l.Acquire()
	if err != nil {
		return err
	}
	if !acquired {
		return ErrLockNotAcquired
	}

	defer func() {
		_ = l.Release()
	}()

	return fn()
}

// Block repeatedly attempts to acquire the lock until the timeout is reached.
// If fn is provided, it executes the callback and automatically releases the lock.
func (l *AtomicLock) Block(timeout time.Duration, fn ...func() error) error {
	start := time.Now()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		acquired, err := l.Acquire()
		if err != nil {
			return err
		}
		if acquired {
			if len(fn) > 0 && fn[0] != nil {
				defer func() {
					_ = l.Release()
				}()
				return fn[0]()
			}
			return nil
		}

		if time.Since(start) >= timeout {
			return ErrLockTimeout
		}

		<-ticker.C
	}
}

// --- Memory Lock Provider ---

type memoryLockItem struct {
	owner     string
	expiresAt time.Time
}

// MemoryLockProvider implements in-process atomic locks.
type MemoryLockProvider struct {
	mu    sync.Mutex
	locks map[string]*memoryLockItem
}

// NewMemoryLockProvider creates an in-process lock manager.
func NewMemoryLockProvider() *MemoryLockProvider {
	return &MemoryLockProvider{
		locks: make(map[string]*memoryLockItem),
	}
}

func (p *MemoryLockProvider) Acquire(name, owner string, duration time.Duration) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	item, exists := p.locks[name]

	// Lock is currently held and not expired
	if exists && now.Before(item.expiresAt) {
		if item.owner == owner {
			// Same owner: refresh expiration
			item.expiresAt = now.Add(duration)
			return true, nil
		}
		return false, nil
	}

	// Unlocked or expired: acquire lock
	p.locks[name] = &memoryLockItem{
		owner:     owner,
		expiresAt: now.Add(duration),
	}
	return true, nil
}

func (p *MemoryLockProvider) Release(name, owner string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, exists := p.locks[name]
	if !exists {
		return nil
	}

	if item.owner != owner {
		return ErrLockNotOwned
	}

	delete(p.locks, name)
	return nil
}

func generateOwnerID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
