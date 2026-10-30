# Flutter Multiplatform & Hybrid Serving 📱💻

GoLaravel offers native integration with **Flutter**, allowing developers to build cross-platform client applications for **Web, Windows, macOS, Linux, Android, and iOS** from a single codebase, served directly by the GoLaravel backend.

---

## 1. The Presentation Architecture Problem

Developers typically face an architectural tradeoff:
1. **Interactive Client Applications (Flutter / SPAs)**: Fluid, rich, multi-platform UX, but large initial asset bundle downloads.
2. **Server-Side Rendering (Templ / Blade)**: Zero bundle size, instant TTFB (< 1ms), but limited offline/native desktop capabilities.

### GoLaravel's Hybrid Solution:
- **Core Application**: Powered by **Flutter Multiplatform** for the interactive dashboard and native desktop/mobile apps.
- **Fast-Path Micro-SSR**: Powered by **Templ** for customer-facing documents (invoices, receipts, legal notices, token links, email landing pages).

---

## 2. The Hybrid Micro-SSR Pattern in Action

### Use Case: Public Invoices & Receipts
When an invoice is issued to a client, an email or SMS is sent with a public security link:
`https://app.com/invoice/3f8a92bc`

If this were served through the Flutter SPA:
- The customer would have to download megabytes of Flutter Web JavaScript/Wasm runtime just to view a single static document.
- On mobile connections, this wastes data and introduces seconds of loading latency.

In GoLaravel, the invoice is rendered via **Templ Micro-SSR**:
```go
// Public invoice link opened by customers
app.GET("/invoice/:token", func(c *golaravel.Context) error {
	token := c.Param("token")
	inv, err := client.Invoice.Query().
		Where(invoice.TokenEQ(token)).
		WithItems().
		Only(c.Context())
	if err != nil {
		return c.String(404, "Invoice not found")
	}

	// Delivered in < 45 nanoseconds! 0 client JS bundle needed.
	return c.Templ(200, views.PublicInvoiceView(inv))
})
```

---

## 3. Serving Flutter Web from GoLaravel

GoLaravel includes `flutter.ServeWeb()`, which:
- Serves precompiled static Flutter Web assets (`main.dart.js`, `assets/`, `canvaskit/`).
- Automatically resolves SPA client-side routing to `index.html`.
- Dynamically adjusts `<base href="...">` to match your mount prefix.

```go
import "github.com/golaravel/golaravel/pkg/flutter"

// Host Flutter Web app at /app with SPA fallback
flutter.ServeWeb(app.Router, flutter.Options{
	MountPath:   "/app",
	DistDir:     "client/build/web",
	FallbackSPA: true,
})
```

---

## 4. Building & Running Flutter

Build the Flutter Web release bundle using Artisan:
```bash
artisan flutter:build
```

Run Flutter locally for Desktop (Windows/macOS/Linux) or Mobile:
```bash
cd client
flutter run -d windows # Or macos, linux, chrome, android, ios
```
