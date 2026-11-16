package broadcast

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"

	"github.com/golaravel/golaravel/pkg/context"
)

func serve(h context.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := context.NewContext()
		c.Reset(w, r)
		_ = h(c)
	}))
}

// The test user is whoever the X-User header names.
func registerOrderRule(t *testing.T) {
	t.Helper()
	ResetChannels()
	t.Cleanup(ResetChannels)
	Channel("orders.{id}", func(c *context.Context, p map[string]string) bool {
		return c.Header("X-User") == "owner-of-"+p["id"]
	})
}

func sseStatus(t *testing.T, srv *httptest.Server, channel, user string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/?channel="+channel, nil)
	if user != "" {
		req.Header.Set("X-User", user)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestSSEChannelAuthorization(t *testing.T) {
	registerOrderRule(t)
	srv := serve(SSEHandler(NewHub()))
	defer srv.Close()

	for _, tc := range []struct {
		channel, user string
		want          int
	}{
		{"news", "", 200},                 // public
		{"private-chat", "", 403},         // private without a rule
		{"orders.42", "", 403},            // rule, wrong user
		{"orders.42", "owner-of-7", 403},  // rule, someone else's order
		{"orders.42", "owner-of-42", 200}, // rule passes
		{"private-orders.42", "owner-of-42", 200},
		{"bad%0Aevent:%20x", "", 403}, // newline would break SSE framing
	} {
		if got := sseStatus(t, srv, tc.channel, tc.user); got != tc.want {
			t.Errorf("channel %q user %q: %d, want %d", tc.channel, tc.user, got, tc.want)
		}
	}
}

func wsURL(srv *httptest.Server) string { return "ws" + strings.TrimPrefix(srv.URL, "http") }

func dial(t *testing.T, srv *httptest.Server, h http.Header) (*websocket.Conn, int) {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), h)
	if err != nil {
		if resp != nil {
			return nil, resp.StatusCode
		}
		t.Fatal(err)
	}
	return conn, 101
}

func TestWebSocketOriginCheck(t *testing.T) {
	srv := serve(WebSocketHandlerWith(Options{Hub: NewHub(), AllowedOrigins: []string{"https://app.example.com"}}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	for origin, want := range map[string]int{
		"":                        101, // native client
		"http://" + host:          101, // same origin
		"https://app.example.com": 101, // allow-listed
		"https://evil.example":    403, // cross-site hijacking attempt
	} {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		conn, code := dial(t, srv, h)
		if conn != nil {
			conn.Close()
		}
		if code != want {
			t.Errorf("origin %q: %d, want %d", origin, code, want)
		}
	}
}

func readEvent(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, b, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func TestWebSocketSubscriptionAuthorization(t *testing.T) {
	registerOrderRule(t)
	hub := NewHub()
	srv := serve(WebSocketHandlerWith(Options{Hub: hub, MaxSubscriptions: 2}))
	defer srv.Close()

	h := http.Header{}
	h.Set("X-User", "owner-of-42")
	conn, _ := dial(t, srv, h)
	defer conn.Close()

	send := func(action, ch string) {
		_ = conn.WriteJSON(map[string]string{"action": action, "channel": ch})
	}
	send("subscribe", "orders.7")
	if ev := readEvent(t, conn); ev["event"] != "subscription_error" || ev["error"] != "forbidden" {
		t.Fatalf("someone else's order: %v", ev)
	}
	send("subscribe", "orders.42")
	time.Sleep(50 * time.Millisecond)
	hub.Broadcast("orders.42", "order.shipped", map[string]any{"id": 42})
	if ev := readEvent(t, conn); ev["event"] != "order.shipped" {
		t.Fatalf("own order event: %v", ev)
	}

	send("subscribe", "news")
	send("subscribe", "sports")
	if ev := readEvent(t, conn); ev["error"] != "too_many_subscriptions" {
		t.Fatalf("subscription cap: %v", ev)
	}
}
