package storage

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Disk visibility, as in Laravel: a public disk's files may be linked
// with URL; a private disk's files need TemporaryURL.
const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

// Visibility returns the visibility of the named disk (the default disk
// when omitted). Drivers report it with a Visibility() string method;
// drivers that don't are treated as private.
func Visibility(disk ...string) string {
	if v, ok := Disk(disk...).(interface{ Visibility() string }); ok && v.Visibility() == VisibilityPublic {
		return VisibilityPublic
	}
	return VisibilityPrivate
}

// Visibility reports VisibilityPublic when objects are linked through a
// public bucket or CDN (see S3Config.Visibility).
func (d *S3Driver) Visibility() string {
	switch d.cfg.Visibility {
	case VisibilityPublic, VisibilityPrivate:
		return d.cfg.Visibility
	}
	if d.cfg.PublicURLPrefix != "" {
		return VisibilityPublic
	}
	return VisibilityPrivate
}

// DisplayURL returns a URL to show or download a file: the plain URL on a
// public disk, and a TemporaryURL valid for ttl on a private one.
func DisplayURL(p string, ttl time.Duration, disk ...string) (string, error) {
	d := Disk(disk...)
	if d == nil {
		return "", ErrDiskNotFound
	}
	if Visibility(disk...) == VisibilityPublic {
		return d.URL(p), nil
	}
	return d.TemporaryURL(p, ttl)
}

// MountPaths returns the URL path prefixes of the registered local disks
// (such as "/storage"), the ones Handler serves. Prefixes inside another
// are left out.
func MountPaths() []string { return defaultDiskManager().MountPaths() }

// MountPaths returns the URL path prefixes of the manager's local disks.
func (dm *DiskManager) MountPaths() []string {
	var all []string
	for _, ld := range dm.localDisks() {
		if p := urlPath(ld.baseURL); p != "" {
			all = append(all, p)
		}
	}
	sort.Strings(all)
	var out []string
	for _, p := range all {
		if len(out) > 0 && (p == out[len(out)-1] || strings.HasPrefix(p, out[len(out)-1]+"/")) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (dm *DiskManager) localDisks() []*LocalDriver {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	var out []*LocalDriver
	for _, d := range dm.disks {
		if ld, ok := d.(*LocalDriver); ok && ld != nil {
			out = append(out, ld)
		}
	}
	return out
}

// urlPath returns the path of a disk's base URL ("/storage" for both
// "/storage" and "https://app.test/storage"), or "" for a root URL.
func urlPath(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	p := "/" + strings.Trim(u.Path, "/")
	if p == "/" {
		return ""
	}
	return p
}

// resolve finds the local disk whose URL prefix is the longest match for
// the request path, and the file path relative to it.
func (dm *DiskManager) resolve(reqPath string) (*LocalDriver, string) {
	var best *LocalDriver
	bestLen := -1
	rel := ""
	for _, ld := range dm.localDisks() {
		p := urlPath(ld.baseURL)
		if p == "" || len(p) <= bestLen {
			continue
		}
		if reqPath == p || !strings.HasPrefix(reqPath, p+"/") {
			continue
		}
		best, bestLen, rel = ld, len(p), strings.TrimPrefix(reqPath, p+"/")
	}
	return best, rel
}

// Handler serves the files of the local disks, as mounted by
// app.ServeStorage:
//
//   - GET / HEAD on a public disk (the "public" disk) serve the file.
//   - GET / HEAD with a valid TemporaryURL signature serve a file of any
//     disk; a bad or expired signature gets 403.
//   - PUT with a valid PresignedPutURL signature stores the request body
//     (direct uploads); it answers 201.
//
// Other requests for private files get 404, as do directories and paths
// with a segment starting with "." (such as .env or .git). Files that a
// browser could run as a page (HTML, SVG, XML, scripts) are always sent
// as downloads, and every response carries nosniff and a sandboxing
// Content-Security-Policy. Add ?download (or ?download=name.ext) to any
// URL to download the file instead of showing it.
func Handler() http.Handler { return defaultDiskManager() }

// ServeHTTP implements Handler for the manager's disks.
func (dm *DiskManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d, rel := dm.resolve(r.URL.Path)
	if d == nil || rel == "" || hiddenPath(rel) {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	signed := q.Get("signature") != ""
	upload := q.Get("action") == "upload"

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if upload {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if signed {
			if err := d.VerifySignedURL(r.URL.RequestURI()); err != nil {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		} else if d.Visibility() != VisibilityPublic {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, d, rel, signed)
	case http.MethodPut:
		if !signed || !upload {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if err := d.VerifySignedURL(r.URL.RequestURI()); err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if err := d.PutReader(rel, r.Body); err != nil {
			_ = d.Delete(rel)
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Upload failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	default:
		w.Header().Set("Allow", "GET, HEAD, PUT")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func hiddenPath(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." || strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// inlineTypes are shown in the browser; everything else is downloaded.
var inlineTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"image/bmp": true, "image/avif": true, "image/x-icon": true, "image/vnd.microsoft.icon": true,
	"application/pdf": true, "text/plain": true, "text/csv": true, "application/json": true,
}

func serveFile(w http.ResponseWriter, r *http.Request, d *LocalDriver, rel string, signed bool) {
	full := d.fullPath(rel)
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	ct, _, _ := mime.ParseMediaType(mime.TypeByExtension(filepath.Ext(full)))
	if ct == "" {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(f, buf)
		ct, _, _ = mime.ParseMediaType(http.DetectContentType(buf[:n]))
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	h := w.Header()
	h.Set("Content-Type", contentTypeHeader(ct))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	if signed {
		h.Set("Cache-Control", "private, no-cache")
	} else {
		h.Set("Cache-Control", "public, max-age=3600")
	}

	inline := inlineTypes[ct] || strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "audio/")
	q := r.URL.Query()
	name := path.Base(filepath.ToSlash(rel))
	if _, ok := q["download"]; ok {
		inline = false
		if n := strings.TrimSpace(q.Get("download")); n != "" {
			name = path.Base(strings.ReplaceAll(n, `\`, "/"))
		}
	}
	disposition := "inline"
	if !inline {
		disposition = "attachment"
	}
	if v := mime.FormatMediaType(disposition, map[string]string{"filename": name}); v != "" {
		h.Set("Content-Disposition", v)
	} else {
		h.Set("Content-Disposition", disposition)
	}

	http.ServeContent(w, r, name, info.ModTime(), f)
}

func contentTypeHeader(ct string) string {
	if ct == "" {
		return "application/octet-stream"
	}
	if strings.HasPrefix(ct, "text/") || ct == "application/json" {
		return ct + "; charset=utf-8"
	}
	return ct
}
