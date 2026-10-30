# Realtime WebSockets & SSE Broadcasting 📡 (PRD §5.3)

GoLaravel includes a high-performance, real-time event broadcasting system supporting both **full-duplex WebSockets** and **Server-Sent Events (SSE)**, with automated lifecycle hooks for **Ent ORM model mutations**.

---

## 1. Overview & Dual Protocol Support

| Protocol | Best Suited For | Client Implementation |
|---|---|---|
| **WebSockets** (`/api/ws`) | Flutter Apps (Mobile, Desktop, Web), Interactive dashboards | Standard WebSocket (`web_socket_channel` in Dart, `new WebSocket()` in JS) |
| **Server-Sent Events** (`/api/broadcast/sse`) | Lightweight web pages, unauthenticated public tracking | Built-in browser `new EventSource()` — **Zero client libraries required** |

---

## 2. Ent ORM Mutation Hooks (Live Model Updates)

GoLaravel can automatically broadcast events whenever models are created, updated, or deleted by attaching `broadcast.EntMutationHook`:

```go
import "github.com/golaravel/golaravel/pkg/broadcast"

// Attach Ent lifecycle mutation hook:
client.Invoice.Use(broadcast.EntMutationHook("invoices"))
```

### What Happens Automatically:
- When `client.Invoice.Create().Save(ctx)` runs:
  - Broadcasts event `invoice.created` on channel `invoices`.
- When `client.Invoice.UpdateOneID(id).Save(ctx)` runs (e.g. marked as paid):
  - Broadcasts event `invoice.updated` on channel `invoices` and `invoices.{id}`.
- When `client.Invoice.DeleteOneID(id).Exec(ctx)` runs:
  - Broadcasts event `invoice.deleted` on channel `invoices`.

Payload received by clients:
```json
{
  "entity": "invoice",
  "action": "invoice.created",
  "op": "OpCreate",
  "id": 42
}
```

---

## 3. Registering Broadcast Endpoints

Register the WebSocket and SSE handlers on your router or route group:

```go
import "github.com/golaravel/golaravel/pkg/broadcast"

api := app.Group("/api")

// 1. Full-Duplex WebSockets
api.GET("/ws", broadcast.WebSocketHandler())

// 2. Unidirectional Server-Sent Events (SSE)
api.GET("/broadcast/sse", broadcast.SSEHandler())
```

---

## 4. Manual Broadcasting

You can also broadcast custom domain events manually from any controller, job, or event listener:

```go
import "github.com/golaravel/golaravel/pkg/broadcast"

// Broadcast to public channel
broadcast.Broadcast("invoices", "payment.received", map[string]any{
	"invoice_id": 42,
	"amount":     1500.00,
	"currency":   "USD",
})

// Broadcast to private user channel
broadcast.Broadcast(fmt.Sprintf("user.%s", userId), "notification.new", map[string]any{
	"title": "Invoice Paid",
})
```

---

## 5. Client Integration Examples

### Browser Native SSE (Zero Libraries)
```javascript
const es = new EventSource('/api/broadcast/sse?channel=invoices');

es.addEventListener('invoice.created', (event) => {
    const data = JSON.parse(event.data);
    console.log('New invoice created:', data.id);
});

es.addEventListener('invoice.updated', (event) => {
    const data = JSON.parse(event.data);
    console.log('Invoice updated:', data.id);
});
```

### Flutter / Dart WebSocket
```dart
import 'dart:convert';
import 'package:web_socket_channel/web_socket_channel.dart';

final channel = WebSocketChannel.connect(Uri.parse('ws://localhost:8080/api/ws'));

// Subscribe to invoices channel
channel.sink.add(json.encode({
  'action': 'subscribe',
  'channel': 'invoices',
}));

// Listen for live updates
channel.stream.listen((message) {
  final event = json.decode(message);
  print('Realtime event received: ${event['event']}');
  // Refresh invoices table in UI
});
```
