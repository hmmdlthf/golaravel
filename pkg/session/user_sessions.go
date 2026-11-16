package session

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/context"
)

// AuthUserKey is the session attribute holding the logged-in user's id. The
// Manager copies it to the driver on Save, so server-side drivers can find
// (and end) every session of a user, like Laravel's sessions.user_id.
const AuthUserKey = "_auth_user_id"

// ErrUserSessionsUnsupported is returned by DestroyUserSessions when the
// driver cannot list sessions by user (the cookie driver keeps sessions in
// the browser, so the server has nothing to delete).
var ErrUserSessionsUnsupported = errors.New("session: driver cannot destroy sessions by user")

// UserSessionDriver is implemented by drivers that link sessions to a user.
// The memory, file, SQL and Redis drivers implement it.
type UserSessionDriver interface {
	Driver
	// WriteUser stores the session like Write and records its user id
	// ("" for a guest session).
	WriteUser(id string, data []byte, userID string) error
	// DestroyUser deletes every session of userID except the ids listed in
	// except.
	DestroyUser(userID string, except ...string) error
}

// DestroyUserSessions ends every session of userID, except the ids given
// (pass the current session's id to log out other devices). It returns
// ErrUserSessionsUnsupported for drivers that cannot do this.
func (m *Manager) DestroyUserSessions(userID string, except ...string) error {
	if userID == "" {
		return nil
	}
	ud, ok := m.driver.(UserSessionDriver)
	if !ok {
		return ErrUserSessionsUnsupported
	}
	return ud.DestroyUser(userID, except...)
}

// Regenerate gives s a new id (preventing session fixation) and deletes
// the old one from the store. With flush set, its data is cleared too.
func (m *Manager) Regenerate(s *Session, flush ...bool) error {
	old := s.ID()
	_ = s.Regenerate(flush...)
	if _, isCookie := m.driver.(*CookieDriver); isCookie || old == "" {
		return nil
	}
	return m.driver.Destroy(old)
}

// Invalidate flushes s, deletes it from the store, gives it a new id and
// a new CSRF token: what a logout does.
func (m *Manager) Invalidate(s *Session) error {
	if err := m.Regenerate(s, true); err != nil {
		return err
	}
	s.RegenerateToken()
	return nil
}

// Manager returns the session manager that started the request's session
// (set by StartSession), or the default manager.
func ManagerFrom(c *context.Context) *Manager {
	if v, ok := c.Get(contextManagerKey); ok {
		if m, ok := v.(*Manager); ok {
			return m
		}
	}
	return DefaultManager()
}

const contextManagerKey = "session_manager"

func (m *Manager) write(s *Session, data []byte) error {
	if ud, ok := m.driver.(UserSessionDriver); ok {
		return ud.WriteUser(s.ID(), data, s.GetString(AuthUserKey))
	}
	return m.driver.Write(s.ID(), data)
}

func excluded(id string, except []string) bool {
	for _, e := range except {
		if e == id {
			return true
		}
	}
	return false
}

// --- memory ---

func (d *MemoryDriver) WriteUser(id string, data []byte, userID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sessions[id] = memorySessionItem{data: data, userID: userID, updatedAt: nowFunc()}
	return nil
}

func (d *MemoryDriver) DestroyUser(userID string, except ...string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, item := range d.sessions {
		if item.userID == userID && !excluded(id, except) {
			delete(d.sessions, id)
		}
	}
	return nil
}

// --- file ---

// WriteUser for files: the user id lives in the payload, so it is the same
// as Write.
func (d *FileDriver) WriteUser(id string, data []byte, _ string) error { return d.Write(id, data) }

// DestroyUser scans the session directory and removes the user's sessions.
// It reads every file, so prefer the SQL or Redis driver for many sessions.
func (d *FileDriver) DestroyUser(userID string, except ...string) error {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !validSessionID(e.Name()) || excluded(e.Name(), except) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(d.dir, e.Name()))
		if err != nil {
			continue
		}
		if payloadUserID(raw) == userID {
			_ = os.Remove(filepath.Join(d.dir, e.Name()))
		}
	}
	return nil
}

func payloadUserID(raw []byte) string {
	var p struct {
		Attributes map[string]any `json:"attributes"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	if v, ok := p.Attributes[AuthUserKey].(string); ok {
		return v
	}
	return ""
}

// --- redis ---

// userKey indexes a session under its user: <prefix>user:<hex uid>:<id>.
// The uid is hex-encoded so it can never contain SCAN glob characters.
func (d *RedisDriver) userPattern(userID string) string {
	return d.prefix + "user:" + hex.EncodeToString([]byte(userID)) + ":"
}

func (d *RedisDriver) WriteUser(id string, data []byte, userID string) error {
	if err := d.Write(id, data); err != nil || userID == "" {
		return err
	}
	args := []string{"SET", d.userPattern(userID) + id, "1"}
	if d.lifetime > 0 {
		args = append(args, "PX", strconv.FormatInt(d.lifetime.Milliseconds(), 10))
	}
	_, err := d.client.Do(args...)
	return err
}

// DestroyUser deletes the user's indexed sessions. Each session is checked
// before deletion, so a stale index entry never ends another user's session.
func (d *RedisDriver) DestroyUser(userID string, except ...string) error {
	base := d.userPattern(userID)
	return d.client.Scan(base+"*", func(keys []string) error {
		for _, k := range keys {
			id := strings.TrimPrefix(k, base)
			if excluded(id, except) {
				continue
			}
			if raw, err := d.Read(id); err == nil && payloadUserID(raw) == userID {
				if err := d.Destroy(id); err != nil {
					return err
				}
			}
			if _, err := d.client.Do("DEL", k); err != nil {
				return err
			}
		}
		return nil
	})
}
