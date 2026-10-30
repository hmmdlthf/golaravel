package oauth

import (
	"net/http"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

// CallbackHandler is invoked when the OAuth2 provider redirects back with a verified User profile.
type CallbackHandler func(c *context.Context, user *User) error

// MountRoutes mounts standard redirect and callback routes for the given provider.
// Defaults:
// - GET /auth/{provider}/redirect
// - GET /auth/{provider}/callback
func MountRoutes(r router.RouteRegistrar, provider string, callback CallbackHandler, prefix ...string) {
	base := "/auth"
	if len(prefix) > 0 && prefix[0] != "" {
		base = strings.TrimRight(prefix[0], "/")
	}

	pName := strings.ToLower(provider)

	// Redirect to OAuth2 provider
	r.Get(base+"/"+pName+"/redirect", func(c *context.Context) error {
		return Driver(pName).Redirect(c)
	})

	// Callback handling
	r.Get(base+"/"+pName+"/callback", func(c *context.Context) error {
		user, err := Driver(pName).User(c)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error":   "oauth_error",
				"message": err.Error(),
			})
		}

		return callback(c, user)
	})
}
