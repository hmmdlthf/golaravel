package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

const (
	CSRFCookieName = "XSRF-TOKEN"
	CSRFHeaderName = "X-XSRF-TOKEN"
	CSRFFormField  = "_token"
	CSRFContextKey = "csrf_token"
)

// CSRF provides Cross-Site Request Forgery protection using Double Submit Cookie pattern.
func CSRF() context.HandlerFunc {
	return func(c *context.Context) error {
		// Try to read existing cookie
		var token string
		if cookie, err := c.Request.Cookie(CSRFCookieName); err == nil && cookie.Value != "" {
			token = cookie.Value
		} else {
			// Generate new 32-byte secure random token
			bytes := make([]byte, 32)
			_, _ = rand.Read(bytes)
			token = hex.EncodeToString(bytes)

			http.SetCookie(&c.Response, &http.Cookie{
				Name:     CSRFCookieName,
				Value:    token,
				Path:     "/",
				HttpOnly: false, // Accessible by JS frontend (Inertia/Axios)
				Secure:   c.Request.TLS != nil || strings.EqualFold(c.Header("X-Forwarded-Proto"), "https"),
				SameSite: http.SameSiteLaxMode,
				Expires:  time.Now().Add(24 * time.Hour),
			})
		}

		// Inject into context for Templ views / forms
		c.Set(CSRFContextKey, token)

		// For safe HTTP methods (GET, HEAD, OPTIONS), verification is skipped
		method := c.Request.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			return c.Next()
		}

		// Verify token on state-changing requests (POST, PUT, PATCH, DELETE)
		headerToken := c.Header(CSRFHeaderName)
		if headerToken == "" {
			headerToken = c.Header("X-CSRF-TOKEN")
		}
		if headerToken == "" {
			headerToken = c.Request.FormValue(CSRFFormField)
		}

		if headerToken == "" || subtle.ConstantTimeCompare([]byte(headerToken), []byte(token)) != 1 {
			c.AbortWithStatus(http.StatusForbidden)
			return c.JSON(http.StatusForbidden, map[string]string{
				"error":   "Forbidden",
				"message": "CSRF token mismatch",
			})
		}

		return c.Next()
	}
}

// Token retrieves the current CSRF token from the context.
func Token(c *context.Context) string {
	if val, ok := c.Get(CSRFContextKey); ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}
