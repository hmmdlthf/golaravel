package broadcast

import (
	"bufio"
	stdContext "context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

func TestHub_PubSub(t *testing.T) {
	hub := NewHub()
	ch, unsub := hub.Subscribe("invoices")
	defer unsub()

	var wg sync.WaitGroup
	wg.Add(1)

	var received *Message
	go func() {
		defer wg.Done()
		select {
		case msg := <-ch:
			received = msg
		case <-time.After(500 * time.Millisecond):
			t.Error("timeout waiting for broadcast")
		}
	}()

	hub.Broadcast("invoices", "invoice.created", map[string]any{"id": 42, "amount": 1250.00})
	wg.Wait()

	if received == nil {
		t.Fatal("expected message to be received")
	}
	if received.Event != "invoice.created" {
		t.Fatalf("expected event invoice.created, got %s", received.Event)
	}
	if received.Data["id"] != 42 {
		t.Fatalf("expected id=42, got %v", received.Data["id"])
	}
}

func TestSSE_Streaming(t *testing.T) {
	hub := NewHub()
	handler := SSEHandler(hub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/broadcast/sse?channel=orders", nil)
	c := context.NewContext()
	c.Reset(w, req)

	// Run SSE in a background goroutine since it blocks waiting for events
	var wg sync.WaitGroup
	wg.Add(1)

	ctx, cancel := stdContextWithTimeout(50 * time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	c.Request = req

	go func() {
		defer wg.Done()
		_ = handler(c)
	}()

	time.Sleep(10 * time.Millisecond)
	hub.Broadcast("orders", "order.placed", map[string]any{"order_id": 999})
	wg.Wait()

	scanner := bufio.NewScanner(w.Body)
	var foundEvent, foundData bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: order.placed") {
			foundEvent = true
		}
		if strings.Contains(line, `"order_id":999`) {
			foundData = true
		}
	}

	if !foundEvent || !foundData {
		t.Fatalf("expected SSE output to contain event and data, got: %s", w.Body.String())
	}
}

func TestWebSocket_ClientSubscription(t *testing.T) {
	hub := NewHub()
	r := router.New()
	r.GET("/ws", WebSocketHandler(hub))

	// Setup httptest server with WebSocket support
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c := context.NewContext()
		c.Reset(w, req)
		handlers := r.Match(req.Method, req.URL.Path, c)
		if handlers != nil {
			c.SetHandlers(handlers)
			_ = c.Next()
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	defer ws.Close()

	// 1. Subscribe to "invoices"
	subCmd := `{"action":"subscribe","channel":"invoices"}`
	if err := ws.WriteMessage(websocket.TextMessage, []byte(subCmd)); err != nil {
		t.Fatalf("failed to write subscribe command: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	// 2. Broadcast event
	hub.Broadcast("invoices", "invoice.paid", map[string]any{"id": 77, "status": "paid"})

	// 3. Receive event on WebSocket
	_ = ws.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, p, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read from websocket: %v", err)
	}

	msgStr := string(p)
	if !strings.Contains(msgStr, "invoice.paid") || !strings.Contains(msgStr, `"status":"paid"`) {
		t.Fatalf("unexpected message on websocket: %s", msgStr)
	}
}

func stdContextWithTimeout(d time.Duration) (stdContext.Context, stdContext.CancelFunc) {
	return stdContext.WithTimeout(stdContext.Background(), d)
}
