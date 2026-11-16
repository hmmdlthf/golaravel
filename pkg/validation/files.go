package validation

import (
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

func files(v any) []*multipart.FileHeader {
	switch x := v.(type) {
	case *multipart.FileHeader:
		if x != nil {
			return []*multipart.FileHeader{x}
		}
	case []*multipart.FileHeader:
		return x
	case []any:
		var out []*multipart.FileHeader
		for _, e := range x {
			f, ok := e.(*multipart.FileHeader)
			if !ok {
				return nil
			}
			out = append(out, f)
		}
		return out
	}
	return nil
}

// sniff detects the content type from the file's first 512 bytes; the
// client-sent Content-Type and the file name are never trusted alone.
func sniff(f *multipart.FileHeader) string {
	r, err := f.Open()
	if err != nil {
		return ""
	}
	defer r.Close()
	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	ct, _, _ := mime.ParseMediaType(http.DetectContentType(buf[:n]))
	return ct
}

// safeImages excludes SVG, which can carry scripts.
var safeImages = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/bmp": true}

// textLike extensions whose content sniffs as text/plain.
var textLike = map[string]bool{"txt": true, "csv": true, "json": true, "md": true, "log": true, "xml": true, "yaml": true, "yml": true}

func fileMatches(f *multipart.FileHeader, rule string, params []string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(f.Filename), "."))
	switch rule {
	case "extensions":
		for _, p := range params {
			if strings.EqualFold(strings.TrimSpace(p), ext) {
				return true
			}
		}
		return false
	case "image":
		return safeImages[sniff(f)]
	case "mimetypes":
		ct := sniff(f)
		for _, p := range params {
			p = strings.ToLower(strings.TrimSpace(p))
			if p == ct || (strings.HasSuffix(p, "/*") && strings.HasPrefix(ct, strings.TrimSuffix(p, "*"))) {
				return true
			}
		}
		return false
	case "mimes":
		ct := sniff(f)
		for _, p := range params {
			want := strings.ToLower(strings.TrimSpace(p))
			if want == "jpg" {
				want = "jpeg"
			}
			// The extension must be one of the allowed ones and agree with
			// the sniffed content.
			if ext != want && !(want == "jpeg" && ext == "jpg") {
				continue
			}
			expected, _, _ := mime.ParseMediaType(mime.TypeByExtension("." + want))
			switch {
			case expected != "" && expected == ct:
				return true
			case textLike[want] && strings.HasPrefix(ct, "text/"):
				return true
			case want == "zip" && ct == "application/zip":
				return true
			}
		}
		return false
	}
	return false
}
