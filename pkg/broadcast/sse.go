package broadcast

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/context"
)

// SSEHandler returns a handler streaming Server-Sent Events (SSE) to connected clients.
// Works natively with browser EventSource without any client libraries.
//
// Channels are authorized like WebSocket subscriptions: see Channel and
// SSEHandlerWith.
func SSEHandler(h ...*Hub) context.HandlerFunc {
	opts := Options{}
	if len(h) > 0 {
		opts.Hub = h[0]
	}
	return SSEHandlerWith(opts)
}

// SSEHandlerWith is SSEHandler with options.
func SSEHandlerWith(opts Options) context.HandlerFunc {
	hub := opts.hub()
	return func(c *context.Context) error {
		channel := c.DefaultQuery("channel", "default")
		if !opts.authorizeChannel(c, channel) {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "channel_forbidden", "channel": channel})
		}

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
		hello, _ := json.Marshal(map[string]string{"channel": channel})
		_, _ = fmt.Fprintf(&c.Response, "event: connected\ndata: %s\n\n", hello)
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

				// Event names come from application code, but never let one
				// break the SSE framing.
				event := strings.NewReplacer("\r", "", "\n", "").Replace(msg.Event)
				_, err = fmt.Fprintf(&c.Response, "event: %s\ndata: %s\n\n", event, string(dataBytes))
				if err != nil {
					return nil
				}
				flusher.Flush()
			}
		}
	}
}
