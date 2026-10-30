package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
)

// GuardFunc represents an authentication strategy that attempts to extract and return
// an Authenticatable user from a request context without writing an HTTP response.
type GuardFunc func(c *context.Context) (Authenticatable, error)

// BearerGuardFunc returns a GuardFunc that authenticates requests using a JWT Bearer token.
func BearerGuardFunc(jwt *JWTManager, provider ...UserProvider) GuardFunc {
	return func(c *context.Context) (Authenticatable, error) {
		user, claims, err := AuthenticateBearer(c, jwt, provider...)
		if err != nil {
			return nil, err
		}
		c.Set(ContextTokenKey, claims)
		return user, nil
	}
}

// AuthenticateCookie attempts to authenticate the request using a JWT stored in an HTTP cookie.
func AuthenticateCookie(c *context.Context, jwt *JWTManager, cookieName string, provider ...UserProvider) (Authenticatable, *JWTClaims, error) {
	cookieVal, err := c.Cookie(cookieName)
	if err != nil || cookieVal == "" {
		return nil, nil, errors.New("Authentication cookie missing")
	}

	claims, err := jwt.VerifyToken(cookieVal)
	if err != nil {
		return nil, nil, err
	}

	var user Authenticatable = &SimpleUser{ID: claims.Sub}
	if len(provider) > 0 && provider[0] != nil {
		u, err := provider[0].FindByID(claims.Sub)
		if err != nil || u == nil {
			return nil, nil, errors.New("User not found")
		}
		user = u
	}

	return user, claims, nil
}

// CookieGuardFunc returns a GuardFunc that authenticates requests using a JWT in an HTTP-only cookie.
func CookieGuardFunc(jwt *JWTManager, cookieName string, provider ...UserProvider) GuardFunc {
	return func(c *context.Context) (Authenticatable, error) {
		user, claims, err := AuthenticateCookie(c, jwt, cookieName, provider...)
		if err != nil {
			return nil, err
		}
		c.Set(ContextTokenKey, claims)
		return user, nil
	}
}

// CookieGuard returns an authentication middleware that extracts and validates a JWT token
// stored in an HTTP-only cookie. Ideal for browser-based web applications and micro-SSRs.
func CookieGuard(jwt *JWTManager, cookieName string, provider ...UserProvider) context.HandlerFunc {
	return func(c *context.Context) error {
		user, claims, err := AuthenticateCookie(c, jwt, cookieName, provider...)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error":   "Unauthorized",
				"message": err.Error(),
			})
		}

		c.Set(ContextTokenKey, claims)
		c.Set(ContextUserKey, user)
		return c.Next()
	}
}

// APIKeyValidator is a function that validates an API key and returns the authenticatable entity.
type APIKeyValidator func(key string) (Authenticatable, error)

// AuthenticateAPIKey extracts and validates an API key from request headers.
func AuthenticateAPIKey(c *context.Context, validator APIKeyValidator, headerName ...string) (Authenticatable, error) {
	hdr := "X-API-Key"
	if len(headerName) > 0 && headerName[0] != "" {
		hdr = headerName[0]
	}

	key := c.Header(hdr)
	if key == "" {
		// Check Authorization header fallback: "ApiKey <key>"
		authHdr := c.Header("Authorization")
		if strings.HasPrefix(authHdr, "ApiKey ") {
			key = strings.TrimPrefix(authHdr, "ApiKey ")
		}
	}

	if key == "" {
		return nil, errors.New("API key missing")
	}

	user, err := validator(key)
	if err != nil || user == nil {
		return nil, errors.New("Invalid API key")
	}

	return user, nil
}

// APIKeyGuardFunc returns a GuardFunc that authenticates requests using an API key header.
func APIKeyGuardFunc(validator APIKeyValidator, headerName ...string) GuardFunc {
	return func(c *context.Context) (Authenticatable, error) {
		return AuthenticateAPIKey(c, validator, headerName...)
	}
}

// APIKeyGuard authenticates requests via a header API key (e.g. X-API-Key or Authorization: ApiKey <key>).
func APIKeyGuard(validator APIKeyValidator, headerName ...string) context.HandlerFunc {
	return func(c *context.Context) error {
		user, err := AuthenticateAPIKey(c, validator, headerName...)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error":   "Unauthorized",
				"message": err.Error(),
			})
		}

		c.Set(ContextUserKey, user)
		return c.Next()
	}
}

// MultiGuard chains multiple GuardFuncs (e.g. BearerGuardFunc, CookieGuardFunc, APIKeyGuardFunc).
// If any guard successfully authenticates the request, execution proceeds.
// If all guards fail, a 401 Unauthorized response is sent.
func MultiGuard(guards ...GuardFunc) context.HandlerFunc {
	return func(c *context.Context) error {
		for _, guard := range guards {
			user, err := guard(c)
			if err == nil && user != nil {
				c.Set(ContextUserKey, user)
				return c.Next()
			}
		}

		c.AbortWithStatus(http.StatusUnauthorized)
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error":   "Unauthorized",
			"message": "All authentication guards failed",
		})
	}
}
