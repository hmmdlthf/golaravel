package broadcast

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"
	"github.com/golaravel/golaravel/pkg/context"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow cross-origin WebSocket connections from Flutter apps
	},
}

// ClientCommand represents a message sent from the WebSocket client to the server.
type ClientCommand struct {
	Action  string `json:"action"`  // "subscribe", "unsubscribe", "ping"
	Channel string `json:"channel"` // channel name
}

// WebSocketHandler returns a handler that upgrades the connection to full-duplex WebSocket.
func WebSocketHandler(h ...*Hub) context.HandlerFunc {
	hub := DefaultHub()
	if len(h) > 0 && h[0] != nil {
		hub = h[0]
	}

	return func(c *context.Context) error {
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
				mu.Lock()
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
