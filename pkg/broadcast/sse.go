package broadcast

import (
	"fmt"
	"net/http"

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/context"
)

// SSEHandler returns a handler streaming Server-Sent Events (SSE) to connected clients.
// Works natively with browser EventSource without any client libraries.
func SSEHandler(h ...*Hub) context.HandlerFunc {
	hub := DefaultHub()
	if len(h) > 0 && h[0] != nil {
		hub = h[0]
	}

	return func(c *context.Context) error {
		channel := c.DefaultQuery("channel", "default")

		flusher, ok := c.Response.ResponseWriter.(http.Flusher)
		if !ok {
			return c.String(http.StatusInternalServerError, "Streaming unsupported by response writer")
		}

		c.SetHeader("Content-Type", "text/event-stream")
		c.SetHeader("Cache-Control", "no-cache")
		c.SetHeader("Connection", "keep-alive")
		c.SetHeader("X-Accel-Buffering", "no")
		c.Response.WriteHeader(http.StatusOK)
		flusher.Flush()

		msgChan, unsubscribe := hub.Subscribe(channel)
		defer unsubscribe()

		// Send initial connected event
		_, _ = fmt.Fprintf(&c.Response, "event: connected\ndata: {\"channel\":\"%s\"}\n\n", channel)
		flusher.Flush()

		ctx := c.Request.Context()

		for {
			select {
			case <-ctx.Done():
				return nil
			case msg, ok := <-msgChan:
				if !ok {
					return nil
				}
				dataBytes, err := json.Marshal(msg.Data)
				if err != nil {
					continue
				}

				_, err = fmt.Fprintf(&c.Response, "event: %s\ndata: %s\n\n", msg.Event, string(dataBytes))
				if err != nil {
					return nil
				}
				flusher.Flush()
			}
		}
	}
}
