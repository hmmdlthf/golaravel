package storage_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/storage"
	gltest "github.com/golaravel/golaravel/pkg/testing"
)

func TestUploadedFileDetails(t *testing.T) {
	f := gltest.FakeImage("../../Holiday Photo.JPEG", 4, 3).UploadedFile()
	if got := f.ClientOriginalName(); got != "Holiday Photo.JPEG" {
		t.Fatalf("name: %q", got)
	}
	if got := f.ClientOriginalExtension(); got != "jpeg" {
		t.Fatalf("client ext: %q", got)
	}
	if f.MimeType() != "image/jpeg" || f.ClientMimeType() != "image/jpeg" {
		t.Fatalf("mime: %q / %q", f.MimeType(), f.ClientMimeType())
	}
	if f.Extension() != "jpeg" {
		t.Fatalf("ext keeps the client's jpeg spelling: %q", f.Extension())
	}
	if !f.IsValid() || f.Size() == 0 {
		t.Fatal("expected a valid, non-empty file")
	}
	name := f.HashName("photos")
	if !strings.HasPrefix(name, "photos/") || !strings.HasSuffix(name, ".jpeg") || len(name) != len("photos/")+40+len(".jpeg") {
		t.Fatalf("hash name: %q", name)
	}
}

func TestExtensionComesFromTheBytes(t *testing.T) {
	cases := map[string]string{
		// a PNG named .exe is a png
		"evil.exe":    "png",
		"report.pdf":  "pdf",
		"notes.txt":   "txt",
		"sheet.xlsx":  "xlsx",
		"x.weird$ext": "",
	}
	for name, want := range cases {
		var f *storage.UploadedFile
		switch {
		case strings.HasSuffix(name, ".exe"):
			f = gltest.FakeFile{Name: name, Content: gltest.FakeImage("a.png", 2, 2).Content}.UploadedFile()
		case strings.HasSuffix(name, "$ext"):
			f = gltest.FakeUpload(name, 1).UploadedFile()
		default:
			f = gltest.FakeUpload(name, 2).UploadedFile()
		}
		if got := f.Extension(); got != want {
			t.Errorf("%s: extension %q, want %q", name, got, want)
		}
	}
}

func TestStoreAndStoreAs(t *testing.T) {
	disk := gltest.FakeStorage(t, "public")
	f := gltest.FakeUpload("cv.pdf", 3).UploadedFile()

	p, err := f.Store("docs", "public")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, "docs/") || !strings.HasSuffix(p, ".pdf") {
		t.Fatalf("stored at %q", p)
	}
	disk.AssertExists(p).AssertCount("docs", 1)

	p2, err := f.StorePubliclyAs("../../etc", "../passwd")
	if err != nil {
		t.Fatal(err)
	}
	if p2 != "passwd" {
		t.Fatalf("traversal must be cleaned: %q", p2)
	}
	if b, _ := disk.Get(p2); len(b) != 3*1024 {
		t.Fatalf("stored %d bytes", len(b))
	}

	if err := storage.DeleteMany([]string{p, p2, "missing.txt"}, "public"); err != nil {
		t.Fatal(err)
	}
	disk.AssertMissing(p, p2).AssertDirectoryEmpty("")

	var none *storage.UploadedFile
	if _, err := none.Store("x"); err != storage.ErrNoFile {
		t.Fatalf("nil file: %v", err)
	}
}

func TestPutFileUsesTheNamedDisk(t *testing.T) {
	docs := gltest.FakeStorage(t, "docs")
	p, err := storage.PutFileAs("contracts", gltest.FakeUpload("a.pdf", 1).UploadedFile(), "nda.pdf", "docs")
	if err != nil || p != "contracts/nda.pdf" {
		t.Fatalf("%q %v", p, err)
	}
	docs.AssertExists("contracts/nda.pdf")
}

func TestVisibilityAndDisplayURL(t *testing.T) {
	gltest.FakeStorage(t, "public")
	gltest.FakeStorage(t, "private-docs")
	if storage.Visibility("public") != storage.VisibilityPublic {
		t.Fatal("the public disk is public")
	}
	if storage.Visibility("private-docs") != storage.VisibilityPrivate {
		t.Fatal("other disks are private")
	}
	u, _ := storage.DisplayURL("a.png", time.Minute, "public")
	if strings.Contains(u, "signature") {
		t.Fatalf("public URL should not be signed: %s", u)
	}
	u, _ = storage.DisplayURL("a.pdf", time.Minute, "private-docs")
	if !strings.Contains(u, "signature=") {
		t.Fatalf("private URL should be signed: %s", u)
	}
}
