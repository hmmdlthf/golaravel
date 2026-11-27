package storage

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryDriver keeps files in memory. It backs gltest.FakeStorage and is
// handy wherever a throwaway disk is needed.
type MemoryDriver struct {
	mu         sync.RWMutex
	files      map[string]memFile
	baseURL    string
	visibility string
}

type memFile struct {
	data     []byte
	modified time.Time
}

// NewMemoryDriver returns an empty in-memory disk whose URLs start with
// baseURL ("/storage/fake" when empty).
func NewMemoryDriver(baseURL ...string) *MemoryDriver {
	u := "/storage/fake"
	if len(baseURL) > 0 && baseURL[0] != "" {
		u = strings.TrimRight(baseURL[0], "/")
	}
	return &MemoryDriver{files: map[string]memFile{}, baseURL: u}
}

// SetVisibility sets the disk's visibility (see Visibility).
func (d *MemoryDriver) SetVisibility(v string) *MemoryDriver {
	d.visibility = v
	return d
}

// Visibility returns VisibilityPublic or VisibilityPrivate.
func (d *MemoryDriver) Visibility() string {
	if d.visibility == VisibilityPublic {
		return VisibilityPublic
	}
	return VisibilityPrivate
}

func memKey(p string) string { return joinPath(filepath.ToSlash(p)) }

func (d *MemoryDriver) Put(p string, contents []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.files[memKey(p)] = memFile{data: append([]byte(nil), contents...), modified: time.Now()}
	return nil
}

func (d *MemoryDriver) PutReader(p string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return d.Put(p, b)
}

func (d *MemoryDriver) Get(p string) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	f, ok := d.files[memKey(p)]
	if !ok {
		return nil, ErrFileNotFound
	}
	return append([]byte(nil), f.data...), nil
}

func (d *MemoryDriver) GetStream(p string) (io.ReadCloser, error) {
	b, err := d.Get(p)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (d *MemoryDriver) Exists(p string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.files[memKey(p)]
	return ok
}

func (d *MemoryDriver) Delete(p string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.files, memKey(p))
	return nil
}

func (d *MemoryDriver) Copy(src, dst string) error {
	b, err := d.Get(src)
	if err != nil {
		return err
	}
	return d.Put(dst, b)
}

func (d *MemoryDriver) Move(src, dst string) error {
	if err := d.Copy(src, dst); err != nil {
		return err
	}
	return d.Delete(src)
}

func (d *MemoryDriver) Size(p string) (int64, error) {
	b, err := d.Get(p)
	return int64(len(b)), err
}

func (d *MemoryDriver) LastModified(p string) (time.Time, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	f, ok := d.files[memKey(p)]
	if !ok {
		return time.Time{}, ErrFileNotFound
	}
	return f.modified, nil
}

func (d *MemoryDriver) MimeType(p string) (string, error) {
	if t := mime.TypeByExtension(filepath.Ext(p)); t != "" {
		return t, nil
	}
	b, err := d.Get(p)
	if err != nil {
		return "", err
	}
	return http.DetectContentType(b), nil
}

func (d *MemoryDriver) URL(p string) string { return d.baseURL + "/" + memKey(p) }

func (d *MemoryDriver) TemporaryURL(p string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("%s?expires=%d&signature=fake", d.URL(p), time.Now().Add(ttl).Unix()), nil
}

func (d *MemoryDriver) PresignedPutURL(p string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("%s?action=upload&expires=%d&signature=fake", d.URL(p), time.Now().Add(ttl).Unix()), nil
}

// Files lists the stored paths inside dir (all of them when dir is "" or
// omitted), sorted. With recursive false only files directly in dir are
// listed.
func (d *MemoryDriver) Files(dir string, recursive ...bool) []string {
	deep := len(recursive) == 0 || recursive[0]
	prefix := memKey(dir)
	if prefix != "" {
		prefix += "/"
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	var out []string
	for k := range d.files {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if !deep && strings.Contains(strings.TrimPrefix(k, prefix), "/") {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Swap registers d as the named disk and returns a function that puts the
// previous disk (or none) back.
func Swap(name string, d Driver) (restore func()) {
	dm := defaultDiskManager()
	dm.mu.Lock()
	prev, had := dm.disks[name]
	dm.disks[name] = d
	dm.mu.Unlock()
	return func() {
		dm.mu.Lock()
		defer dm.mu.Unlock()
		if had {
			dm.disks[name] = prev
		} else {
			delete(dm.disks, name)
		}
	}
}

// DefaultDiskName returns the name of the default disk.
func DefaultDiskName() string {
	dm := defaultDiskManager()
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.defaultDisk
}
