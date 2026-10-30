package auth

import (
	"errors"
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

// AuthenticateBearer attempts to authenticate the request using a JWT Bearer header.
func AuthenticateBearer(c *context.Context, jwt *JWTManager, provider ...UserProvider) (Authenticatable, *JWTClaims, error) {
	authHeader := c.Header("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, nil, errors.New("Bearer token missing or invalid")
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	claims, err := jwt.VerifyToken(tokenString)
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

// BearerGuard returns a high-performance JWT bearer token authentication middleware.
// Ideal for Flutter (Mobile, Desktop, Web) and API clients.
func BearerGuard(jwt *JWTManager, provider ...UserProvider) context.HandlerFunc {
	return func(c *context.Context) error {
		user, claims, err := AuthenticateBearer(c, jwt, provider...)
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
