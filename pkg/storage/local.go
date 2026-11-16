package storage

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LocalDriver implements the Driver interface for the local server filesystem.
type LocalDriver struct {
	basePath string
	baseURL  string
	secret   []byte

	// randomSecret is set when no secret was configured; the warning is
	// logged once, the first time a URL is actually signed.
	randomSecret bool
	warnOnce     sync.Once
}

// insecureDefaultSecret is the fallback earlier versions used; it is public.
const insecureDefaultSecret = "golaravel-storage-default-secret-key-32bytes!!"

// NewLocalDriver creates a storage driver backed by a local directory.
// secret signs temporary URLs; set it from your application key.
func NewLocalDriver(basePath, baseURL string, secret ...string) (*LocalDriver, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("storage: failed to create root directory: %w", err)
	}

	sec, random := "", false
	if len(secret) > 0 {
		sec = secret[0]
	}
	if sec == "" || sec == insecureDefaultSecret {
		// A fixed fallback would let anyone forge signed download and
		// upload URLs. A random key keeps them safe; they stop working on
		// restart until APP_KEY (or STORAGE_SECRET) is set.
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		sec = hex.EncodeToString(b)
		random = true
	}

	return &LocalDriver{
		basePath: basePath,
		baseURL:  strings.TrimRight(baseURL, "/"),
		secret:   []byte(sec),

		randomSecret: random,
	}, nil
}

// fullPath resolves and cleans the relative storage path against the basePath.
func (d *LocalDriver) fullPath(path string) string {
	cleanRel := filepath.Clean("/" + filepath.ToSlash(path))
	return filepath.Join(d.basePath, cleanRel)
}

// Put writes contents to the storage path.
func (d *LocalDriver) Put(path string, contents []byte) error {
	fullPath := d.fullPath(path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, contents, 0644)
}

// PutReader writes an io.Reader stream directly to the storage path.
func (d *LocalDriver) PutReader(path string, r io.Reader) error {
	fullPath := d.fullPath(path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, r)
	return err
}

// Get reads the file contents from storage.
func (d *LocalDriver) Get(path string) ([]byte, error) {
	fullPath := d.fullPath(path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	return data, nil
}

// GetStream returns an io.ReadCloser for streaming file contents directly.
func (d *LocalDriver) GetStream(path string) (io.ReadCloser, error) {
	fullPath := d.fullPath(path)
	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	return file, nil
}

// Exists checks if the file exists.
func (d *LocalDriver) Exists(path string) bool {
	fullPath := d.fullPath(path)
	_, err := os.Stat(fullPath)
	return err == nil
}

// Delete removes the file from storage.
func (d *LocalDriver) Delete(path string) error {
	fullPath := d.fullPath(path)
	err := os.Remove(fullPath)
	if err != nil && os.IsNotExist(err) {
		return nil // Deleting non-existent file is idempotent
	}
	return err
}

// Copy duplicates a file from src to dst.
func (d *LocalDriver) Copy(src, dst string) error {
	srcPath := d.fullPath(src)
	dstPath := d.fullPath(dst)

	srcFile, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrFileNotFound
		}
		return err
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// Move moves/renames a file from src to dst.
func (d *LocalDriver) Move(src, dst string) error {
	srcPath := d.fullPath(src)
	dstPath := d.fullPath(dst)

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}

	err := os.Rename(srcPath, dstPath)
	if err == nil {
		return nil
	}

	// Fallback to copy and delete if cross-device link
	if err := d.Copy(src, dst); err != nil {
		return err
	}
	return d.Delete(src)
}

// Size returns the file size in bytes.
func (d *LocalDriver) Size(path string) (int64, error) {
	fullPath := d.fullPath(path)
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ErrFileNotFound
		}
		return 0, err
	}
	return info.Size(), nil
}

// LastModified returns the file's last modified timestamp.
func (d *LocalDriver) LastModified(path string) (time.Time, error) {
	fullPath := d.fullPath(path)
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, ErrFileNotFound
		}
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// MimeType detects the MIME type from extension or file content header.
func (d *LocalDriver) MimeType(path string) (string, error) {
	if ext := mime.TypeByExtension(filepath.Ext(path)); ext != "" {
		return ext, nil
	}

	fullPath := d.fullPath(path)
	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrFileNotFound
		}
		return "", err
	}
	defer file.Close()

	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}

	return http.DetectContentType(buf[:n]), nil
}

// URL generates the public URL for the file.
func (d *LocalDriver) URL(path string) string {
	cleanRel := strings.TrimLeft(filepath.ToSlash(path), "/")
	return fmt.Sprintf("%s/%s", d.baseURL, cleanRel)
}

// sign generates an HMAC-SHA256 signature for path, expires, and action.
func (d *LocalDriver) sign(path string, expires int64, action string) string {
	if d.randomSecret {
		d.warnOnce.Do(func() {
			slog.Warn("storage: no signing secret configured (APP_KEY / STORAGE_SECRET); using a random key, so signed URLs end on restart")
		})
	}
	mac := hmac.New(sha256.New, d.secret)
	data := fmt.Sprintf("%s|%d|%s", path, expires, action)
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

// TemporaryURL generates a time-limited HMAC-signed GET URL for secure downloads.
func (d *LocalDriver) TemporaryURL(path string, ttl time.Duration) (string, error) {
	cleanRel := strings.TrimLeft(filepath.ToSlash(path), "/")
	expires := time.Now().Add(ttl).Unix()
	signature := d.sign(cleanRel, expires, "get")

	return fmt.Sprintf("%s/%s?expires=%d&signature=%s", d.baseURL, cleanRel, expires, signature), nil
}

// PresignedPutURL generates a time-limited HMAC-signed upload URL for direct uploads.
func (d *LocalDriver) PresignedPutURL(path string, ttl time.Duration) (string, error) {
	cleanRel := strings.TrimLeft(filepath.ToSlash(path), "/")
	expires := time.Now().Add(ttl).Unix()
	signature := d.sign(cleanRel, expires, "put")

	return fmt.Sprintf("%s/%s?action=upload&expires=%d&signature=%s", d.baseURL, cleanRel, expires, signature), nil
}

// VerifySignedURL verifies that a signed temporary URL is valid and unexpired.
func (d *LocalDriver) VerifySignedURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}

	q := u.Query()
	expiresStr := q.Get("expires")
	sig := q.Get("signature")
	if expiresStr == "" || sig == "" {
		return ErrInvalidSignature
	}

	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return ErrInvalidSignature
	}

	if time.Now().Unix() > expires {
		return ErrExpiredURL
	}

	action := "get"
	if q.Get("action") == "upload" {
		action = "put"
	}

	// Reconstruct the clean relative path from URL path
	urlPath := strings.TrimPrefix(u.Path, strings.TrimPrefix(d.baseURL, "http://"))
	urlPath = strings.TrimPrefix(urlPath, strings.TrimPrefix(d.baseURL, "https://"))
	urlPath = strings.TrimLeft(filepath.ToSlash(urlPath), "/")

	// Strip base URL path prefix if baseURL has path component
	if baseParsed, err := url.Parse(d.baseURL); err == nil && baseParsed.Path != "" {
		basePrefix := strings.Trim(baseParsed.Path, "/")
		urlPath = strings.TrimPrefix(urlPath, basePrefix)
		urlPath = strings.TrimLeft(urlPath, "/")
	}

	expectedSig := d.sign(urlPath, expires, action)
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return ErrInvalidSignature
	}

	return nil
}
