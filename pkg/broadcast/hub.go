package broadcast

import (
	"sync"
)

// Message represents a broadcasted realtime event.
type Message struct {
	Channel string         `json:"channel"`
	Event   string         `json:"event"`
	Data    map[string]any `json:"data"`
}

type subscriber struct {
	ch   chan *Message
	quit chan struct{}
}

// Hub manages pub/sub channels and dispatches events to WebSocket & SSE clients.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]map[*subscriber]struct{}
}

// Global default broadcast hub
var globalHub = NewHub()

// NewHub creates a new Broadcast Hub.
func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[string]map[*subscriber]struct{}),
	}
}

// Subscribe registers a listener on the specified channel.
// Returns a channel of messages and an unsubscribe cleanup function.
func (h *Hub) Subscribe(channel string) (<-chan *Message, func()) {
	sub := &subscriber{
		ch:   make(chan *Message, 64),
		quit: make(chan struct{}),
	}

	h.mu.Lock()
	if h.subscribers[channel] == nil {
		h.subscribers[channel] = make(map[*subscriber]struct{})
	}
	h.subscribers[channel][sub] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if subs, ok := h.subscribers[channel]; ok {
			delete(subs, sub)
			if len(subs) == 0 {
				delete(h.subscribers, channel)
			}
		}
		close(sub.quit)
	}

	return sub.ch, unsubscribe
}

// Broadcast sends an event payload to all clients subscribed to channel.
func (h *Hub) Broadcast(channel, event string, data map[string]any) {
	h.mu.RLock()
	subsMap := h.subscribers[channel]
	if len(subsMap) == 0 {
		h.mu.RUnlock()
		return
	}

	// Create message
	msg := &Message{
		Channel: channel,
		Event:   event,
		Data:    data,
	}

	// Copy subscriber list to avoid holding lock during channel dispatch
	subs := make([]*subscriber, 0, len(subsMap))
	for s := range subsMap {
		subs = append(subs, s)
	}
	h.mu.RUnlock()

	for _, s := range subs {
		select {
		case s.ch <- msg:
		case <-s.quit:
		default:
			// Non-blocking drop if slow consumer buffer is full
		}
	}
}

// Global helpers
func Subscribe(channel string) (<-chan *Message, func()) { return globalHub.Subscribe(channel) }
func Broadcast(channel, event string, data map[string]any) { globalHub.Broadcast(channel, event, data) }
func DefaultHub() *Hub                                   { return globalHub }
