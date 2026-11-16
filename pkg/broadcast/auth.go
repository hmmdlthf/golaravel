package broadcast

import (
	"net/url"
	"strings"
	"sync"

	"github.com/golaravel/golaravel/pkg/context"
)

// ChannelAuthorizer decides whether the request may listen on a channel.
// params holds the {placeholders} of the matching pattern.
type ChannelAuthorizer func(c *context.Context, params map[string]string) bool

type channelRule struct {
	segments []string
	fn       ChannelAuthorizer
}

var (
	channelsMu sync.RWMutex
	channels   []channelRule
)

// Channel registers who may listen on channels matching pattern, like
// Laravel's routes/channels.php:
//
//	broadcast.Channel("orders.{id}", func(c *context.Context, p map[string]string) bool {
//		return currentUserOwnsOrder(c, p["id"])
//	})
//
// Segments are separated by ".", and {name} matches one segment. A
// registered pattern makes its channels private: the callback must return
// true for every subscription, with or without a "private-" / "presence-"
// prefix on the channel name.
func Channel(pattern string, fn ChannelAuthorizer) {
	channelsMu.Lock()
	defer channelsMu.Unlock()
	channels = append(channels, channelRule{segments: strings.Split(pattern, "."), fn: fn})
}

// ResetChannels removes every registered channel rule (for tests).
func ResetChannels() {
	channelsMu.Lock()
	defer channelsMu.Unlock()
	channels = nil
}

func stripPrefix(channel string) (string, bool) {
	for _, p := range []string{"private-", "presence-"} {
		if strings.HasPrefix(channel, p) {
			return strings.TrimPrefix(channel, p), true
		}
	}
	return channel, false
}

func matchRule(name string) (ChannelAuthorizer, map[string]string, bool) {
	segs := strings.Split(name, ".")
	channelsMu.RLock()
	defer channelsMu.RUnlock()
	for _, r := range channels {
		if len(r.segments) != len(segs) {
			continue
		}
		params := map[string]string{}
		ok := true
		for i, s := range r.segments {
			if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
				if segs[i] == "" {
					ok = false
					break
				}
				params[strings.Trim(s, "{}")] = segs[i]
				continue
			}
			if s != segs[i] {
				ok = false
				break
			}
		}
		if ok {
			return r.fn, params, true
		}
	}
	return nil, nil, false
}

// validChannel rejects names that could break the SSE / JSON framing or
// are absurdly long.
func validChannel(name string) bool {
	if name == "" || len(name) > 200 {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// authorizeChannel applies Options.Authorize, then the registered channel
// rules. "private-" and "presence-" channels without a rule are refused;
// other channels are public.
func (o *Options) authorizeChannel(c *context.Context, channel string) bool {
	if !validChannel(channel) {
		return false
	}
	if o.Authorize != nil {
		return o.Authorize(c, channel)
	}
	name, private := stripPrefix(channel)
	if fn, params, ok := matchRule(name); ok {
		return fn != nil && fn(c, params)
	}
	return !private
}

// originAllowed: no Origin (native apps, server clients), same origin, or
// one of AllowedOrigins ("*" allows any).
func (o *Options) originAllowed(c *context.Context) bool {
	origin := c.Header("Origin")
	if origin == "" {
		return true
	}
	for _, a := range o.AllowedOrigins {
		if a == "*" || strings.EqualFold(strings.TrimRight(a, "/"), origin) {
			return true
		}
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, c.Request.Host)
}

// Options configures the SSE and WebSocket handlers.
type Options struct {
	// Hub to subscribe on (default: DefaultHub()).
	Hub *Hub
	// Authorize, when set, decides every subscription itself instead of
	// the registered Channel rules.
	Authorize func(c *context.Context, channel string) bool
	// AllowedOrigins lists browser origins (besides the page's own) that
	// may open WebSockets, for example a Flutter web build served from
	// another host. "*" allows any origin; avoid it when the connection
	// carries cookies.
	AllowedOrigins []string
	// MaxSubscriptions caps channels per WebSocket connection (default 100).
	MaxSubscriptions int
}

func (o Options) hub() *Hub {
	if o.Hub != nil {
		return o.Hub
	}
	return DefaultHub()
}
