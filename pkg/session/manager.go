package session

import (
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

const (
	ContextSessionKey = "session"
)

// Config configures the Session Manager.
type Config struct {
	Driver     Driver
	CookieName string
	Lifetime   time.Duration
	Path       string
	Domain     string
	Secure     bool
	// HttpOnly is kept for compatibility and is always on unless
	// AllowScriptAccess is set (a zero Config no longer turns it off).
	HttpOnly      bool
	SameSite      http.SameSite
	ExpireOnClose bool
	// Encrypt is kept for compatibility: the session cookie is always
	// encrypted unless DisableEncryption is set.
	Encrypt bool
	// SecretKey encrypts the session cookie. When empty, a random key is
	// generated per process (sessions do not survive a restart and are not
	// shared between instances) and a warning is logged. Set it from your
	// application key in production.
	SecretKey string

	// AllowScriptAccess drops HttpOnly so browser JavaScript can read the
	// session cookie. Avoid it.
	AllowScriptAccess bool
	// DisableEncryption stores the plain session id in the cookie (server
	// side drivers only; the cookie driver always encrypts).
	DisableEncryption bool
}

// insecureDefaultSecret is the key earlier versions shipped in DefaultConfig.
const insecureDefaultSecret = "change-this-default-session-secret-key-32-chars"

// DefaultConfig provides production-ready session defaults.
var DefaultConfig = Config{
	CookieName: "golaravel_session",
	Lifetime:   120 * time.Minute,
	Path:       "/",
	HttpOnly:   true,
	SameSite:   http.SameSiteLaxMode,
	Encrypt:    true,
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
		if userCfg.SameSite != 0 {
			c.SameSite = userCfg.SameSite
		}
		c.ExpireOnClose = userCfg.ExpireOnClose
		c.AllowScriptAccess = userCfg.AllowScriptAccess
		c.DisableEncryption = userCfg.DisableEncryption
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
	if la, ok := c.Driver.(lifetimeAware); ok {
		la.setLifetime(c.Lifetime)
	}
	// Secure by default whatever the caller's zero values say.
	c.HttpOnly = !c.AllowScriptAccess
	_, cookieDriver := c.Driver.(*CookieDriver)
	c.Encrypt = !c.DisableEncryption || cookieDriver
	if c.SecretKey == insecureDefaultSecret {
		slog.Warn("session: the built-in example SecretKey is public; using a random per-process key instead. Set Config.SecretKey.")
		c.SecretKey = ""
	}
	if c.SecretKey == "" {
		slog.Warn("session: no SecretKey configured; using a random per-process key (sessions end on restart). Set Config.SecretKey or APP_KEY.")
		c.SecretKey = generateRandomString(64)
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
	if err := m.write(s, data); err != nil {
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

// The global default manager is built on first use (not at package load),
// with its key taken from APP_KEY, so apps that configure their own manager
// never see a "no SecretKey" warning.
var (
	globalMu      sync.Mutex
	globalManager *Manager
)

// SetDefaultManager sets the application-wide session manager.
func SetDefaultManager(mgr *Manager) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalManager = mgr
}

// DefaultManager returns the global session manager, building one from
// DefaultConfig and APP_KEY the first time it is needed.
func DefaultManager() *Manager {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalManager == nil {
		globalManager = NewManager(Config{SecretKey: os.Getenv("APP_KEY")})
	}
	return globalManager
}
