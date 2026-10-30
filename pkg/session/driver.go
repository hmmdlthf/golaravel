package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrSessionNotFound = errors.New("session: session not found")
)

// Driver defines the contract for session storage engines.
type Driver interface {
	Read(id string) ([]byte, error)
	Write(id string, data []byte) error
	Destroy(id string) error
	GC(maxLifetime time.Duration) error
}

// --- Memory Driver ---

type memorySessionItem struct {
	data      []byte
	updatedAt time.Time
}

// MemoryDriver stores sessions in server memory with automatic TTL cleanup.
type MemoryDriver struct {
	mu       sync.RWMutex
	sessions map[string]memorySessionItem
}

// NewMemoryDriver creates an in-memory session driver.
func NewMemoryDriver() *MemoryDriver {
	return &MemoryDriver{
		sessions: make(map[string]memorySessionItem),
	}
}

func (d *MemoryDriver) Read(id string) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	item, exists := d.sessions[id]
	if !exists {
		return nil, ErrSessionNotFound
	}
	return item.data, nil
}

func (d *MemoryDriver) Write(id string, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sessions[id] = memorySessionItem{
		data:      data,
		updatedAt: time.Now(),
	}
	return nil
}

func (d *MemoryDriver) Destroy(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sessions, id)
	return nil
}

func (d *MemoryDriver) GC(maxLifetime time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cutoff := time.Now().Add(-maxLifetime)
	for id, item := range d.sessions {
		if item.updatedAt.Before(cutoff) {
			delete(d.sessions, id)
		}
	}
	return nil
}

// --- File Driver ---

// FileDriver stores serialized sessions as files on disk.
type FileDriver struct {
	dir string
}

// NewFileDriver creates a file-backed session driver in the specified directory.
func NewFileDriver(dir string) (*FileDriver, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("session/file: failed to create session dir: %w", err)
	}
	return &FileDriver{dir: dir}, nil
}

func (d *FileDriver) path(id string) string {
	return filepath.Join(d.dir, filepath.Clean(id))
}

func (d *FileDriver) Read(id string) ([]byte, error) {
	data, err := os.ReadFile(d.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	return data, nil
}

func (d *FileDriver) Write(id string, data []byte) error {
	return os.WriteFile(d.path(id), data, 0600)
}

func (d *FileDriver) Destroy(id string) error {
	err := os.Remove(d.path(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d *FileDriver) GC(maxLifetime time.Duration) error {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-maxLifetime)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(d.dir, entry.Name()))
		}
	}
	return nil
}

// --- Cookie Driver ---

// CookieDriver is a marker driver indicating sessions are stored in an encrypted client cookie.
type CookieDriver struct{}

// NewCookieDriver creates a cookie-backed session driver.
func NewCookieDriver() *CookieDriver {
	return &CookieDriver{}
}

func (d *CookieDriver) Read(id string) ([]byte, error) {
	// Payload is passed directly in the cookie by the SessionManager
	return nil, nil
}

func (d *CookieDriver) Write(id string, data []byte) error {
	return nil
}

func (d *CookieDriver) Destroy(id string) error {
	return nil
}

func (d *CookieDriver) GC(maxLifetime time.Duration) error {
	return nil
}
