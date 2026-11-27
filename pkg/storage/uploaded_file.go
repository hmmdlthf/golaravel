package storage

import (
	"crypto/rand"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// UploadedFile is a file sent in a multipart request, in the style of
// Laravel's Illuminate\Http\UploadedFile. Get one from a request with
// c.File / c.Files, or wrap a header with NewUploadedFile.
//
//	path, err := c.File("avatar").Store("avatars", "public")
//
// The client's name and type are reported as sent but never trusted:
// MimeType and Extension come from the file's bytes, and Store picks a
// random name.
type UploadedFile struct {
	header *multipart.FileHeader

	sniffOnce sync.Once
	mimeType  string
}

// NewUploadedFile wraps a multipart file header. It returns nil for nil.
func NewUploadedFile(fh *multipart.FileHeader) *UploadedFile {
	if fh == nil {
		return nil
	}
	return &UploadedFile{header: fh}
}

// Header returns the underlying multipart header.
func (f *UploadedFile) Header() *multipart.FileHeader { return f.header }

// Open returns the file's contents.
func (f *UploadedFile) Open() (multipart.File, error) {
	if f == nil || f.header == nil {
		return nil, ErrNoFile
	}
	return f.header.Open()
}

// Bytes reads the whole file.
func (f *UploadedFile) Bytes() ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Size returns the file size in bytes.
func (f *UploadedFile) Size() int64 {
	if f == nil || f.header == nil {
		return 0
	}
	return f.header.Size
}

// IsValid reports whether a file was received and can be read.
func (f *UploadedFile) IsValid() bool {
	if f == nil || f.header == nil {
		return false
	}
	r, err := f.header.Open()
	if err != nil {
		return false
	}
	r.Close()
	return true
}

// ClientOriginalName returns the file name the client sent, without any
// directory part. Show it to people; never use it as a storage path.
func (f *UploadedFile) ClientOriginalName() string {
	if f == nil || f.header == nil {
		return ""
	}
	name := filepath.Base(strings.ReplaceAll(f.header.Filename, `\`, "/"))
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// ClientOriginalExtension returns the extension of the client's file
// name, lower-cased and without the dot.
func (f *UploadedFile) ClientOriginalExtension() string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(f.ClientOriginalName()), "."))
}

// ClientMimeType returns the Content-Type the client sent for the file.
func (f *UploadedFile) ClientMimeType() string {
	if f == nil || f.header == nil {
		return ""
	}
	return f.header.Header.Get("Content-Type")
}

// MimeType returns the file's type, detected from its first bytes.
func (f *UploadedFile) MimeType() string {
	if f == nil || f.header == nil {
		return ""
	}
	f.sniffOnce.Do(func() { f.mimeType = SniffMimeType(f.header) })
	return f.mimeType
}

// SniffMimeType detects a multipart file's type from its first 512 bytes
// ("application/octet-stream" when it cannot be read).
func SniffMimeType(fh *multipart.FileHeader) string {
	r, err := fh.Open()
	if err != nil {
		return "application/octet-stream"
	}
	defer r.Close()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(r, buf)
	ct, _, _ := mime.ParseMediaType(http.DetectContentType(buf[:n]))
	return ct
}

// preferredExt is the extension used for types with several.
var preferredExt = map[string]string{
	"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp",
	"image/bmp": "bmp", "image/x-icon": "ico", "application/pdf": "pdf",
	"application/zip": "zip", "application/x-gzip": "gz", "application/x-rar-compressed": "rar",
	"application/vnd.ms-fontobject": "eot", "font/woff": "woff", "font/woff2": "woff2",
	"font/ttf": "ttf", "font/otf": "otf", "audio/mpeg": "mp3", "audio/wave": "wav",
	"audio/ogg": "ogg", "audio/aiff": "aiff", "audio/basic": "au", "audio/midi": "mid",
	"video/mp4": "mp4", "video/webm": "webm", "video/avi": "avi", "application/ogg": "ogg",
	"application/wasm": "wasm", "application/postscript": "ps",
}

var safeExt = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// Extension guesses the file's extension (without the dot) from its
// bytes. Where the bytes don't tell (plain text, office documents and
// other zip-based or unrecognised formats), the client's extension is
// used if it is a plain alphanumeric one; otherwise it returns "".
func (f *UploadedFile) Extension() string {
	ct := f.MimeType()
	client := f.ClientOriginalExtension()
	if !safeExt.MatchString(client) {
		client = ""
	}
	// zip containers (docx, xlsx, epub...) and text files are named by
	// their extension.
	if ct == "application/octet-stream" || ct == "" {
		return client
	}
	if (strings.HasPrefix(ct, "text/") || ct == "application/zip") && client != "" {
		return client
	}
	if ext, ok := preferredExt[ct]; ok {
		if client == "jpeg" && ext == "jpg" {
			return client
		}
		return ext
	}
	if exts, _ := mime.ExtensionsByType(ct); len(exts) > 0 {
		for _, e := range exts {
			if strings.TrimPrefix(e, ".") == client {
				return client
			}
		}
		return strings.TrimPrefix(exts[0], ".")
	}
	return client
}

// HashName returns a random 40 character name with the file's guessed
// extension, inside dir when one is given: "avatars/3fa0...c9.png".
func (f *UploadedFile) HashName(dir ...string) string {
	name := randomString(40)
	if ext := f.Extension(); ext != "" {
		name += "." + ext
	}
	if len(dir) > 0 && strings.Trim(dir[0], "/") != "" {
		return joinPath(dir[0], name)
	}
	return name
}

// ErrNoFile is returned when storing an UploadedFile that holds no file.
var ErrNoFile = errors.New("storage: no uploaded file")

// Store saves the file in dir under a random name (see HashName) on the
// given disk (the default disk when omitted) and returns its path.
func (f *UploadedFile) Store(dir string, disk ...string) (string, error) {
	if f == nil || f.header == nil {
		return "", ErrNoFile
	}
	return f.StoreAs(dir, f.HashName(), disk...)
}

// StoreAs saves the file as dir/name on the given disk (the default disk
// when omitted) and returns its path. name may hold sub-directories; ".."
// segments are removed.
func (f *UploadedFile) StoreAs(dir, name string, disk ...string) (string, error) {
	if f == nil || f.header == nil {
		return "", ErrNoFile
	}
	dst := joinPath(dir, name)
	if dst == "" {
		return "", errors.New("storage: empty file name")
	}
	d := Disk(disk...)
	if d == nil {
		return "", ErrDiskNotFound
	}
	r, err := f.header.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	if err := d.PutReader(dst, r); err != nil {
		return "", err
	}
	return dst, nil
}

// StorePublicly stores the file on the "public" disk (or the one given)
// under a random name.
func (f *UploadedFile) StorePublicly(dir string, disk ...string) (string, error) {
	return f.Store(dir, publicDisk(disk))
}

// StorePubliclyAs stores the file as dir/name on the "public" disk (or
// the one given).
func (f *UploadedFile) StorePubliclyAs(dir, name string, disk ...string) (string, error) {
	return f.StoreAs(dir, name, publicDisk(disk))
}

func publicDisk(disk []string) string {
	if len(disk) > 0 && disk[0] != "" {
		return disk[0]
	}
	return "public"
}

// joinPath joins storage path parts into a clean relative path with no
// ".." segments.
func joinPath(parts ...string) string {
	return strings.TrimPrefix(path.Clean("/"+path.Join(parts...)), "/")
}

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alnum[int(b[i])%len(alnum)]
	}
	return string(b)
}

// PutFile stores an uploaded file in dir under a random name on the given
// disk (the default disk when omitted), like Laravel's Storage::putFile.
func PutFile(dir string, f *UploadedFile, disk ...string) (string, error) {
	return f.Store(dir, disk...)
}

// PutFileAs stores an uploaded file as dir/name on the given disk (the
// default disk when omitted), like Laravel's Storage::putFileAs.
func PutFileAs(dir string, f *UploadedFile, name string, disk ...string) (string, error) {
	return f.StoreAs(dir, name, disk...)
}

// DeleteMany deletes several files from the given disk (the default disk
// when omitted). Missing files are ignored; the first other error is
// returned after trying them all.
func DeleteMany(paths []string, disk ...string) error {
	d := Disk(disk...)
	if d == nil {
		return ErrDiskNotFound
	}
	var first error
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := d.Delete(p); err != nil && !errors.Is(err, ErrFileNotFound) && first == nil {
			first = err
		}
	}
	return first
}
