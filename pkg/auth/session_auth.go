package auth

import (
	"errors"
	"net/http"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/session"
)

// Session login for server-rendered sites, like Laravel's web guard. It
// needs session.StartSession on the route; the session stores the user id
// (session.AuthUserKey), which server-side drivers also record per session
// so DestroyUserSessions / LogoutOtherDevices work.

// ErrNoSession is returned when session.StartSession did not run.
var ErrNoSession = errors.New("auth: no session on the request (add session.StartSession)")

// Login logs user in on the request's session: the session id is
// regenerated (preventing fixation, the old id is deleted), the user id is
// stored, the CSRF token is renewed and auth.User(c) returns user.
func Login(c *context.Context, user Authenticatable) error {
	sess := session.FromContext(c)
	if sess == nil {
		return ErrNoSession
	}
	if err := session.ManagerFrom(c).Regenerate(sess); err != nil {
		return err
	}
	sess.Put(session.AuthUserKey, user.GetAuthIdentifier())
	c.Set("csrf_token", sess.RegenerateToken())
	c.Set(ContextUserKey, user)
	return nil
}

// Logout ends the request's session: its data is flushed and deleted from
// the store, it gets a new id and a new CSRF token.
func Logout(c *context.Context) error {
	sess := session.FromContext(c)
	if sess == nil {
		return ErrNoSession
	}
	if err := session.ManagerFrom(c).Invalidate(sess); err != nil {
		return err
	}
	c.Set("csrf_token", sess.Token())
	c.Set(ContextUserKey, nil)
	return nil
}

// LogoutOtherDevices ends every other session of the logged-in user,
// keeping the current one (for example after a password change). It
// returns session.ErrUserSessionsUnsupported with the cookie driver.
func LogoutOtherDevices(c *context.Context) error {
	sess := session.FromContext(c)
	if sess == nil {
		return ErrNoSession
	}
	uid := sess.GetString(session.AuthUserKey)
	if uid == "" {
		return nil
	}
	return session.ManagerFrom(c).DestroyUserSessions(uid, sess.ID())
}

// SessionUserID returns the id of the user logged in on the request's
// session, or "".
func SessionUserID(c *context.Context) string {
	return session.GetString(c, session.AuthUserKey)
}

// SessionGuardFunc returns a GuardFunc that authenticates the user stored
// in the session, loading them through provider (nil: a SimpleUser with
// the id). A user the provider no longer finds is logged out.
func SessionGuardFunc(provider UserProvider) GuardFunc {
	return func(c *context.Context) (Authenticatable, error) {
		uid := SessionUserID(c)
		if uid == "" {
			return nil, errors.New("Not logged in")
		}
		if provider == nil {
			return &SimpleUser{ID: uid}, nil
		}
		u, err := provider.FindByID(uid)
		if err != nil || u == nil {
			_ = Logout(c)
			return nil, errors.New("User not found")
		}
		return u, nil
	}
}

// SessionGuardOptions configures SessionGuard.
type SessionGuardOptions struct {
	// RedirectTo sends guests to this URL (for example "/login") instead
	// of a 401 JSON reply.
	RedirectTo string
}

// SessionGuard requires a user logged in on the session (see Login) and
// makes them available through auth.User(c).
func SessionGuard(provider UserProvider, opts ...SessionGuardOptions) context.HandlerFunc {
	var o SessionGuardOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	guard := SessionGuardFunc(provider)
	return func(c *context.Context) error {
		user, err := guard(c)
		if err != nil {
			if o.RedirectTo != "" {
				c.Abort()
				return c.Redirect(http.StatusFound, o.RedirectTo)
			}
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

// UserProviderFunc adapts a function to UserProvider.
type UserProviderFunc func(id string) (Authenticatable, error)

// FindByID calls f.
func (f UserProviderFunc) FindByID(id string) (Authenticatable, error) { return f(id) }
