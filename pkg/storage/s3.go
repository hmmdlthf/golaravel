package storage

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// S3Config holds the configuration required for AWS S3 and S3-compatible backends
// such as MinIO, Cloudflare R2, Ceph, and LocalStack.
type S3Config struct {
	Bucket          string
	Region          string
	AccessKey       string
	SecretKey       string
	Endpoint        string // e.g. "https://s3.amazonaws.com" or "http://minio:9000"
	UsePathStyle    bool   // true for MinIO/LocalStack (http://host/bucket/key)
	PublicURLPrefix string // Optional CDN / public domain (e.g. "https://cdn.example.com")
	HTTPClient      *http.Client
	// Visibility is VisibilityPublic when objects can be read through URL
	// (a public bucket or CDN) and VisibilityPrivate when they need
	// TemporaryURL. Empty means public when PublicURLPrefix is set.
	Visibility string
}

// S3Driver implements Driver for S3-compatible object storage backends
// using pure Go standard library with AWS SigV4 query and header signing.
type S3Driver struct {
	cfg        S3Config
	client     *http.Client
	baseURL    *url.URL
	isPathStyle bool
}

// NewS3Driver creates a new S3Driver instance.
func NewS3Driver(cfg S3Config) (*S3Driver, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("storage: s3 bucket name cannot be empty")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{
			Timeout: 60 * time.Second,
		}
	}

	endpointStr := cfg.Endpoint
	if endpointStr == "" {
		endpointStr = fmt.Sprintf("https://s3.%s.amazonaws.com", cfg.Region)
	}

	if !strings.HasPrefix(endpointStr, "http://") && !strings.HasPrefix(endpointStr, "https://") {
		endpointStr = "https://" + endpointStr
	}

	parsedEndpoint, err := url.Parse(endpointStr)
	if err != nil {
		return nil, fmt.Errorf("storage: invalid s3 endpoint: %w", err)
	}

	// Determine path style vs virtual-host style
	isPathStyle := cfg.UsePathStyle
	host := parsedEndpoint.Hostname()
	if host == "localhost" || host == "127.0.0.1" || strings.Count(host, ".") == 3 {
		// IP addresses and localhost default to path style
		isPathStyle = true
	}

	return &S3Driver{
		cfg:         cfg,
		client:      cfg.HTTPClient,
		baseURL:     parsedEndpoint,
		isPathStyle: isPathStyle,
	}, nil
}

// buildURL constructs the full URL for the given key path.
func (d *S3Driver) buildURL(key string) (*url.URL, error) {
	cleanKey := strings.TrimLeft(filepath.ToSlash(key), "/")

	u := *d.baseURL
	if d.isPathStyle {
		basePath := strings.TrimRight(u.Path, "/")
		u.Path = fmt.Sprintf("%s/%s/%s", basePath, d.cfg.Bucket, cleanKey)
	} else {
		u.Host = fmt.Sprintf("%s.%s", d.cfg.Bucket, u.Host)
		basePath := strings.TrimRight(u.Path, "/")
		if basePath == "" {
			u.Path = "/" + cleanKey
		} else {
			u.Path = fmt.Sprintf("%s/%s", basePath, cleanKey)
		}
	}
	return &u, nil
}

// Put writes contents to the S3 bucket path.
func (d *S3Driver) Put(path string, contents []byte) error {
	targetURL, err := d.buildURL(path)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPut, targetURL.String(), bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("storage: failed to build put request: %w", err)
	}

	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		mimeType = http.DetectContentType(contents)
	}
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(contents)))

	SignS3Request(req, contents, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", time.Now())

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("storage: put failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage: s3 put failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// PutReader writes an io.Reader stream to the S3 bucket path.
func (d *S3Driver) PutReader(path string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("storage: failed to read stream: %w", err)
	}
	return d.Put(path, data)
}

// Get reads the file contents from S3.
func (d *S3Driver) Get(path string) ([]byte, error) {
	stream, err := d.GetStream(path)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	return io.ReadAll(stream)
}

// GetStream returns an io.ReadCloser for streaming the file directly from S3.
func (d *S3Driver) GetStream(path string) (io.ReadCloser, error) {
	targetURL, err := d.buildURL(path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodGet, targetURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to build get request: %w", err)
	}

	SignS3Request(req, nil, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", time.Now())

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: get failed: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrFileNotFound
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("storage: s3 get failed with status %d: %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

// Exists checks if the file exists in the S3 bucket via HEAD request.
func (d *S3Driver) Exists(path string) bool {
	targetURL, err := d.buildURL(path)
	if err != nil {
		return false
	}

	req, err := http.NewRequest(http.MethodHead, targetURL.String(), nil)
	if err != nil {
		return false
	}

	SignS3Request(req, nil, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", time.Now())

	resp, err := d.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// Delete removes the file from the S3 bucket.
func (d *S3Driver) Delete(path string) error {
	targetURL, err := d.buildURL(path)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodDelete, targetURL.String(), nil)
	if err != nil {
		return fmt.Errorf("storage: failed to build delete request: %w", err)
	}

	SignS3Request(req, nil, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", time.Now())

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("storage: delete failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage: s3 delete failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Copy duplicates a file within the S3 bucket.
func (d *S3Driver) Copy(src, dst string) error {
	dstURL, err := d.buildURL(dst)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPut, dstURL.String(), nil)
	if err != nil {
		return fmt.Errorf("storage: failed to build copy request: %w", err)
	}

	cleanSrc := strings.TrimLeft(filepath.ToSlash(src), "/")
	copySource := fmt.Sprintf("/%s%s", d.cfg.Bucket, canonicalURI(cleanSrc))
	req.Header.Set("x-amz-copy-source", copySource)

	SignS3Request(req, nil, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", time.Now())

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("storage: copy failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage: s3 copy failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Move moves/renames a file in S3 by copying and then deleting the source.
func (d *S3Driver) Move(src, dst string) error {
	if err := d.Copy(src, dst); err != nil {
		return err
	}
	return d.Delete(src)
}

// Size returns the file size in bytes via HEAD request.
func (d *S3Driver) Size(path string) (int64, error) {
	targetURL, err := d.buildURL(path)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest(http.MethodHead, targetURL.String(), nil)
	if err != nil {
		return 0, err
	}

	SignS3Request(req, nil, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", time.Now())

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, ErrFileNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("storage: s3 head failed with status %d", resp.StatusCode)
	}

	return resp.ContentLength, nil
}

// LastModified returns the last modification timestamp of the S3 object.
func (d *S3Driver) LastModified(path string) (time.Time, error) {
	targetURL, err := d.buildURL(path)
	if err != nil {
		return time.Time{}, err
	}

	req, err := http.NewRequest(http.MethodHead, targetURL.String(), nil)
	if err != nil {
		return time.Time{}, err
	}

	SignS3Request(req, nil, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", time.Now())

	resp, err := d.client.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return time.Time{}, ErrFileNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("storage: s3 head failed with status %d", resp.StatusCode)
	}

	lastModStr := resp.Header.Get("Last-Modified")
	if lastModStr == "" {
		return time.Time{}, fmt.Errorf("storage: Last-Modified header not returned")
	}

	return http.ParseTime(lastModStr)
}

// MimeType returns the MIME type of the file.
func (d *S3Driver) MimeType(path string) (string, error) {
	if ext := mime.TypeByExtension(filepath.Ext(path)); ext != "" {
		return ext, nil
	}

	targetURL, err := d.buildURL(path)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodHead, targetURL.String(), nil)
	if err != nil {
		return "", err
	}

	SignS3Request(req, nil, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", time.Now())

	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrFileNotFound
	}

	return resp.Header.Get("Content-Type"), nil
}

// URL generates the public URL for the file.
func (d *S3Driver) URL(path string) string {
	cleanPath := strings.TrimLeft(filepath.ToSlash(path), "/")
	if d.cfg.PublicURLPrefix != "" {
		return fmt.Sprintf("%s/%s", strings.TrimRight(d.cfg.PublicURLPrefix, "/"), cleanPath)
	}

	targetURL, err := d.buildURL(path)
	if err != nil {
		return ""
	}
	return targetURL.String()
}

// TemporaryURL generates a time-limited presigned GET URL for secure download.
func (d *S3Driver) TemporaryURL(path string, ttl time.Duration) (string, error) {
	targetURL, err := d.buildURL(path)
	if err != nil {
		return "", err
	}

	return PresignS3URL(http.MethodGet, targetURL, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", ttl, time.Now())
}

// PresignedPutURL generates a time-limited presigned PUT URL for direct client uploads.
func (d *S3Driver) PresignedPutURL(path string, ttl time.Duration) (string, error) {
	targetURL, err := d.buildURL(path)
	if err != nil {
		return "", err
	}

	return PresignS3URL(http.MethodPut, targetURL, d.cfg.AccessKey, d.cfg.SecretKey, d.cfg.Region, "s3", ttl, time.Now())
}
