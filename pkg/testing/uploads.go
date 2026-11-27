package gltest

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golaravel/golaravel/pkg/storage"
)

// FakeDisk is an in-memory disk installed by FakeStorage, with
// assertions in the style of Laravel's Storage::fake().
type FakeDisk struct {
	*storage.MemoryDriver
	t    testing.TB
	name string
}

// FakeStorage replaces the named disk (the default disk when omitted)
// with an empty in-memory one for the rest of the test, like Laravel's
// Storage::fake('public'). The real disk is put back when the test ends.
// The fake keeps the real disk's visibility.
func FakeStorage(t testing.TB, name ...string) *FakeDisk {
	t.Helper()
	disk := storage.DefaultDiskName()
	if len(name) > 0 && name[0] != "" {
		disk = name[0]
	}
	mem := storage.NewMemoryDriver("/storage/" + disk).SetVisibility(storage.Visibility(disk))
	t.Cleanup(storage.Swap(disk, mem))
	return &FakeDisk{MemoryDriver: mem, t: t, name: disk}
}

// AssertExists fails the test unless every path is stored.
func (d *FakeDisk) AssertExists(paths ...string) *FakeDisk {
	d.t.Helper()
	for _, p := range paths {
		if !d.Exists(p) {
			d.t.Errorf("expected %q on disk %q; it holds %v", p, d.name, d.Files(""))
		}
	}
	return d
}

// AssertMissing fails the test if any path is stored.
func (d *FakeDisk) AssertMissing(paths ...string) *FakeDisk {
	d.t.Helper()
	for _, p := range paths {
		if d.Exists(p) {
			d.t.Errorf("expected %q to be missing from disk %q", p, d.name)
		}
	}
	return d
}

// AssertCount fails the test unless dir (and its sub-directories) holds
// exactly n files.
func (d *FakeDisk) AssertCount(dir string, n int) *FakeDisk {
	d.t.Helper()
	if got := d.Files(dir); len(got) != n {
		d.t.Errorf("expected %d files in %q on disk %q, found %d: %v", n, dir, d.name, len(got), got)
	}
	return d
}

// AssertDirectoryEmpty fails the test if dir holds any file.
func (d *FakeDisk) AssertDirectoryEmpty(dir string) *FakeDisk {
	d.t.Helper()
	return d.AssertCount(dir, 0)
}

// FakeFile is a file to upload in a test, like Laravel's
// UploadedFile::fake(). Send it with WithUpload, or turn it into a
// *storage.UploadedFile with UploadedFile.
type FakeFile struct {
	Name        string
	Content     []byte
	ContentType string
}

// FakeImage returns a width × height image named name, encoded as PNG,
// JPEG or GIF by its extension (PNG for others), so image, mimes and
// dimensions rules pass.
func FakeImage(name string, width, height int) FakeFile {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / max(width, 1)), G: 120, B: uint8(y * 255 / max(height, 1)), A: 255})
		}
	}
	var buf bytes.Buffer
	ct := "image/png"
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		_ = jpeg.Encode(&buf, img, nil)
		ct = "image/jpeg"
	case ".gif":
		_ = gif.Encode(&buf, img, nil)
		ct = "image/gif"
	default:
		_ = png.Encode(&buf, img)
	}
	return FakeFile{Name: name, Content: buf.Bytes(), ContentType: ct}
}

// magic starts fake files so their content matches their type.
var magic = map[string]string{
	".pdf": "%PDF-1.4\n", ".zip": "PK\x03\x04", ".docx": "PK\x03\x04", ".xlsx": "PK\x03\x04",
	".gz": "\x1f\x8b\x08", ".mp3": "ID3", ".txt": "", ".csv": "",
}

// FakeUpload returns a file named name of about sizeKB kilobytes. Its
// content starts with the signature of its type for common formats (PDF,
// zip, office files, gzip, mp3), so content checks pass; text formats are
// filled with text. mimeType sets the Content-Type the client claims
// (guessed from the name when omitted).
func FakeUpload(name string, sizeKB int, mimeType ...string) FakeFile {
	ext := strings.ToLower(filepath.Ext(name))
	size := sizeKB * 1024
	var buf bytes.Buffer
	buf.WriteString(magic[ext])
	fill := byte(0)
	if ext == ".txt" || ext == ".csv" || ext == ".md" || ext == ".json" {
		fill = 'a'
	}
	for buf.Len() < size {
		buf.WriteByte(fill)
	}
	b := buf.Bytes()
	if len(b) > size && size > 0 {
		b = b[:size]
	}
	ct := ""
	if len(mimeType) > 0 {
		ct = mimeType[0]
	}
	if ct == "" {
		ct = http.DetectContentType(b)
	}
	return FakeFile{Name: name, Content: b, ContentType: ct}
}

// UploadedFile returns the file as a *storage.UploadedFile, as a handler
// would get it from c.File, for unit tests that don't send a request.
func (f FakeFile) UploadedFile() *storage.UploadedFile {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreatePart(f.partHeader("file"))
	if err != nil {
		panic(err)
	}
	_, _ = part.Write(f.Content)
	_ = w.Close()
	form, err := multipart.NewReader(&body, w.Boundary()).ReadForm(int64(len(f.Content)) + 1<<20)
	if err != nil {
		panic(fmt.Sprintf("gltest: building fake upload: %v", err))
	}
	return storage.NewUploadedFile(form.File["file"][0])
}

func (f FakeFile) partHeader(field string) textproto.MIMEHeader {
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(field), escapeQuotes(f.Name)))
	ct := f.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	h.Set("Content-Type", ct)
	return h
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string { return quoteEscaper.Replace(s) }

// WithUpload attaches fake files to the request under field, turning it
// into a multipart request. Several files with one field name arrive as
// c.Files(field).
func (b *TestRequestBuilder) WithUpload(field string, files ...FakeFile) *TestRequestBuilder {
	for _, f := range files {
		b.files = append(b.files, testFileUpload{fieldName: field, filename: f.Name, content: f.Content, contentType: f.ContentType})
	}
	return b
}
