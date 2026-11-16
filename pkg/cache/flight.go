package cache

import (
	"sync"
	"time"
)

// flightGroup lets concurrent callers for one key share a single
// computation (a minimal singleflight).
type flightGroup struct {
	mu    sync.Mutex
	calls map[string]*flight
}

type flight struct {
	wg  sync.WaitGroup
	val any
	err error
}

func (g *flightGroup) do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = map[string]*flight{}
	}
	if f, ok := g.calls[key]; ok {
		g.mu.Unlock()
		f.wg.Wait()
		return f.val, f.err
	}
	f := &flight{}
	f.wg.Add(1)
	g.calls[key] = f
	g.mu.Unlock()

	f.val, f.err = fn()
	f.wg.Done()

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()
	return f.val, f.err
}

// RememberLockTimeout is how long a shared-store Remember waits for
// another instance computing the same key before computing it itself.
var RememberLockTimeout = 30 * time.Second
