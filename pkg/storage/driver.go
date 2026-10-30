package storage

import (
	"errors"
	"io"
	"mime/multipart"
	"time"
)

// Common storage errors
var (
	ErrFileNotFound     = errors.New("storage: file not found")
	ErrDiskNotFound     = errors.New("storage: disk not found")
	ErrInvalidSignature = errors.New("storage: invalid URL signature")
	ErrExpiredURL       = errors.New("storage: signed URL has expired")
)

// Driver defines the standard contract for filesystem storage drivers
// adhering to the Laravel Flysystem parity specifications.
type Driver interface {
	// Put writes raw bytes to the specified path.
	Put(path string, contents []byte) error

	// PutReader writes an io.Reader stream directly to the specified path.
	PutReader(path string, r io.Reader) error

	// Get reads file contents from storage as a byte slice.
	Get(path string) ([]byte, error)

	// GetStream returns an io.ReadCloser for streaming file contents directly.
	GetStream(path string) (io.ReadCloser, error)

	// Exists checks if the file exists in storage.
	Exists(path string) bool

	// Delete removes the file at the specified path.
	Delete(path string) error

	// Copy duplicates a file from src to dst.
	Copy(src, dst string) error

	// Move moves/renames a file from src to dst.
	Move(src, dst string) error

	// Size returns the file size in bytes.
	Size(path string) (int64, error)

	// LastModified returns the last modification timestamp of the file.
	LastModified(path string) (time.Time, error)

	// MimeType detects and returns the MIME type of the file.
	MimeType(path string) (string, error)

	// URL generates the public URL for the file.
	URL(path string) string

	// TemporaryURL generates a time-limited presigned GET URL for secure downloads.
	TemporaryURL(path string, ttl time.Duration) (string, error)

	// PresignedPutURL generates a time-limited presigned PUT URL for direct client uploads.
	PresignedPutURL(path string, ttl time.Duration) (string, error)
}

// UploadedFileHandler handles saving multipart form uploads.
type UploadedFileHandler interface {
	SaveUploadedFile(file *multipart.FileHeader, dstPath string) error
}
