package session

import (
	"net/http"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

const (
	ContextSessionKey = "session"
)

// Config configures the Session Manager.
type Config struct {
	Driver        Driver
	CookieName    string
	Lifetime      time.Duration
	Path          string
	Domain        string
	Secure        bool
	HttpOnly      bool
	SameSite      http.SameSite
	ExpireOnClose bool
	Encrypt       bool
	SecretKey     string
}

// DefaultConfig provides production-ready session defaults.
var DefaultConfig = Config{
	CookieName: "golaravel_session",
	Lifetime:   120 * time.Minute,
	Path:       "/",
	HttpOnly:   true,
	SameSite:   http.SameSiteLaxMode,
	Encrypt:    true,
	SecretKey:  "change-this-default-session-secret-key-32-chars",
}

// Manager coordinates session lifecycles, encryption, and driver storage.
type Manager struct {
	cfg       Config
	encrypter *Encrypter
	driver    Driver
}

// NewManager creates a configured session manager.
func NewManager(cfg ...Config) *Manager {
	c := DefaultConfig
	if len(cfg) > 0 {
		userCfg := cfg[0]
		if userCfg.CookieName != "" {
			c.CookieName = userCfg.CookieName
		}
		if userCfg.Lifetime > 0 {
			c.Lifetime = userCfg.Lifetime
		}
		if userCfg.Path != "" {
			c.Path = userCfg.Path
		}
		if userCfg.Domain != "" {
			c.Domain = userCfg.Domain
		}
		c.Secure = userCfg.Secure
		c.HttpOnly = userCfg.HttpOnly
		if userCfg.SameSite != 0 {
			c.SameSite = userCfg.SameSite
		}
		c.ExpireOnClose = userCfg.ExpireOnClose
		c.Encrypt = userCfg.Encrypt
		if userCfg.SecretKey != "" {
			c.SecretKey = userCfg.SecretKey
		}
		if userCfg.Driver != nil {
			c.Driver = userCfg.Driver
		}
	}

	if c.Driver == nil {
		c.Driver = NewMemoryDriver()
	}

	return &Manager{
		cfg:       c,
		encrypter: NewEncrypter(c.SecretKey),
		driver:    c.Driver,
	}
}

// Start retrieves an existing session from request cookies or initializes a new session.
func (m *Manager) Start(c *context.Context) *Session {
	var sess *Session

	cookieVal, err := c.Cookie(m.cfg.CookieName)
	if err == nil && cookieVal != "" {
		sess = m.loadSession(cookieVal)
	}

	if sess == nil {
		sess = NewSession()
	}

	c.Set(ContextSessionKey, sess)
	return sess
}

func (m *Manager) loadSession(cookieVal string) *Session {
	// 1. If CookieDriver, the entire session state is inside the encrypted cookie
	if _, isCookieDriver := m.driver.(*CookieDriver); isCookieDriver {
		raw, err := m.encrypter.Decrypt(cookieVal)
		if err != nil {
			return nil
		}
		var s Session
		if err := s.UnmarshalJSON(raw); err != nil {
			return nil
		}
		return &s
	}

	// 2. Server-side driver (Memory/File): Cookie contains session ID (encrypted or plain)
	sessID := cookieVal
	if m.cfg.Encrypt {
		decrypted, err := m.encrypter.Decrypt(cookieVal)
		if err != nil {
			return nil
		}
		sessID = string(decrypted)
	}

	data, err := m.driver.Read(sessID)
	if err != nil {
		return nil
	}

	var s Session
	if err := s.UnmarshalJSON(data); err != nil {
		return nil
	}
	return &s
}

// Save serializes session state, persists to the driver, and writes the response cookie.
func (m *Manager) Save(c *context.Context, s *Session) error {
	// Age flash messages
	s.AgeFlash()

	data, err := s.MarshalJSON()
	if err != nil {
		return err
	}

	maxAge := int(m.cfg.Lifetime.Seconds())
	if m.cfg.ExpireOnClose {
		maxAge = 0
	}

	// Cookie Driver: encrypt the entire session payload
	if _, isCookieDriver := m.driver.(*CookieDriver); isCookieDriver {
		encrypted, err := m.encrypter.Encrypt(data)
		if err != nil {
			return err
		}
		m.writeCookie(c, encrypted, maxAge)
		return nil
	}

	// Server Driver: write to storage engine and set session ID cookie
	if err := m.driver.Write(s.ID(), data); err != nil {
		return err
	}

	cookieVal := s.ID()
	if m.cfg.Encrypt {
		encrypted, err := m.encrypter.Encrypt([]byte(s.ID()))
		if err != nil {
			return err
		}
		cookieVal = encrypted
	}

	m.writeCookie(c, cookieVal, maxAge)
	return nil
}

func (m *Manager) writeCookie(c *context.Context, value string, maxAge int) {
	http.SetCookie(&c.Response, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    value,
		Path:     m.cfg.Path,
		Domain:   m.cfg.Domain,
		MaxAge:   maxAge,
		Secure:   m.cfg.Secure,
		HttpOnly: m.cfg.HttpOnly,
		SameSite: m.cfg.SameSite,
	})
}

// FromContext retrieves the active session from the request context.
func FromContext(c *context.Context) *Session {
	if val, ok := c.Get(ContextSessionKey); ok {
		if s, ok := val.(*Session); ok {
			return s
		}
	}
	return nil
}

// Global default manager
var globalManager = NewManager()

// SetDefaultManager sets the application-wide session manager.
func SetDefaultManager(mgr *Manager) {
	globalManager = mgr
}

// DefaultManager returns the global session manager.
func DefaultManager() *Manager {
	return globalManager
}
