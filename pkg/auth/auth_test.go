package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

func TestAuth_JWT(t *testing.T) {
	jwt := NewJWTManager("super-secret-key-32-bytes-long!", 1*time.Hour)

	token, err := jwt.GenerateToken("user_42", map[string]any{"role": "admin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	claims, err := jwt.VerifyToken(token)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}

	if claims.Sub != "user_42" {
		t.Fatalf("expected sub=user_42, got %s", claims.Sub)
	}
	if claims.Custom["role"] != "admin" {
		t.Fatalf("expected custom role=admin, got %v", claims.Custom["role"])
	}
}

func TestAuth_BearerGuard(t *testing.T) {
	jwt := NewJWTManager("guard-secret-key-32-bytes-long!!", 1*time.Hour)
	token, _ := jwt.GenerateToken("user_99")

	guard := BearerGuard(jwt)

	// 1. Authorized Request
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/protected", nil)
	req1.Header.Set("Authorization", "Bearer "+token)
	c1 := context.NewContext()
	c1.Reset(w1, req1)
	c1.SetHandlers([]context.HandlerFunc{
		guard,
		func(c *context.Context) error {
			user := User(c)
			if user == nil || user.GetAuthIdentifier() != "user_99" {
				t.Fatalf("expected authenticated user user_99, got %v", user)
			}
			return c.String(http.StatusOK, "OK")
		},
	})
	_ = c1.Next()

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// 2. Unauthorized Request (missing header)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/protected", nil)
	c2 := context.NewContext()
	c2.Reset(w2, req2)
	c2.SetHandlers([]context.HandlerFunc{guard})
	_ = c2.Next()

	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", w2.Code)
	}
}

func TestAuth_Gates(t *testing.T) {
	gm := NewGateManager()
	gm.Define("edit-post", func(user Authenticatable, args ...any) bool {
		if len(args) == 0 {
			return false
		}
		authorID, ok := args[0].(string)
		return ok && user.GetAuthIdentifier() == authorID
	})

	user := &SimpleUser{ID: "author_1"}

	if !gm.Allows("edit-post", user, "author_1") {
		t.Fatal("expected gate to allow author")
	}

	if gm.Allows("edit-post", user, "author_2") {
		t.Fatal("expected gate to deny non-author")
	}
}
