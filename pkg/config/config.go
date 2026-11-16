package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store holds key-value configuration pairs loaded from environment and .env files.
type Store struct {
	mu     sync.RWMutex
	values map[string]string
}

// Global default configuration store
var globalStore = NewStore()

// NewStore initializes a new Config Store.
func NewStore() *Store {
	return &Store{
		values: make(map[string]string, 64),
	}
}

// LoadEnv reads a .env file and sets values into the store and os environment.
func LoadEnv(filenames ...string) error {
	return globalStore.LoadEnv(filenames...)
}

// LoadEnv reads one or more .env files.
func (s *Store) LoadEnv(filenames ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(filenames) == 0 {
		filenames = []string{".env"}
	}

	for _, filename := range filenames {
		file, err := os.Open(filename)
		if err != nil {
			if os.IsNotExist(err) {
				continue // Optional file
			}
			return err
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			line = strings.TrimPrefix(line, "export ")
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			val := parseEnvValue(strings.TrimSpace(parts[1]))

			s.values[key] = val
			if os.Getenv(key) == "" {
				_ = os.Setenv(key, val)
			}
		}
		err = scanner.Err()
		_ = file.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// parseEnvValue handles quoting and comments:
//
//	KEY=value # comment   -> value
//	KEY="a # b"           -> a # b
//	KEY="line\nbreak"     -> line, newline, break (double quotes only)
//	KEY='raw \n'          -> raw \n
func parseEnvValue(val string) string {
	if len(val) >= 2 {
		switch q := val[0]; q {
		case '"', '\'':
			if end := closingQuote(val, q); end > 0 {
				inner := val[1:end]
				if q == '"' {
					inner = strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(inner)
				}
				return inner
			}
		}
	}
	// Unquoted: " #" starts a comment.
	if i := strings.Index(val, " #"); i >= 0 {
		val = strings.TrimSpace(val[:i])
	}
	return val
}

// Set sets a configuration key-value pair.
func (s *Store) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
}

// Get retrieves a string config value, falling back to OS env or defaultValue.
func (s *Store) Get(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if val, ok := s.values[key]; ok {
		return val
	}
	return defaultValue
}

// GetInt retrieves an integer config value.
func (s *Store) GetInt(key string, defaultValue int) int {
	str := s.Get(key, "")
	if str == "" {
		return defaultValue
	}
	if val, err := strconv.Atoi(str); err == nil {
		return val
	}
	return defaultValue
}

// GetBool retrieves a boolean config value.
func (s *Store) GetBool(key string, defaultValue bool) bool {
	str := strings.ToLower(s.Get(key, ""))
	if str == "" {
		return defaultValue
	}
	return str == "1" || str == "true" || str == "yes" || str == "on"
}

// GetDuration retrieves a duration config value (e.g. "10s", "15m").
func (s *Store) GetDuration(key string, defaultValue time.Duration) time.Duration {
	str := s.Get(key, "")
	if str == "" {
		return defaultValue
	}
	if d, err := time.ParseDuration(str); err == nil {
		return d
	}
	return defaultValue
}

// Global helper accessors
func Get(key, defaultValue string) string                   { return globalStore.Get(key, defaultValue) }
func GetInt(key string, defaultValue int) int               { return globalStore.GetInt(key, defaultValue) }
func GetBool(key string, defaultValue bool) bool            { return globalStore.GetBool(key, defaultValue) }
func GetDuration(key string, d time.Duration) time.Duration { return globalStore.GetDuration(key, d) }
func Set(key, value string)                                 { globalStore.Set(key, value) }

// closingQuote returns the index of the quote closing val[0], skipping
// backslash-escaped quotes inside double quotes; -1 when unterminated.
func closingQuote(val string, q byte) int {
	for i := 1; i < len(val); i++ {
		if q == '"' && val[i] == '\\' {
			i++
			continue
		}
		if val[i] == q {
			return i
		}
	}
	return -1
}
