package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/context"
)

// Session represents an active HTTP session containing stateful data and flash messages.
type Session struct {
	mu         sync.RWMutex
	id         string
	attributes map[string]any
	newFlash   map[string]struct{}
	oldFlash   map[string]struct{}
	started    bool
}

// NewSession creates an empty session instance.
func NewSession(id ...string) *Session {
	sessID := generateSessionID()
	if len(id) > 0 && id[0] != "" {
		sessID = id[0]
	}

	return &Session{
		id:         sessID,
		attributes: make(map[string]any),
		newFlash:   make(map[string]struct{}),
		oldFlash:   make(map[string]struct{}),
		started:    true,
	}
}

// ID returns the unique session identifier.
func (s *Session) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// SetID changes the session identifier.
func (s *Session) SetID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = id
}

// Get retrieves an attribute by key, or returns the fallback if missing.
func (s *Session) Get(key string, defaultVal ...any) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if val, exists := s.attributes[key]; exists {
		return val
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return nil
}

// GetString retrieves a string attribute.
func (s *Session) GetString(key string, defaultVal ...string) string {
	val := s.Get(key)
	if val == nil {
		if len(defaultVal) > 0 {
			return defaultVal[0]
		}
		return ""
	}
	if str, ok := val.(string); ok {
		return str
	}
	return fmt.Sprint(val)
}

// GetInt retrieves an integer attribute.
func (s *Session) GetInt(key string, defaultVal ...int) int {
	val := s.Get(key)
	if val == nil {
		if len(defaultVal) > 0 {
			return defaultVal[0]
		}
		return 0
	}
	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		if len(defaultVal) > 0 {
			return defaultVal[0]
		}
		return 0
	}
}

// GetBool retrieves a boolean attribute.
func (s *Session) GetBool(key string, defaultVal ...bool) bool {
	val := s.Get(key)
	if val == nil {
		if len(defaultVal) > 0 {
			return defaultVal[0]
		}
		return false
	}
	if b, ok := val.(bool); ok {
		return b
	}
	return false
}

// Has checks if an attribute exists and is not nil.
func (s *Session) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, exists := s.attributes[key]
	return exists && val != nil
}

// Exists checks if a key is present in the session (even if nil).
func (s *Session) Exists(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.attributes[key]
	return exists
}

// Put stores a key-value pair in the session.
func (s *Session) Put(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes[key] = value
}

// Push appends a value onto an existing slice in the session.
func (s *Session) Push(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.attributes[key]
	if !exists {
		s.attributes[key] = []any{value}
		return
	}
	if slice, ok := existing.([]any); ok {
		s.attributes[key] = append(slice, value)
	} else {
		s.attributes[key] = []any{existing, value}
	}
}

// Pull retrieves and removes an attribute from the session.
func (s *Session) Pull(key string, defaultVal ...any) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	val, exists := s.attributes[key]
	if exists {
		delete(s.attributes, key)
		return val
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return nil
}

// Forget removes one or more keys from the session.
func (s *Session) Forget(keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		delete(s.attributes, k)
		delete(s.newFlash, k)
		delete(s.oldFlash, k)
	}
}

// Flush removes all attributes from the session.
func (s *Session) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes = make(map[string]any)
	s.newFlash = make(map[string]struct{})
	s.oldFlash = make(map[string]struct{})
}

// All returns a shallow copy of all session attributes.
func (s *Session) All() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]any, len(s.attributes))
	for k, v := range s.attributes {
		out[k] = v
	}
	return out
}

// --- Flash Messages ---

// Flash stores a key-value pair that will be available during the subsequent HTTP request,
// then automatically deleted. Ideal for redirect alerts like session("success").
func (s *Session) Flash(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes[key] = value
	s.newFlash[key] = struct{}{}
	delete(s.oldFlash, key)
}

// Now stores a key-value pair that will only be available for the immediate current request.
func (s *Session) Now(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes[key] = value
	s.oldFlash[key] = struct{}{}
}

// Keep preserves specific flashed keys for an additional request lifecycle.
func (s *Session) Keep(keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		if _, exists := s.oldFlash[k]; exists {
			delete(s.oldFlash, k)
			s.newFlash[k] = struct{}{}
		}
	}
}

// Reflash preserves all current flash messages for another request lifecycle.
func (s *Session) Reflash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.oldFlash {
		s.newFlash[k] = struct{}{}
	}
	s.oldFlash = make(map[string]struct{})
}

// AgeFlash cleans up expired flash messages at the end of the request.
// It deletes previous old flash keys and shifts newly added flash keys into old.
func (s *Session) AgeFlash() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Delete old flash keys from attributes
	for k := range s.oldFlash {
		delete(s.attributes, k)
	}

	// Move new flash to old
	s.oldFlash = s.newFlash
	s.newFlash = make(map[string]struct{})
}

// --- CSRF Token & Security ---

// Token returns the session's CSRF token, generating one if not yet set.
func (s *Session) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok, ok := s.attributes["_token"].(string); ok && tok != "" {
		return tok
	}
	tok := generateRandomString(40)
	s.attributes["_token"] = tok
	return tok
}

// RegenerateToken generates a new CSRF token.
func (s *Session) RegenerateToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok := generateRandomString(40)
	s.attributes["_token"] = tok
	return tok
}

// Regenerate generates a new session ID to protect against session fixation attacks.
func (s *Session) Regenerate(destroyOld ...bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = generateSessionID()
	if len(destroyOld) > 0 && destroyOld[0] {
		s.attributes = make(map[string]any)
		s.newFlash = make(map[string]struct{})
		s.oldFlash = make(map[string]struct{})
	}
	return nil
}

// IsStarted reports whether the session has been initialized.
func (s *Session) IsStarted() bool {
	return s.started
}

// --- JSON Serialization ---

type serializedSession struct {
	ID         string              `json:"id"`
	Attributes map[string]any      `json:"attributes"`
	NewFlash   map[string]struct{} `json:"new_flash"`
	OldFlash   map[string]struct{} `json:"old_flash"`
}

func (s *Session) MarshalJSON() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(serializedSession{
		ID:         s.id,
		Attributes: s.attributes,
		NewFlash:   s.newFlash,
		OldFlash:   s.oldFlash,
	})
}

func (s *Session) UnmarshalJSON(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ser serializedSession
	if err := json.Unmarshal(data, &ser); err != nil {
		return err
	}
	s.id = ser.ID
	s.attributes = ser.Attributes
	if s.attributes == nil {
		s.attributes = make(map[string]any)
	}
	s.newFlash = ser.NewFlash
	if s.newFlash == nil {
		s.newFlash = make(map[string]struct{})
	}
	s.oldFlash = ser.OldFlash
	if s.oldFlash == nil {
		s.oldFlash = make(map[string]struct{})
	}
	s.started = true
	return nil
}

func generateSessionID() string {
	return generateRandomString(40)
}

func generateRandomString(length int) string {
	bytes := make([]byte, (length+1)/2)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)[:length]
}

// Get retrieves an attribute from the active session in context.
func Get(c *context.Context, key string, defaultVal ...any) any {
	s := FromContext(c)
	if s == nil {
		if len(defaultVal) > 0 {
			return defaultVal[0]
		}
		return nil
	}
	return s.Get(key, defaultVal...)
}

// GetString retrieves a string attribute from the active session in context.
func GetString(c *context.Context, key string, defaultVal ...string) string {
	s := FromContext(c)
	if s == nil {
		if len(defaultVal) > 0 {
			return defaultVal[0]
		}
		return ""
	}
	return s.GetString(key, defaultVal...)
}

// Put stores an attribute in the active session in context.
func Put(c *context.Context, key string, value any) {
	s := FromContext(c)
	if s != nil {
		s.Put(key, value)
	}
}

// Flash stores a flash message in the active session in context.
func Flash(c *context.Context, key string, value any) {
	s := FromContext(c)
	if s != nil {
		s.Flash(key, value)
	}
}

// Has checks if an attribute exists in the active session in context.
func Has(c *context.Context, key string) bool {
	s := FromContext(c)
	if s == nil {
		return false
	}
	return s.Has(key)
}

// Token retrieves the CSRF token from the active session in context.
func Token(c *context.Context) string {
	s := FromContext(c)
	if s == nil {
		return ""
	}
	return s.Token()
}
