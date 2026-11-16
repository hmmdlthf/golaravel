package broadcast

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/gorilla/websocket"
)

// ClientCommand represents a message sent from the WebSocket client to the server.
type ClientCommand struct {
	Action  string `json:"action"`  // "subscribe", "unsubscribe", "ping"
	Channel string `json:"channel"` // channel name
}

// WebSocketHandler returns a handler that upgrades the connection to full-duplex WebSocket.
//
// Browser connections are accepted from the page's own origin and from
// Options.AllowedOrigins only (see WebSocketHandlerWith), so another site
// cannot open a WebSocket with the user's cookies. Clients that send no
// Origin (native apps) are accepted. Every subscription is authorized (see
// Channel).
func WebSocketHandler(h ...*Hub) context.HandlerFunc {
	opts := Options{}
	if len(h) > 0 {
		opts.Hub = h[0]
	}
	return WebSocketHandlerWith(opts)
}

// WebSocketHandlerWith is WebSocketHandler with options.
func WebSocketHandlerWith(opts Options) context.HandlerFunc {
	hub := opts.hub()
	maxSubs := opts.MaxSubscriptions
	if maxSubs <= 0 {
		maxSubs = 100
	}
	return func(c *context.Context) error {
		if !opts.originAllowed(c) {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "origin_not_allowed"})
		}
		upgrader := websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// The origin was checked above against opts.
			CheckOrigin: func(*http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(c.Response.ResponseWriter, c.Request, nil)
		if err != nil {
			slog.Error("websocket upgrade error", "error", err)
			return err
		}
		defer conn.Close()

		c.Abort() // Abort downstream HTTP middlewares as connection is upgraded

		clientSubs := make(map[string]func())
		var mu sync.Mutex
		sendChan := make(chan []byte, 128)
		done := make(chan struct{})

		// Cleanup all subscriptions on disconnect
		defer func() {
			close(done)
			mu.Lock()
			for _, unsub := range clientSubs {
				unsub()
			}
			mu.Unlock()
		}()

		// Writer Goroutine
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-done:
					return
				case data := <-sendChan:
					_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
						return
					}
				case <-ticker.C:
					_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						return
					}
				}
			}
		}()

		// Reader Loop
		conn.SetReadLimit(65536)
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})

		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				break
			}

			var cmd ClientCommand
			if err := json.Unmarshal(msgBytes, &cmd); err != nil {
				continue
			}

			switch cmd.Action {
			case "subscribe":
				if cmd.Channel == "" {
					continue
				}
				refuse := func(reason string) {
					msg, _ := json.Marshal(map[string]string{"event": "subscription_error", "channel": cmd.Channel, "error": reason})
					select {
					case sendChan <- msg:
					default:
					}
				}
				if !opts.authorizeChannel(c, cmd.Channel) {
					refuse("forbidden")
					continue
				}
				mu.Lock()
				if _, exists := clientSubs[cmd.Channel]; !exists && len(clientSubs) >= maxSubs {
					mu.Unlock()
					refuse("too_many_subscriptions")
					continue
				}
				if _, exists := clientSubs[cmd.Channel]; !exists {
					msgChan, unsub := hub.Subscribe(cmd.Channel)
					clientSubs[cmd.Channel] = unsub

					// Forward messages to sendChan
					go func(ch <-chan *Message) {
						for {
							select {
							case <-done:
								return
							case m, ok := <-ch:
								if !ok {
									return
								}
								bytes, err := json.Marshal(m)
								if err == nil {
									select {
									case sendChan <- bytes:
									case <-done:
										return
									}
								}
							}
						}
					}(msgChan)
				}
				mu.Unlock()

			case "unsubscribe":
				mu.Lock()
				if unsub, exists := clientSubs[cmd.Channel]; exists {
					unsub()
					delete(clientSubs, cmd.Channel)
				}
				mu.Unlock()

			case "ping":
				select {
				case sendChan <- []byte(`{"event":"pong"}`):
				default:
				}
			}
		}

		return nil
	}
}
