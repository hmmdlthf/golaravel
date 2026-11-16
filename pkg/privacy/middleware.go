package privacy

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/context"
)

// TenantResolver extracts a tenant identifier from an incoming HTTP request context.
type TenantResolver func(c *context.Context) (string, error)

// HeaderResolver extracts the tenant identifier from an HTTP header (default: "X-Tenant-ID").
func HeaderResolver(headerName ...string) TenantResolver {
	hdr := "X-Tenant-ID"
	if len(headerName) > 0 && headerName[0] != "" {
		hdr = headerName[0]
	}

	return func(c *context.Context) (string, error) {
		val := c.Header(hdr)
		return strings.TrimSpace(val), nil
	}
}

// SubdomainResolver extracts the tenant identifier from the request Host subdomain.
// For example, with baseDomain "example.com", "acme.example.com" resolves to "acme".
func SubdomainResolver(baseDomain string) TenantResolver {
	cleanBase := strings.ToLower(strings.TrimPrefix(baseDomain, "."))

	return func(c *context.Context) (string, error) {
		host := c.Request.Host
		if colonIdx := strings.IndexByte(host, ':'); colonIdx != -1 {
			host = host[:colonIdx]
		}
		host = strings.ToLower(strings.TrimSpace(host))

		if !strings.HasSuffix(host, "."+cleanBase) {
			return "", nil
		}

		subdomain := strings.TrimSuffix(host, "."+cleanBase)
		if subdomain == "www" || subdomain == "" {
			return "", nil
		}

		return subdomain, nil
	}
}

// ParamResolver extracts the tenant identifier from a route parameter (e.g. ":tenant").
func ParamResolver(paramName string) TenantResolver {
	return func(c *context.Context) (string, error) {
		return strings.TrimSpace(c.Param(paramName)), nil
	}
}

// QueryResolver extracts the tenant identifier from a URL query parameter (e.g. "?tenant=acme").
func QueryResolver(paramName string) TenantResolver {
	return func(c *context.Context) (string, error) {
		return strings.TrimSpace(c.Query(paramName)), nil
	}
}

// JWTClaimResolver extracts the tenant identifier from active JWT bearer claims.
func JWTClaimResolver(claimKey ...string) TenantResolver {
	key := "tenant_id"
	if len(claimKey) > 0 && claimKey[0] != "" {
		key = claimKey[0]
	}

	return func(c *context.Context) (string, error) {
		claims := auth.Claims(c)
		if claims == nil || claims.Custom == nil {
			return "", nil
		}
		if val, exists := claims.Custom[key]; exists {
			return fmt.Sprint(val), nil
		}
		return "", nil
	}
}

// TenantMiddlewareOptions configures the behavior of TenantMiddleware.
type TenantMiddlewareOptions struct {
	Required        bool
	DefaultTenant   string
	OnMissingTenant func(c *context.Context) error
	// Authorize confirms the request may act for the resolved tenant (for
	// example that the signed-in user is a member). Header, query, route
	// parameter and subdomain resolvers take the tenant from the client,
	// so without this check any user can switch to another tenant's data.
	// Returning false answers 403. JWTClaimResolver reads a server-issued
	// claim and is safe without it.
	Authorize func(c *context.Context, tenantID string) bool
}

var warnUnauthorizedTenant sync.Once

// TenantMiddleware inspects requests using the provided resolvers in priority order.
// Once resolved, it injects the tenant ID into the GoLaravel context and stdlib request context.
func TenantMiddleware(resolvers ...TenantResolver) context.HandlerFunc {
	return TenantMiddlewareWithOptions(TenantMiddlewareOptions{Required: true}, resolvers...)
}

// TenantMiddlewareWithOptions creates a TenantMiddleware with custom options.
func TenantMiddlewareWithOptions(opts TenantMiddlewareOptions, resolvers ...TenantResolver) context.HandlerFunc {
	if len(resolvers) == 0 {
		resolvers = []TenantResolver{HeaderResolver()}
	}

	return func(c *context.Context) error {
		var tenantID string

		for _, resolver := range resolvers {
			id, err := resolver(c)
			if err == nil && id != "" {
				tenantID = id
				break
			}
		}

		if tenantID == "" && opts.DefaultTenant != "" {
			tenantID = opts.DefaultTenant
		}

		if tenantID == "" {
			if opts.Required {
				if opts.OnMissingTenant != nil {
					return opts.OnMissingTenant(c)
				}
				c.AbortWithStatus(http.StatusForbidden)
				return c.JSON(http.StatusForbidden, map[string]string{
					"error":   "Tenant Required",
					"message": "A valid tenant identifier is required to access this resource",
				})
			}
			return c.Next()
		}

		if opts.Authorize != nil {
			if !opts.Authorize(c, tenantID) {
				c.AbortWithStatus(http.StatusForbidden)
				return c.JSON(http.StatusForbidden, map[string]string{
					"error":   "Tenant Forbidden",
					"message": "You do not have access to this tenant",
				})
			}
		} else {
			warnUnauthorizedTenant.Do(func() {
				slog.Warn("privacy: TenantMiddleware has no Authorize check; the tenant chosen by the client is trusted as-is. Set TenantMiddlewareOptions.Authorize unless the tenant comes from JWTClaimResolver.")
			})
		}

		// Inject into GoLaravel context
		c.Set("tenant_id", tenantID)

		// Inject into underlying http.Request standard library context for Ent ORM traversal
		reqCtx := WithTenantID(c.Context(), tenantID)
		c.Request = c.Request.WithContext(reqCtx)

		return c.Next()
	}
}
