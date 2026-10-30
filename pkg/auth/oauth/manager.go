package oauth

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// FactoryFunc is a constructor function for instantiating an OAuth2 provider.
type FactoryFunc func(cfg Config) Provider

// Manager orchestrates OAuth2 provider creation, configuration, and driver resolution.
type Manager struct {
	mu        sync.RWMutex
	factories map[string]FactoryFunc
	configs   map[string]Config
}

// Global default Manager
var defaultManager = NewManager()

// NewManager creates a new OAuth Manager with built-in provider factories.
func NewManager() *Manager {
	m := &Manager{
		factories: make(map[string]FactoryFunc),
		configs:   make(map[string]Config),
	}

	// Register built-in providers
	m.factories["github"] = func(cfg Config) Provider {
		return NewGitHubProvider(cfg)
	}
	m.factories["google"] = func(cfg Config) Provider {
		return NewGoogleProvider(cfg)
	}
	m.factories["discord"] = func(cfg Config) Provider {
		return NewDiscordProvider(cfg)
	}

	return m
}

// DefaultManager returns the global default Manager instance.
func DefaultManager() *Manager {
	return defaultManager
}

// Register registers a custom OAuth2 provider factory.
func (m *Manager) Register(name string, factory FactoryFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.factories[strings.ToLower(name)] = factory
}

// SetConfig sets or overrides credentials for a specific provider driver.
func (m *Manager) SetConfig(name string, cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[strings.ToLower(name)] = cfg
}

// Driver returns a configured Provider instance for the given driver name.
func (m *Manager) Driver(name string) Provider {
	lower := strings.ToLower(name)

	m.mu.RLock()
	factory, exists := m.factories[lower]
	cfg, hasCfg := m.configs[lower]
	m.mu.RUnlock()

	if !exists {
		panic(fmt.Sprintf("%v: %s", ErrProviderNotFound, name))
	}

	if !hasCfg {
		cfg = m.loadConfigFromEnv(lower)
	}

	return factory(cfg)
}

// Build creates an on-the-fly Provider with custom configuration.
func (m *Manager) Build(name string, cfg Config) Provider {
	lower := strings.ToLower(name)

	m.mu.RLock()
	factory, exists := m.factories[lower]
	m.mu.RUnlock()

	if !exists {
		panic(fmt.Sprintf("%v: %s", ErrProviderNotFound, name))
	}

	return factory(cfg)
}

func (m *Manager) loadConfigFromEnv(name string) Config {
	upper := strings.ToUpper(name)
	return Config{
		ClientID:     os.Getenv(upper + "_CLIENT_ID"),
		ClientSecret: os.Getenv(upper + "_CLIENT_SECRET"),
		RedirectURL:  os.Getenv(upper + "_REDIRECT_URL"),
	}
}

// Global Helper Functions (Socialite Facade Equivalent)

// Driver resolves a Provider by name (e.g. "github", "google", "discord").
func Driver(name string) Provider {
	return defaultManager.Driver(name)
}

// Build constructs a Provider with explicit configuration.
func Build(name string, cfg Config) Provider {
	return defaultManager.Build(name, cfg)
}

// Register registers a custom provider constructor globally.
func Register(name string, factory FactoryFunc) {
	defaultManager.Register(name, factory)
}

// SetConfig assigns provider configuration globally.
func SetConfig(name string, cfg Config) {
	defaultManager.SetConfig(name, cfg)
}
