package artisan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakeCrud_FullStack(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "artisan_crud_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create dummy go.mod in tmpDir
	goModContent := "module github.com/example/store\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	err = RunMakeCrud([]string{"Product"}, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error running make:crud: %v", err)
	}

	// 1. Verify Schema
	schemaFile := filepath.Join(tmpDir, "app", "schema", "product.go")
	schemaData, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("failed to read schema: %v", err)
	}
	schemaStr := string(schemaData)
	if !strings.Contains(schemaStr, "type Product struct") || !strings.Contains(schemaStr, "package schema") {
		t.Errorf("schema file does not contain expected Product struct")
	}

	// 2. Verify Requests
	reqFile := filepath.Join(tmpDir, "app", "requests", "product_request.go")
	reqData, err := os.ReadFile(reqFile)
	if err != nil {
		t.Fatalf("failed to read requests: %v", err)
	}
	reqStr := string(reqData)
	if !strings.Contains(reqStr, "CreateProductRequest") || !strings.Contains(reqStr, "UpdateProductRequest") {
		t.Errorf("requests file does not contain expected request structs")
	}

	// 3. Verify Controller
	ctrlFile := filepath.Join(tmpDir, "app", "handlers", "product_controller.go")
	ctrlData, err := os.ReadFile(ctrlFile)
	if err != nil {
		t.Fatalf("failed to read controller: %v", err)
	}
	ctrlStr := string(ctrlData)
	if !strings.Contains(ctrlStr, "ProductController") || !strings.Contains(ctrlStr, "RegisterRoutes") {
		t.Errorf("controller file missing expected elements")
	}
	if !strings.Contains(ctrlStr, "github.com/example/store/app/views") {
		t.Errorf("controller missing views import with detected module name")
	}

	// 4. Verify Templ Views
	viewFile := filepath.Join(tmpDir, "app", "views", "product.templ")
	viewData, err := os.ReadFile(viewFile)
	if err != nil {
		t.Fatalf("failed to read views: %v", err)
	}
	viewStr := string(viewData)
	if !strings.Contains(viewStr, "ProductIndex") || !strings.Contains(viewStr, "ProductShow") || !strings.Contains(viewStr, "ProductForm") {
		t.Errorf("views file missing expected Templ components")
	}

	// 5. Verify Seeder
	seederFile := filepath.Join(tmpDir, "database", "seeders", "product_seeder.go")
	seederData, err := os.ReadFile(seederFile)
	if err != nil {
		t.Fatalf("failed to read seeder: %v", err)
	}
	seederStr := string(seederData)
	if !strings.Contains(seederStr, "ProductSeeder") {
		t.Errorf("seeder file missing expected ProductSeeder")
	}
}

func TestMakeCrud_ApiOnly(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "artisan_crud_api_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	err = RunMakeCrud([]string{"Customer", "--api"}, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error running make:crud --api: %v", err)
	}

	// Schema, Requests, Controller, and Seeder should exist
	if _, err := os.Stat(filepath.Join(tmpDir, "app", "schema", "customer.go")); err != nil {
		t.Errorf("expected customer schema to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "app", "requests", "customer_request.go")); err != nil {
		t.Errorf("expected customer requests to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "app", "handlers", "customer_controller.go")); err != nil {
		t.Errorf("expected customer controller to exist: %v", err)
	}

	// Views should NOT exist when --api flag is supplied
	if _, err := os.Stat(filepath.Join(tmpDir, "app", "views", "customer.templ")); !os.IsNotExist(err) {
		t.Errorf("expected customer.templ NOT to exist when --api is used")
	}
}

func TestMakeCrud_ValidationAndErrors(t *testing.T) {
	// Missing name
	err := RunMakeCrud([]string{})
	if err == nil {
		t.Fatal("expected error when entity name is missing")
	}
	if !strings.Contains(err.Error(), "missing entity name") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestMakeCrud_NamingConversions(t *testing.T) {
	tests := []struct {
		input        string
		pascal       string
		snake        string
		pluralPascal string
		pluralSnake  string
	}{
		{"product", "Product", "product", "Products", "products"},
		{"blog_post", "BlogPost", "blog_post", "BlogPosts", "blog_posts"},
		{"Category", "Category", "category", "Categories", "categories"},
		{"box", "Box", "box", "Boxes", "boxes"},
	}

	for _, tt := range tests {
		p := toPascalCase(tt.input)
		s := toSnakeCase(p)
		pp := pluralize(p)
		ps := pluralize(s)

		if p != tt.pascal {
			t.Errorf("toPascalCase(%s) = %s, expected %s", tt.input, p, tt.pascal)
		}
		if s != tt.snake {
			t.Errorf("toSnakeCase(%s) = %s, expected %s", p, s, tt.snake)
		}
		if pp != tt.pluralPascal {
			t.Errorf("pluralize(%s) = %s, expected %s", p, pp, tt.pluralPascal)
		}
		if ps != tt.pluralSnake {
			t.Errorf("pluralize(%s) = %s, expected %s", s, ps, tt.pluralSnake)
		}
	}
}

func TestArtisanRegistry_MakeCrudRegistration(t *testing.T) {
	reg := NewRegistry()
	cmd, exists := reg.commands["make:crud"]
	if !exists {
		t.Fatal("expected make:crud to be registered in Artisan default commands")
	}
	if cmd.Name != "make:crud" || !strings.Contains(cmd.Description, "CRUD") {
		t.Errorf("unexpected command metadata: %+v", cmd)
	}
}
