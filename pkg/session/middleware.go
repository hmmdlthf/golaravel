package session

import (
	"crypto/subtle"
	"net/http"

	"github.com/golaravel/golaravel/pkg/context"
)

// StartSession returns a middleware that initializes stateful session tracking for requests.
// It loads existing session state from encrypted cookies or drivers, exposes the session on
// the request context, and persists changes (along with flash aging) upon request completion.
//
// With no manager it uses DefaultManager, resolved on the first request, so
// passing your own manager never builds (or warns about) the default one.
func StartSession(manager ...*Manager) context.HandlerFunc {
	var given *Manager
	if len(manager) > 0 {
		given = manager[0]
	}

	return func(c *context.Context) error {
		mgr := given
		if mgr == nil {
			mgr = DefaultManager()
		}
		sess := mgr.Start(c)
		c.Set(contextManagerKey, mgr)

		// Share CSRF token into context for Templ views / forms
		c.Set("csrf_token", sess.Token())

		var saved bool
		saveOnce := func() {
			if !saved {
				saved = true
				_ = mgr.Save(c, sess)
			}
		}

		// Register before-write hook so cookie header is written before WriteHeader
		c.Response.Before(saveOnce)

		// Execute downstream middleware and handlers
		err := c.Next()

		// If headers were not yet committed, save now
		saveOnce()

		return err
	}
}

// VerifyCSRFToken enforces stateful CSRF verification using the active session's token.
func VerifyCSRFToken() context.HandlerFunc {
	return func(c *context.Context) error {
		sess := FromContext(c)
		if sess == nil {
			return c.Next()
		}

		// Safe HTTP methods do not require CSRF verification
		method := c.Request.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			return c.Next()
		}

		expectedToken := sess.Token()

		// Check headers and form fields
		token := c.Header("X-CSRF-TOKEN")
		if token == "" {
			token = c.Header("X-XSRF-TOKEN")
		}
		if token == "" {
			token = c.Request.FormValue("_token")
		}

		if token == "" || expectedToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			c.AbortWithStatus(http.StatusForbidden)
			return c.JSON(http.StatusForbidden, map[string]string{
				"error":   "CSRF Token Mismatch",
				"message": "The page has expired due to inactivity. Please reload and try again.",
			})
		}

		return c.Next()
	}
}
