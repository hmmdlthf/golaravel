package storage

import (
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"strconv"
	"sync"
	"time"
)

// DiskManager manages configured storage disks.
type DiskManager struct {
	mu          sync.RWMutex
	disks       map[string]Driver
	defaultDisk string
}

// The global disk manager is built on first use, so importing the package
// reads no environment and creates no directories.
var (
	globalDiskOnce    sync.Once
	globalDiskManager *DiskManager
)

func defaultDiskManager() *DiskManager {
	globalDiskOnce.Do(func() { globalDiskManager = NewDiskManager() })
	return globalDiskManager
}

// NewDiskManager creates and initializes a disk manager.
func NewDiskManager() *DiskManager {
	dm := &DiskManager{
		disks:       make(map[string]Driver),
		defaultDisk: "local",
	}

	// Register default local disk
	localPath := os.Getenv("STORAGE_PATH")
	if localPath == "" {
		localPath = "storage/app"
	}
	localURL := os.Getenv("STORAGE_URL")
	if localURL == "" {
		localURL = "/storage"
	}
	secret := os.Getenv("APP_KEY")
	if secret == "" {
		secret = os.Getenv("STORAGE_SECRET")
	}

	defaultLocal, _ := NewLocalDriver(localPath, localURL, secret)
	dm.disks["local"] = defaultLocal

	// Register public disk
	publicLocal, _ := NewLocalDriver(localPath+"/public", localURL+"/public", secret)
	dm.disks["public"] = publicLocal

	// Configure S3 / MinIO if credentials are provided in env
	dm.configureS3FromEnv()

	// Configure default disk name from env
	if def := os.Getenv("FILESYSTEM_DISK"); def != "" {
		dm.defaultDisk = def
	}

	return dm
}

// configureS3FromEnv sets up the "s3" or "minio" disk from environment variables.
func (dm *DiskManager) configureS3FromEnv() {
	bucket := os.Getenv("AWS_BUCKET")
	if bucket == "" {
		return
	}

	region := os.Getenv("AWS_DEFAULT_REGION")
	if region == "" {
		region = "us-east-1"
	}

	usePathStyle, _ := strconv.ParseBool(os.Getenv("AWS_USE_PATH_STYLE"))

	s3Driver, err := NewS3Driver(S3Config{
		Bucket:          bucket,
		Region:          region,
		AccessKey:       os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretKey:       os.Getenv("AWS_SECRET_ACCESS_KEY"),
		Endpoint:        os.Getenv("AWS_ENDPOINT"),
		UsePathStyle:    usePathStyle,
		PublicURLPrefix: os.Getenv("AWS_URL"),
	})
	if err == nil {
		dm.disks["s3"] = s3Driver
		if os.Getenv("AWS_ENDPOINT") != "" {
			dm.disks["minio"] = s3Driver
		}
	}
}

// Disk retrieves a configured storage disk by name.
// If name is omitted, returns the default configured disk.
func Disk(name ...string) Driver {
	return defaultDiskManager().Disk(name...)
}

// Disk retrieves a disk from the manager.
func (dm *DiskManager) Disk(name ...string) Driver {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	diskName := dm.defaultDisk
	if len(name) > 0 && name[0] != "" {
		diskName = name[0]
	}

	d, ok := dm.disks[diskName]
	if !ok {
		// Fallback to local if unknown disk requested
		if local, exists := dm.disks["local"]; exists {
			return local
		}
		return nil
	}
	return d
}

// RegisterDisk registers a custom storage disk driver.
func RegisterDisk(name string, d Driver) {
	defaultDiskManager().RegisterDisk(name, d)
}

// RegisterDisk registers a named disk.
func (dm *DiskManager) RegisterDisk(name string, d Driver) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.disks[name] = d
}

// SetDefaultDisk changes the default disk name.
func SetDefaultDisk(name string) {
	defaultDiskManager().SetDefaultDisk(name)
}

// SetDefaultDisk changes the default disk name.
func (dm *DiskManager) SetDefaultDisk(name string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.defaultDisk = name
}

// Top-level facade methods operating on default disk

// Put writes contents to the default disk.
func Put(path string, contents []byte) error {
	d := Disk()
	if d == nil {
		return ErrDiskNotFound
	}
	return d.Put(path, contents)
}

// PutReader writes an io.Reader stream to the default disk.
func PutReader(path string, r io.Reader) error {
	d := Disk()
	if d == nil {
		return ErrDiskNotFound
	}
	return d.PutReader(path, r)
}

// Get reads the file contents from the default disk.
func Get(path string) ([]byte, error) {
	d := Disk()
	if d == nil {
		return nil, ErrDiskNotFound
	}
	return d.Get(path)
}

// GetStream returns an io.ReadCloser for streaming file contents directly.
func GetStream(path string) (io.ReadCloser, error) {
	d := Disk()
	if d == nil {
		return nil, ErrDiskNotFound
	}
	return d.GetStream(path)
}

// Exists checks if the file exists on the default disk.
func Exists(path string) bool {
	d := Disk()
	if d == nil {
		return false
	}
	return d.Exists(path)
}

// Delete removes the file from the default disk.
func Delete(path string) error {
	d := Disk()
	if d == nil {
		return ErrDiskNotFound
	}
	return d.Delete(path)
}

// Copy duplicates a file from src to dst on the default disk.
func Copy(src, dst string) error {
	d := Disk()
	if d == nil {
		return ErrDiskNotFound
	}
	return d.Copy(src, dst)
}

// Move moves/renames a file from src to dst on the default disk.
func Move(src, dst string) error {
	d := Disk()
	if d == nil {
		return ErrDiskNotFound
	}
	return d.Move(src, dst)
}

// Size returns the file size in bytes from the default disk.
func Size(path string) (int64, error) {
	d := Disk()
	if d == nil {
		return 0, ErrDiskNotFound
	}
	return d.Size(path)
}

// LastModified returns the file last modified timestamp from the default disk.
func LastModified(path string) (time.Time, error) {
	d := Disk()
	if d == nil {
		return time.Time{}, ErrDiskNotFound
	}
	return d.LastModified(path)
}

// MimeType detects the MIME type of the file on the default disk.
func MimeType(path string) (string, error) {
	d := Disk()
	if d == nil {
		return "", ErrDiskNotFound
	}
	return d.MimeType(path)
}

// URL generates the public URL for the file on the default disk.
func URL(path string) string {
	d := Disk()
	if d == nil {
		return ""
	}
	return d.URL(path)
}

// TemporaryURL generates a time-limited presigned GET URL for secure downloads.
func TemporaryURL(path string, ttl time.Duration) (string, error) {
	d := Disk()
	if d == nil {
		return "", ErrDiskNotFound
	}
	return d.TemporaryURL(path, ttl)
}

// PresignedPutURL generates a time-limited presigned PUT URL for direct client uploads.
func PresignedPutURL(path string, ttl time.Duration) (string, error) {
	d := Disk()
	if d == nil {
		return "", ErrDiskNotFound
	}
	return d.PresignedPutURL(path, ttl)
}

// SaveUploadedFile saves a multipart uploaded file to the specified disk.
// If disk is omitted, the default disk is used.
func SaveUploadedFile(file *multipart.FileHeader, dstPath string, disk ...string) error {
	targetDisk := Disk(disk...)
	if targetDisk == nil {
		return ErrDiskNotFound
	}

	src, err := file.Open()
	if err != nil {
		return fmt.Errorf("storage: failed to open uploaded file: %w", err)
	}
	defer src.Close()

	return targetDisk.PutReader(dstPath, src)
}
