package auth

import (
	"sync"
)

// GateFunc defines the callback for checking permission.
type GateFunc func(user Authenticatable, args ...any) bool

// GateManager manages authorization gates and policies.
type GateManager struct {
	mu    sync.RWMutex
	gates map[string]GateFunc
}

// Global default gate manager
var globalGate = NewGateManager()

// NewGateManager creates a new GateManager.
func NewGateManager() *GateManager {
	return &GateManager{
		gates: make(map[string]GateFunc),
	}
}

// Define registers an authorization gate by name.
func (g *GateManager) Define(ability string, fn GateFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gates[ability] = fn
}

// Allows checks whether the user is authorized to perform the given ability.
func (g *GateManager) Allows(ability string, user Authenticatable, args ...any) bool {
	g.mu.RLock()
	fn, exists := g.gates[ability]
	g.mu.RUnlock()

	if !exists {
		return false
	}
	return fn(user, args...)
}

// Denies checks whether the user is NOT authorized.
func (g *GateManager) Denies(ability string, user Authenticatable, args ...any) bool {
	return !g.Allows(ability, user, args...)
}

// Global helpers
func Define(ability string, fn GateFunc)                        { globalGate.Define(ability, fn) }
func Allows(ability string, user Authenticatable, args ...any) bool { return globalGate.Allows(ability, user, args...) }
func Denies(ability string, user Authenticatable, args ...any) bool { return globalGate.Denies(ability, user, args...) }
