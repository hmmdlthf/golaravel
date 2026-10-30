package benchmarks

import (
	"bytes"
	"context"
	"encoding/json"
	htmlTemplate "html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-h/templ"
	gojson "github.com/goccy/go-json"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/router"
)

type BenchUser struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	IsActive  bool   `json:"is_active"`
}

var sampleUser = BenchUser{
	ID:       101,
	Name:     "Taylor Otwell",
	Email:    "taylor@laravel.com",
	Role:     "Administrator",
	IsActive: true,
}

// 1. JSON Benchmark: Standard encoding/json vs GoLaravel (goccy/go-json)
func BenchmarkJSON_StdLib(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(sampleUser)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSON_GoLaravel_Fast(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := gojson.Marshal(sampleUser)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// 2. Template Benchmark: Standard html/template vs Templ (Compiled)
func BenchmarkTemplate_StdLib(b *testing.B) {
	tmpl, err := htmlTemplate.New("test").Parse(`<!DOCTYPE html><html><body><h1>{{.Name}}</h1><p>{{.Email}}</p></body></html>`)
	if err != nil {
		b.Fatal(err)
	}
	var buf bytes.Buffer
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		_ = tmpl.Execute(&buf, sampleUser)
	}
}

func BenchmarkTemplate_Templ(b *testing.B) {
	// Equivalent to a compiled templ component
	cmp := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><body><h1>")
		_, _ = io.WriteString(w, templ.EscapeString(sampleUser.Name))
		_, _ = io.WriteString(w, "</h1><p>")
		_, _ = io.WriteString(w, templ.EscapeString(sampleUser.Email))
		_, _ = io.WriteString(w, "</p></body></html>")
		return nil
	})

	ctx := context.Background()
	var buf bytes.Buffer
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		_ = cmp.Render(ctx, &buf)
	}
}

// 3. Router Benchmark: Static and Deep Dynamic matching
func BenchmarkRouter_ZeroAlloc_Matching(b *testing.B) {
	r := router.New()
	r.GET("/api/v1/organizations/:org_id/projects/:project_id/deployments/:dep_id", func(c *gcontext.Context) error {
		return nil
	})

	c := gcontext.NewContext()
	targetPath := "/api/v1/organizations/org_999/projects/proj_888/deployments/dep_777"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Reset(nil, nil)
		_ = r.Match("GET", targetPath, c)
	}
}

// 4. End-to-End HTTP Server Throughput Benchmark
func BenchmarkGoLaravel_E2E_HTTP_Pipeline(b *testing.B) {
	app := golaravel.New()
	app.GET("/api/users/:id", func(c *gcontext.Context) error {
		_ = c.Param("id")
		return c.JSON(http.StatusOK, sampleUser)
	})

	req := httptest.NewRequest("GET", "/api/users/101", nil)
	w := httptest.NewRecorder()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		app.ServeHTTP(w, req)
	}
}
