package artisan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The example app's POS files are the generator's output, unedited: this
// keeps them in sync, and building the example proves the output compiles
// against real Ent code.
func TestMakeSyncable_MatchesExample(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/golaravel/golaravel/example\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"Product", "name:string", "sku:string", "price:int64", "stock:counter"},
		{"Sale", "total:int64", "sold_at:time", "note:string"},
		{"SaleLine", "sale_id:string", "product_id:string", "qty:float", "price:int64"},
	} {
		if err := RunMakeSyncable(args, dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"schema/product.go", "schema/sale.go", "schema/sale_line.go",
		"synced/product.go", "synced/sale.go", "synced/sale_line.go"} {
		got, err := os.ReadFile(filepath.Join(dir, "app", f))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join("..", "..", "example", "app", f))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("example/app/%s differs from make:syncable output; regenerate it:\n%s", f, got)
		}
	}
}

func TestMakeSyncable_Options(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "app"), 0o755)
	gen := filepath.Join(dir, "app", "generate.go")
	_ = os.WriteFile(gen, []byte("package app\n\n//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate ./schema\n"), 0o644)

	if err := RunMakeSyncable([]string{"stock_item", "image_url:string", "on_hand:counter", "checked_at:time"}, dir); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "app", "synced", "stock_item.go"))
	for _, want := range []string{
		`"example.com/shop/app"`, `"example.com/shop/app/stockitem"`, "func StockItem() offline.Resource[*app.Tx]",
		`Name:     "stock_item"`, "SetImageURL(", "AddOnHand(d)", `Counters: []string{"on_hand"}`, "*time.Time",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("generated resource lacks %q", want)
		}
	}
	g, _ := os.ReadFile(gen)
	if !strings.Contains(string(g), "ent generate --feature sql/execquery ./schema") {
		t.Errorf("generate.go not patched: %s", g)
	}
	// Running again leaves generate.go alone and skips existing files.
	if err := RunMakeSyncable([]string{"stock_item", "name"}, dir); err != nil {
		t.Fatal(err)
	}
	if g2, _ := os.ReadFile(gen); string(g2) != string(g) {
		t.Error("generate.go patched twice")
	}

	for _, bad := range [][]string{
		{},
		{"Thing"},
		{"Thing", "Name:string"},
		{"Thing", "x:money"},
		{"Thing", "tenant_id:string"},
		{"Thing", "sync_hlc:string"},
		{"Thing", "a", "a:int"},
		{"Thing", "a", "--wat"},
	} {
		if err := RunMakeSyncable(bad, dir); err == nil {
			t.Errorf("make:syncable %v should fail", bad)
		}
	}
}

func TestSyncNames(t *testing.T) {
	for in, want := range map[string]string{"SaleLine": "SaleLine", "sale_line": "SaleLine", "sale-line": "SaleLine", "product": "Product", "api_key": "APIKey"} {
		if got := syncTypeName(in); got != want {
			t.Errorf("syncTypeName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"image_url": "ImageURL", "user_id": "UserID", "sold_at": "SoldAt", "http_status": "HTTPStatus"} {
		if got := entPascal(in); got != want {
			t.Errorf("entPascal(%q) = %q, want %q", in, got, want)
		}
	}
}
