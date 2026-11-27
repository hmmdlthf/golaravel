package golaravel

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/middleware"
	"github.com/golaravel/golaravel/pkg/storage"
	gltest "github.com/golaravel/golaravel/pkg/testing"
	"github.com/golaravel/golaravel/pkg/validation"
)

func TestFileHelpersStoreUploads(t *testing.T) {
	disk := gltest.FakeStorage(t, "public")
	app := New()
	app.POST("/profile", func(c *gcontext.Context) error {
		if !c.HasFile("avatar") || c.HasFile("missing") {
			return c.String(http.StatusBadRequest, "has file")
		}
		if c.File("missing") != nil {
			return c.String(http.StatusBadRequest, "missing file")
		}
		p, err := c.File("avatar").Store("avatars", "public")
		if err != nil {
			return err
		}
		var gallery []string
		for _, f := range c.Files("photos") {
			gp, err := f.StorePublicly("gallery")
			if err != nil {
				return err
			}
			gallery = append(gallery, gp)
		}
		return c.JSON(http.StatusOK, map[string]any{"avatar": p, "gallery": gallery, "fields": len(c.AllFiles())})
	})

	res := app.Test(t).Request("POST", "/profile").
		WithForm(map[string]string{"name": "Ada"}).
		WithUpload("avatar", gltest.FakeImage("me.png", 8, 8)).
		WithUpload("photos[]", gltest.FakeImage("a.jpg", 4, 4), gltest.FakeImage("b.gif", 4, 4)).
		Send()
	res.AssertStatus(http.StatusOK)
	var out struct {
		Avatar  string   `json:"avatar"`
		Gallery []string `json:"gallery"`
		Fields  int      `json:"fields"`
	}
	if err := json.Unmarshal(res.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Avatar, "avatars/") || !strings.HasSuffix(out.Avatar, ".png") {
		t.Fatalf("avatar path %q", out.Avatar)
	}
	if len(out.Gallery) != 2 || !strings.HasSuffix(out.Gallery[0], ".jpg") || !strings.HasSuffix(out.Gallery[1], ".gif") {
		t.Fatalf("gallery %v", out.Gallery)
	}
	if out.Fields != 2 {
		t.Fatalf("AllFiles fields: %d", out.Fields)
	}
	disk.AssertExists(out.Avatar).AssertExists(out.Gallery...).AssertCount("gallery", 2)
}

func TestValidationAcceptsUploadedFiles(t *testing.T) {
	data := map[string]any{"avatar": gltest.FakeImage("me.png", 120, 80).UploadedFile()}
	v := validation.Make(data, validation.Rules{"avatar": "required|image|mimes:png|max:100|dimensions:min_width=100"})
	if v.Fails() {
		t.Fatalf("expected the image to pass: %v", v.Errors())
	}
	data = map[string]any{"avatar": gltest.FakeUpload("me.png", 2).UploadedFile()}
	if !validation.Make(data, validation.Rules{"avatar": "image"}).Fails() {
		t.Fatal("a non-image named .png must fail the image rule")
	}
}

func TestMaxBodySizeRaisesTheLimitForOneRoute(t *testing.T) {
	app := New().MaxBodyBytes(4 << 10)
	handler := func(c *gcontext.Context) error {
		if c.File("file") == nil {
			return c.String(http.StatusRequestEntityTooLarge, "too large")
		}
		return c.String(http.StatusOK, "%d", c.File("file").Size())
	}
	app.POST("/small", handler)
	app.POST("/big", middleware.MaxBodySize(1<<20), handler)
	app.POST("/tiny", middleware.MaxBodySize(512), handler)
	client := app.Test(t)
	big := gltest.FakeUpload("video.mp4", 100)

	client.Request("POST", "/small").WithUpload("file", big).Send().AssertStatus(http.StatusRequestEntityTooLarge)
	client.Request("POST", "/big").WithUpload("file", big).Send().AssertStatus(http.StatusOK).AssertSee("102400")
	client.Request("POST", "/tiny").WithUpload("file", gltest.FakeUpload("a.txt", 2)).Send().AssertStatus(http.StatusRequestEntityTooLarge)
	client.Request("POST", "/small").WithUpload("file", gltest.FakeUpload("a.txt", 1)).Send().AssertStatus(http.StatusOK)
}

func TestMethodOverrideKeepsTheUploadReadable(t *testing.T) {
	app := New()
	app.PUT("/docs/1", func(c *gcontext.Context) error {
		b, err := c.File("file").Bytes()
		if err != nil {
			return err
		}
		return c.String(http.StatusOK, "%s %d %s", c.Request.Method, len(b), c.FormValue("title"))
	})
	res := app.Test(t).Request("POST", "/docs/1").
		WithForm(map[string]string{"_method": "PUT", "title": "Q3"}).
		WithUpload("file", gltest.FakeUpload("q3.pdf", 80)).
		Send()
	res.AssertStatus(http.StatusOK).AssertSee("PUT 81920 Q3")
}

// storageApp mounts two local disks: a public one at /files/pub and a
// private one at /files.
func storageApp(t *testing.T) (*Engine, *storage.LocalDriver, *storage.LocalDriver) {
	t.Helper()
	root := t.TempDir()
	priv, err := storage.NewLocalDriver(filepath.Join(root, "app"), "/files", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := storage.NewLocalDriver(filepath.Join(root, "app", "pub"), "/files/pub", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	pub.SetVisibility(storage.VisibilityPublic)
	t.Cleanup(storage.Swap("t-private", priv))
	t.Cleanup(storage.Swap("t-public", pub))
	app := New()
	app.ServeStorage()
	return app, priv, pub
}

func get(app *Engine, method, target string, body io.Reader) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(method, target, body))
	return rec
}

func TestServeStoragePublicAndPrivateFiles(t *testing.T) {
	app, priv, pub := storageApp(t)
	png := gltest.FakeImage("a.png", 2, 2).Content
	_ = pub.Put("avatars/a.png", png)
	_ = pub.Put("page.html", []byte("<script>alert(1)</script>"))
	_ = pub.Put("logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	_ = pub.Put(".env", []byte("SECRET=1"))
	_ = priv.Put("contracts/nda.pdf", []byte("%PDF-1.4 secret"))

	rec := get(app, "GET", pub.URL("avatars/a.png"), nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("public image: %d %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("security headers missing: %v", rec.Header())
	}
	for _, f := range []string{"page.html", "logo.svg"} {
		rec = get(app, "GET", pub.URL(f), nil)
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
			t.Fatalf("%s must download: %d %v", f, rec.Code, rec.Header())
		}
	}
	rec = get(app, "GET", pub.URL("avatars/a.png")+"?download=me.png", nil)
	if rec.Header().Get("Content-Disposition") != `attachment; filename=me.png` {
		t.Fatalf("download name: %q", rec.Header().Get("Content-Disposition"))
	}
	for _, target := range []string{pub.URL(".env"), "/files/pub/avatars", "/files/pub/missing.png", priv.URL("contracts/nda.pdf"), "/files/pub/../contracts/nda.pdf"} {
		if rec = get(app, "GET", target, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404, got %d", target, rec.Code)
		}
	}

	signed, _ := priv.TemporaryURL("contracts/nda.pdf", time.Minute)
	if rec = get(app, "GET", signed, nil); rec.Code != 200 || rec.Body.String() != "%PDF-1.4 secret" {
		t.Fatalf("signed private file: %d %q", rec.Code, rec.Body.String())
	}
	if rec = get(app, "GET", strings.Replace(signed, "signature=", "signature=0", 1), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("tampered signature: %d", rec.Code)
	}
	expired, _ := priv.TemporaryURL("contracts/nda.pdf", -time.Minute)
	if rec = get(app, "GET", expired, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("expired link: %d", rec.Code)
	}
	if rec = get(app, "HEAD", signed, nil); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: %d", rec.Code)
	}
}

func TestServeStorageDirectUploads(t *testing.T) {
	app, priv, _ := storageApp(t)
	put, _ := priv.PresignedPutURL("incoming/clip.mp4", time.Minute)
	if rec := get(app, "PUT", put, strings.NewReader("video-bytes")); rec.Code != http.StatusCreated {
		t.Fatalf("direct upload: %d %s", rec.Code, rec.Body.String())
	}
	if b, _ := priv.Get("incoming/clip.mp4"); string(b) != "video-bytes" {
		t.Fatalf("stored %q", b)
	}
	// an upload link can't be used to read, and a read link can't upload
	if rec := get(app, "GET", put, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("GET with an upload link: %d", rec.Code)
	}
	signed, _ := priv.TemporaryURL("incoming/other.mp4", time.Minute)
	if rec := get(app, "PUT", signed, strings.NewReader("x")); rec.Code != http.StatusForbidden {
		t.Fatalf("PUT with a read link: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(priv.Root(), "incoming", "other.mp4")); err == nil {
		t.Fatal("refused upload must not be stored")
	}
}
