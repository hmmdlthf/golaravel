package auth

import (
	"net/http"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
)

const (
	ContextUserKey  = "auth_user"
	ContextTokenKey = "auth_token"
)

// Authenticatable represents any user entity that can be authenticated.
type Authenticatable interface {
	GetAuthIdentifier() string
}

// SimpleUser provides a default Authenticatable implementation.
type SimpleUser struct {
	ID    string
	Email string
}

func (u *SimpleUser) GetAuthIdentifier() string {
	return u.ID
}

// UserProvider defines the interface to fetch a user by identifier (e.g. from Ent ORM).
type UserProvider interface {
	FindByID(id string) (Authenticatable, error)
}

// BearerGuard returns a high-performance JWT bearer token authentication middleware.
// Ideal for Flutter (Mobile, Desktop, Web) and API clients.
func BearerGuard(jwt *JWTManager, provider ...UserProvider) context.HandlerFunc {
	return func(c *context.Context) error {
		authHeader := c.Header("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatus(http.StatusUnauthorized)
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error":   "Unauthorized",
				"message": "Bearer token missing or invalid",
			})
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := jwt.VerifyToken(tokenString)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error":   "Unauthorized",
				"message": err.Error(),
			})
		}

		c.Set(ContextTokenKey, claims)

		if len(provider) > 0 && provider[0] != nil {
			user, err := provider[0].FindByID(claims.Sub)
			if err != nil || user == nil {
				c.AbortWithStatus(http.StatusUnauthorized)
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error":   "Unauthorized",
					"message": "User not found",
				})
			}
			c.Set(ContextUserKey, user)
		} else {
			c.Set(ContextUserKey, &SimpleUser{ID: claims.Sub})
		}

		return c.Next()
	}
}

// User retrieves the authenticated user from the context.
func User(c *context.Context) Authenticatable {
	if val, ok := c.Get(ContextUserKey); ok {
		if u, ok := val.(Authenticatable); ok {
			return u
		}
	}
	return nil
}

// Claims retrieves the active JWT claims from the context.
func Claims(c *context.Context) *JWTClaims {
	if val, ok := c.Get(ContextTokenKey); ok {
		if cl, ok := val.(*JWTClaims); ok {
			return cl
		}
	}
	return nil
}
