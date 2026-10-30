package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

func TestAuth_CookieGuard(t *testing.T) {
	jwt := NewJWTManager("cookie-guard-secret-32-bytes-long", 1*time.Hour)
	token, _ := jwt.GenerateToken("user_cookie_1")

	guard := CookieGuard(jwt, "auth_session")

	// 1. Success with valid cookie
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/dashboard", nil)
	req1.AddCookie(&http.Cookie{Name: "auth_session", Value: token})
	c1 := context.NewContext()
	c1.Reset(w1, req1)
	c1.SetHandlers([]context.HandlerFunc{
		guard,
		func(c *context.Context) error {
			user := User(c)
			if user == nil || user.GetAuthIdentifier() != "user_cookie_1" {
				t.Fatalf("expected user_cookie_1, got %v", user)
			}
			return c.String(http.StatusOK, "OK")
		},
	})
	_ = c1.Next()
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// 2. Failure with missing cookie
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/dashboard", nil)
	c2 := context.NewContext()
	c2.Reset(w2, req2)
	c2.SetHandlers([]context.HandlerFunc{guard})
	_ = c2.Next()
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w2.Code)
	}
}

func TestAuth_APIKeyGuard(t *testing.T) {
	validator := func(key string) (Authenticatable, error) {
		if key == "secret-api-key-123" {
			return &SimpleUser{ID: "api_client_1"}, nil
		}
		return nil, errors.New("invalid key")
	}

	guard := APIKeyGuard(validator)

	// 1. Success via X-API-Key
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api/v1/data", nil)
	req1.Header.Set("X-API-Key", "secret-api-key-123")
	c1 := context.NewContext()
	c1.Reset(w1, req1)
	c1.SetHandlers([]context.HandlerFunc{
		guard,
		func(c *context.Context) error {
			user := User(c)
			if user == nil || user.GetAuthIdentifier() != "api_client_1" {
				t.Fatalf("expected api_client_1, got %v", user)
			}
			return c.String(http.StatusOK, "OK")
		},
	})
	_ = c1.Next()
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// 2. Success via Authorization: ApiKey <key>
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v1/data", nil)
	req2.Header.Set("Authorization", "ApiKey secret-api-key-123")
	c2 := context.NewContext()
	c2.Reset(w2, req2)
	c2.SetHandlers([]context.HandlerFunc{
		guard,
		func(c *context.Context) error {
			return c.String(http.StatusOK, "OK")
		},
	})
	_ = c2.Next()
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for ApiKey header, got %d", w2.Code)
	}

	// 3. Failure with invalid key
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/api/v1/data", nil)
	req3.Header.Set("X-API-Key", "wrong-key")
	c3 := context.NewContext()
	c3.Reset(w3, req3)
	c3.SetHandlers([]context.HandlerFunc{guard})
	_ = c3.Next()
	if w3.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong key, got %d", w3.Code)
	}
}

func TestAuth_MultiGuard(t *testing.T) {
	jwt := NewJWTManager("multi-guard-secret-32-bytes-long!", 1*time.Hour)
	token, _ := jwt.GenerateToken("user_bearer")

	multi := MultiGuard(
		BearerGuardFunc(jwt),
		CookieGuardFunc(jwt, "session_id"),
		APIKeyGuardFunc(func(key string) (Authenticatable, error) {
			if key == "api-token-99" {
				return &SimpleUser{ID: "user_api"}, nil
			}
			return nil, errors.New("invalid")
		}),
	)

	// 1. Authenticates via Bearer
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/resource", nil)
	req1.Header.Set("Authorization", "Bearer "+token)
	c1 := context.NewContext()
	c1.Reset(w1, req1)
	c1.SetHandlers([]context.HandlerFunc{
		multi,
		func(c *context.Context) error {
			if User(c).GetAuthIdentifier() != "user_bearer" {
				t.Fatalf("expected user_bearer")
			}
			return c.String(http.StatusOK, "OK")
		},
	})
	_ = c1.Next()
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// 2. Authenticates via API Key when Bearer is missing
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/resource", nil)
	req2.Header.Set("X-API-Key", "api-token-99")
	c2 := context.NewContext()
	c2.Reset(w2, req2)
	c2.SetHandlers([]context.HandlerFunc{
		multi,
		func(c *context.Context) error {
			if User(c).GetAuthIdentifier() != "user_api" {
				t.Fatalf("expected user_api")
			}
			return c.String(http.StatusOK, "OK")
		},
	})
	_ = c2.Next()
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	// 3. Fails when none match
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/resource", nil)
	c3 := context.NewContext()
	c3.Reset(w3, req3)
	c3.SetHandlers([]context.HandlerFunc{multi})
	_ = c3.Next()
	if w3.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w3.Code)
	}
}
