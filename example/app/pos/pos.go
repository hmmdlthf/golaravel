// Package pos is the example's offline point of sale: products, sales and
// sale lines that tills create offline and sync with pkg/offline.
//
// The resources in app/synced come from `artisan make:syncable`; this
// package adds the shop's rules on top and mounts the sync endpoints.
package pos

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/product"
	"github.com/golaravel/golaravel/example/app/sale"
	"github.com/golaravel/golaravel/example/app/saleline"
	"github.com/golaravel/golaravel/example/app/synced"
	"github.com/golaravel/golaravel/pkg/auth"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/privacy"
	"github.com/golaravel/golaravel/pkg/router"
)

// DemoTenant is the single tenant of the example; the login token carries
// it as the tenant_id claim.
const DemoTenant = "demo"

// Options configure Setup.
type Options struct {
	// Keys turn on payload encryption for tills that register a key.
	Keys []offline.ServerKey
	// ClientDist is where the built @golaravel/offline package is
	// (clients/offline/dist), served to the demo page at /pos/lib/.
	ClientDist string
}

// Setup creates the sync engine for the POS entities, attaches tenant
// scoping and the change hook to them, and mounts:
//
//	/api/pos/sync/*   the sync endpoints (Bearer token with a tenant_id claim)
//	/pos              a demo till page
func Setup(r router.RouteRegistrar, client *app.Client, jwt *auth.JWTManager, opts Options) (*offline.Engine[*app.Tx], error) {
	eng, err := offline.New(offline.Options[*app.Tx]{
		Begin:   func(ctx context.Context) (*app.Tx, error) { return client.Tx(ctx) },
		Dialect: database.SQLite,
		Keys:    opts.Keys,
	})
	if err != nil {
		return nil, err
	}

	products := synced.Product()
	products.Apply = rejectNegative(products.Apply, "price")
	sales := synced.Sale()
	sales.Authorize = createOnly
	lines := synced.SaleLine()
	lines.Authorize = createOnly
	lines.Apply = rejectNegative(lines.Apply, "price", "qty")
	for _, res := range []offline.Resource[*app.Tx]{products, sales, lines} {
		if err := eng.Register(res); err != nil {
			return nil, err
		}
	}

	client.Product.Intercept(privacy.TenantScope(func(q *app.ProductQuery, t string) { q.Where(product.TenantID(t)) }))
	client.Sale.Intercept(privacy.TenantScope(func(q *app.SaleQuery, t string) { q.Where(sale.TenantID(t)) }))
	client.SaleLine.Intercept(privacy.TenantScope(func(q *app.SaleLineQuery, t string) { q.Where(saleline.TenantID(t)) }))
	client.Product.Use(privacy.TenantMutationHook(), offline.ChangeHook(eng, "product"))
	client.Sale.Use(privacy.TenantMutationHook(), offline.ChangeHook(eng, "sale"))
	client.SaleLine.Use(privacy.TenantMutationHook(), offline.ChangeHook(eng, "sale_line"))

	api := r.Group("/api/pos/sync",
		auth.BearerGuard(jwt),
		privacy.TenantMiddlewareWithOptions(privacy.TenantMiddlewareOptions{Required: true}, privacy.JWTClaimResolver("tenant_id")),
	)
	eng.Routes(api)

	r.GET("/pos", func(c *gcontext.Context) error {
		c.SetHeader("Content-Type", "text/html; charset=utf-8")
		c.SetHeader("Cache-Control", "no-store")
		b, _ := page.ReadFile("web/index.html")
		_, err := c.Response.Write(b)
		return err
	})
	dist := opts.ClientDist
	if dist == "" {
		dist = filepath.Join("clients", "offline", "dist")
	}
	r.GET("/pos/lib/*filepath", func(c *gcontext.Context) error {
		name := filepath.Clean("/" + c.Param("filepath"))
		if !strings.HasSuffix(name, ".js") {
			return c.String(http.StatusNotFound, "not found")
		}
		path := filepath.Join(dist, name)
		if _, err := os.Stat(path); err != nil {
			return c.String(http.StatusNotFound, "Build the client first: cd clients/offline && npm install && npm run build")
		}
		c.SetHeader("Content-Type", "text/javascript; charset=utf-8")
		http.ServeFile(&c.Response, c.Request, path)
		return nil
	})
	return eng, nil
}

//go:embed web/index.html
var page embed.FS

// Sales and their lines are records of what happened at the till: they
// can be created, never edited or deleted.
func createOnly(_ context.Context, _ offline.Principal, m *offline.Mutation, current *offline.Record) error {
	if m.Op != offline.OpCreate || (current != nil && !current.Deleted) {
		return offline.Rejectf("immutable", "%s records cannot be changed once made", m.Entity)
	}
	return nil
}

// rejectNegative refuses writes that set any of fields below zero.
func rejectNegative(apply func(context.Context, *app.Tx, *offline.Write) error, fields ...string) func(context.Context, *app.Tx, *offline.Write) error {
	return func(ctx context.Context, tx *app.Tx, w *offline.Write) error {
		for _, f := range fields {
			raw, ok := w.Set[f]
			if !ok {
				continue
			}
			var v float64
			if err := json.Unmarshal(raw, &v); err != nil {
				return offline.Reject("invalid", errors.New(f+" must be a number"))
			}
			if v < 0 {
				return offline.Rejectf("invalid", "%s must not be negative", f)
			}
		}
		return apply(ctx, tx, w)
	}
}

// Seed adds a few products to the demo tenant.
func Seed(ctx context.Context, client *app.Client) error {
	ctx = privacy.WithTenantID(ctx, DemoTenant)
	if n, err := client.Product.Query().Count(ctx); err != nil || n > 0 {
		return err
	}
	for _, p := range []struct {
		id, name, sku string
		price         int64
		stock         float64
	}{
		{"seed-tea", "Ceylon tea", "TEA-01", 450, 40},
		{"seed-bun", "Butter bun", "BUN-01", 120, 60},
		{"seed-kottu", "Chicken kottu", "KOT-01", 1250, 15},
	} {
		if err := client.Product.Create().SetID(p.id).SetName(p.name).SetSku(p.sku).
			SetPrice(p.price).SetStock(p.stock).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
