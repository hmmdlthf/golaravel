// Command e2eserver runs the sync engine on a throwaway SQLite database,
// for the TypeScript client's end-to-end tests (clients/offline). It is a
// test fixture: identity comes from X-Test-User / X-Test-Tenant headers.
//
//	go run ./pkg/offline/internal/e2eserver -addr 127.0.0.1:0
//
// It prints "LISTENING http://host:port" once it accepts requests.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/pkg/auth"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/features"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/offline/internal/testapp"
	"github.com/golaravel/golaravel/pkg/offline/internal/testent"
	"github.com/golaravel/golaravel/pkg/privacy"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	flag.Parse()
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "golaravel-e2e-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	client, err := testent.Open("sqlite3", "file:"+filepath.Join(dir, "sync.db")+"?_fk=1&_busy_timeout=10000&_journal_mode=WAL&_txlock=immediate")
	if err != nil {
		log.Fatal(err)
	}
	if err := client.Schema.Create(ctx); err != nil {
		log.Fatal(err)
	}
	key, err := offline.GenerateServerKey("e2e-1")
	if err != nil {
		log.Fatal(err)
	}
	eng, err := offline.New(offline.Options[*testent.Tx]{
		Begin:   func(ctx context.Context) (*testent.Tx, error) { return client.Tx(ctx) },
		Dialect: database.SQLite,
		Keys:    []offline.ServerKey{key},
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := eng.Register(testapp.Product(nil)); err != nil {
		log.Fatal(err)
	}
	testapp.Wire(client, eng)

	app := golaravel.New()
	identify := func(c *gcontext.Context) error {
		if u := c.Header("X-Test-User"); u != "" {
			c.Set(auth.ContextUserKey, &auth.SimpleUser{ID: u})
		}
		if t := c.Header("X-Test-Tenant"); t != "" {
			c.Request = c.Request.WithContext(privacy.WithTenantID(c.Context(), t))
		}
		return c.Next()
	}
	eng.Routes(app.Router.Group("/sync", identify))

	// Admin endpoints edit and read products outside sync, the way an
	// admin panel would, to check that devices see those edits.
	admin := app.Router.Group("/admin", identify)
	admin.GET("/products/:id", func(c *gcontext.Context) error {
		p, err := client.Product.Get(c.Context(), c.Param("id"))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"id": p.ID, "name": p.Name, "price": p.Price, "stock": p.Stock,
			"version": p.SyncVersion, "deleted": p.DeletedAt != nil})
	})
	admin.POST("/products/:id", func(c *gcontext.Context) error {
		var in struct {
			Name  *string `json:"name"`
			Price *int64  `json:"price"`
		}
		if err := json.NewDecoder(c.Request.Body).Decode(&in); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		id := c.Param("id")
		var err error
		if _, gerr := client.Product.Get(c.Context(), id); gerr != nil {
			cr := client.Product.Create().SetID(id)
			if in.Name != nil {
				cr.SetName(*in.Name)
			}
			if in.Price != nil {
				cr.SetPrice(*in.Price)
			}
			err = cr.Exec(c.Context())
		} else {
			u := client.Product.UpdateOneID(id)
			if in.Name != nil {
				u.SetName(*in.Name)
			}
			if in.Price != nil {
				u.SetPrice(*in.Price)
			}
			err = u.Exec(c.Context())
		}
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"ok": id})
	})

	// pkg/features' public endpoint, for the TypeScript FeatureClient.
	fm := features.NewInMemory(features.Options{})
	if err := fm.Register(
		features.Bool("invoices.recurring", false).Public(),
		features.Enum("tax.mode", "inclusive", "inclusive", "exclusive", "none"), // not Public: never sent
		features.JSON("ui.theme", map[string]string{"color": "blue"}).Public(),
	); err != nil {
		log.Fatal(err)
	}
	app.Router.GET("/features", identify, fm.Handler())
	admin.POST("/features/:key", func(c *gcontext.Context) error {
		var in struct {
			Value any `json:"value"`
		}
		if err := json.NewDecoder(c.Request.Body).Decode(&in); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := fm.Set(c.Context(), c.Param("key"), in.Value); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"ok": c.Param("key")})
	})

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("LISTENING http://%s\n", ln.Addr())
	log.Fatal(http.Serve(ln, app))
}
