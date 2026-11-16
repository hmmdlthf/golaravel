package events

import (
	"log/slog"
	"sync"
)

// Listener represents an event subscriber callback.
type Listener func(payload any) error

// Dispatcher manages event listeners and event broadcasting.
type Dispatcher struct {
	mu        sync.RWMutex
	listeners map[string][]Listener
}

// Global default event dispatcher
var globalDispatcher = NewDispatcher()

// NewDispatcher creates a new Event Dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		listeners: make(map[string][]Listener, 32),
	}
}

// Listen registers a listener callback for an event name.
func (d *Dispatcher) Listen(eventName string, listener Listener) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listeners[eventName] = append(d.listeners[eventName], listener)
}

// Dispatch executes all registered listeners synchronously.
func (d *Dispatcher) Dispatch(eventName string, payload any) error {
	d.mu.RLock()
	handlers := d.listeners[eventName]
	d.mu.RUnlock()

	for _, handler := range handlers {
		if err := handler(payload); err != nil {
			return err
		}
	}
	return nil
}

// DispatchAsync executes listeners asynchronously in background goroutines.
func (d *Dispatcher) DispatchAsync(eventName string, payload any) {
	d.mu.RLock()
	handlers := make([]Listener, len(d.listeners[eventName]))
	copy(handlers, d.listeners[eventName])
	d.mu.RUnlock()

	go func() {
		// A panicking listener must not crash the process.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("event listener panicked", "event", eventName, "panic", r)
			}
		}()
		for _, handler := range handlers {
			if err := handler(payload); err != nil {
				slog.Error("event listener error", "event", eventName, "error", err)
			}
		}
	}()
}

// Global helper functions
func Listen(eventName string, listener Listener) { globalDispatcher.Listen(eventName, listener) }
func Dispatch(eventName string, payload any) error {
	return globalDispatcher.Dispatch(eventName, payload)
}
func DispatchAsync(eventName string, payload any) { globalDispatcher.DispatchAsync(eventName, payload) }
