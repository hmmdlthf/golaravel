package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Driver defines the contract for storage drivers.
type Driver interface {
	Put(path string, contents []byte) error
	Get(path string) ([]byte, error)
	Exists(path string) bool
	Delete(path string) error
	Size(path string) (int64, error)
	URL(path string) string
}

// LocalDriver implements storage on local filesystem disk.
type LocalDriver struct {
	basePath string
	baseURL  string
}

// NewLocalDriver creates a storage driver backed by a local directory.
func NewLocalDriver(basePath, baseURL string) (*LocalDriver, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("storage: failed to create root directory: %w", err)
	}
	return &LocalDriver{
		basePath: basePath,
		baseURL:  baseURL,
	}, nil
}

// Put writes contents to the storage path.
func (d *LocalDriver) Put(path string, contents []byte) error {
	fullPath := filepath.Join(d.basePath, filepath.Clean("/"+path))
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, contents, 0644)
}

// Get reads the file contents from storage.
func (d *LocalDriver) Get(path string) ([]byte, error) {
	fullPath := filepath.Join(d.basePath, filepath.Clean("/"+path))
	return os.ReadFile(fullPath)
}

// Exists checks if the file exists.
func (d *LocalDriver) Exists(path string) bool {
	fullPath := filepath.Join(d.basePath, filepath.Clean("/"+path))
	_, err := os.Stat(fullPath)
	return err == nil
}

// Delete removes the file from storage.
func (d *LocalDriver) Delete(path string) error {
	fullPath := filepath.Join(d.basePath, filepath.Clean("/"+path))
	return os.Remove(fullPath)
}

// Size returns the file size in bytes.
func (d *LocalDriver) Size(path string) (int64, error) {
	fullPath := filepath.Join(d.basePath, filepath.Clean("/"+path))
	info, err := os.Stat(fullPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// URL generates the public URL for the file.
func (d *LocalDriver) URL(path string) string {
	return d.baseURL + "/" + filepath.Clean("/"+path)
}

// DiskManager manages configured disks.
type DiskManager struct {
	disks map[string]Driver
}

// Global default disk manager
var globalDiskManager = NewDiskManager()

// NewDiskManager creates a disk manager.
func NewDiskManager() *DiskManager {
	dm := &DiskManager{
		disks: make(map[string]Driver),
	}
	defaultLocal, _ := NewLocalDriver("storage/app", "/storage")
	dm.disks["local"] = defaultLocal
	dm.disks["public"] = defaultLocal
	return dm
}

// Disk retrieves a configured storage disk by name.
func Disk(name ...string) Driver {
	diskName := "local"
	if len(name) > 0 {
		diskName = name[0]
	}
	return globalDiskManager.disks[diskName]
}

// Put saves file to default disk.
func Put(path string, contents []byte) error { return Disk().Put(path, contents) }
// Get reads file from default disk.
func Get(path string) ([]byte, error)        { return Disk().Get(path) }
// Exists checks file existence on default disk.
func Exists(path string) bool                { return Disk().Exists(path) }
// Delete removes file from default disk.
func Delete(path string) error               { return Disk().Delete(path) }

// ErrFileNotFound is returned when file cannot be located.
var ErrFileNotFound = errors.New("storage: file not found")

// PutReader writes a stream directly to storage.
func PutReader(path string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return Put(path, data)
}
