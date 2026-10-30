package gltest_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
	gltest "github.com/golaravel/golaravel/pkg/testing"
)

func TestClient_Get_And_StatusAssertions(t *testing.T) {
	app := golaravel.New()
	app.Get("/hello", func(c *context.Context) error {
		return c.String(http.StatusOK, "Hello World from GoLaravel Test!")
	})
	app.Get("/not-found", func(c *context.Context) error {
		return c.String(http.StatusNotFound, "Item not found")
	})
	app.Get("/server-error", func(c *context.Context) error {
		return c.String(http.StatusInternalServerError, "Database down")
	})

	client := gltest.New(t, app)

	// 1. AssertOK & AssertSee
	client.Get("/hello").
		AssertOK().
		AssertSuccessful().
		AssertSee("Hello World").
		AssertSeeInOrder("Hello", "World", "GoLaravel").
		AssertDontSee("Error")

	// 2. AssertNotFound
	client.Get("/not-found").
		AssertNotFound().
		AssertClientError().
		AssertSee("Item not found")

	// 3. AssertServerError
	client.Get("/server-error").
		AssertServerError().
		AssertSee("Database down")
}

func TestClient_PostJSON_And_PathAssertions(t *testing.T) {
	app := golaravel.New()
	app.Post("/api/users", func(c *context.Context) error {
		type Req struct {
			Name string `json:"name"`
		}
		var r Req
		if err := c.BindJSON(&r); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusCreated, map[string]any{
			"id": 101,
			"user": map[string]any{
				"name":  r.Name,
				"roles": []string{"admin", "editor"},
			},
			"active": true,
		})
	})

	client := app.Test(t)

	res := client.PostJSON("/api/users", map[string]string{
		"name": "Jane Doe",
	})

	// Assertions
	res.AssertCreated().
		AssertSuccessful().
		AssertHeader("Content-Type", "application/json; charset=utf-8").
		AssertContentType("application/json").
		AssertJsonPath("id", 101).
		AssertJsonPath("user.name", "Jane Doe").
		AssertJsonPath("user.roles.0", "admin").
		AssertJsonPath("user.roles.1", "editor").
		AssertJsonPath("active", true).
		AssertJsonCount(2, "user.roles").
		AssertJsonMissingPath("user.nonexistent").
		AssertJson(map[string]any{
			"id": 101,
			"user": map[string]any{
				"name": "Jane Doe",
			},
		})

	// Decode target struct
	var dest struct {
		ID int `json:"id"`
	}
	if err := res.DecodeJson(&dest); err != nil {
		t.Fatalf("failed decoding json: %v", err)
	}
	if dest.ID != 101 {
		t.Fatalf("expected ID 101, got %d", dest.ID)
	}
}

func TestClient_ValidationErrors(t *testing.T) {
	app := golaravel.New()
	app.Post("/api/submit", func(c *context.Context) error {
		return c.JSON(http.StatusUnprocessableEntity, map[string]any{
			"message": "The given data was invalid.",
			"errors": map[string]any{
				"email":    []string{"The email field is required."},
				"password": []string{"The password must be at least 8 characters."},
			},
		})
	})

	client := app.Test(t)

	client.PostJSON("/api/submit", map[string]any{}).
		AssertUnprocessable().
		AssertClientError().
		AssertJsonValidationErrors("email", "password")
}

func TestClient_Headers_And_Cookies(t *testing.T) {
	app := golaravel.New()
	app.Get("/profile", func(c *context.Context) error {
		auth := c.Header("Authorization")
		if auth != "Bearer secret-token-123" {
			return c.String(http.StatusUnauthorized, "Unauthorized")
		}

		c.SetHeader("X-Framework", "GoLaravel")
		c.SetCookie("session_id", "sess_abc123", 3600, "/", "", false, true)

		return c.String(http.StatusOK, "Profile accessed")
	})

	// Test with client-level default token
	client := gltest.New(t, app).WithToken("secret-token-123")

	res := client.Get("/profile").
		AssertOK().
		AssertHeader("X-Framework", "GoLaravel").
		AssertHeaderMissing("X-Missing-Header").
		AssertCookie("session_id", "sess_abc123").
		AssertCookieMissing("nonexistent_cookie")

	if cookie := res.Cookie("session_id"); cookie == nil || cookie.Value != "sess_abc123" {
		t.Fatalf("expected cookie session_id to be retrieved, got %v", cookie)
	}
}

func TestClient_Redirect(t *testing.T) {
	app := golaravel.New()
	app.Get("/old-path", func(c *context.Context) error {
		return c.Redirect(http.StatusFound, "/new-path")
	})

	client := app.Test(t)

	client.Get("/old-path").
		AssertFound().
		AssertRedirect("/new-path")
}

func TestClient_Form_And_FileUpload(t *testing.T) {
	app := golaravel.New()
	app.Post("/upload", func(c *context.Context) error {
		title := c.FormValue("title")
		file, err := c.FormFile("document")
		if err != nil {
			return c.String(http.StatusBadRequest, "Missing document: %s", err.Error())
		}

		f, err := file.Open()
		if err != nil {
			return c.String(http.StatusInternalServerError, "%s", err.Error())
		}
		defer f.Close()

		data, err := io.ReadAll(f)
		if err != nil {
			return c.String(http.StatusInternalServerError, "%s", err.Error())
		}

		return c.JSON(http.StatusOK, map[string]any{
			"title":    title,
			"filename": file.Filename,
			"size":     len(data),
		})
	})

	client := app.Test(t)

	fileBytes := []byte("%PDF-1.4 Mock Upload Document Content")
	res := client.Request("POST", "/upload").
		WithForm(map[string]string{"title": "Quarterly Report"}).
		WithFile("document", "report.pdf", fileBytes, "application/pdf").
		Send()

	res.AssertOK().
		AssertJsonPath("title", "Quarterly Report").
		AssertJsonPath("filename", "report.pdf").
		AssertJsonPath("size", len(fileBytes))
}

func TestClient_Dump(t *testing.T) {
	app := golaravel.New()
	app.Get("/debug", func(c *context.Context) error {
		c.SetHeader("X-Debug-Mode", "true")
		return c.JSON(http.StatusOK, map[string]string{"debug": "active"})
	})

	client := app.Test(t)
	// Dump and DumpBody should execute cleanly without error
	client.Get("/debug").
		AssertOK().
		Dump().
		DumpBody()
}
