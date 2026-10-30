package flutter

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

// Options defines configuration for serving Flutter Web applications from GoLaravel.
type Options struct {
	MountPath   string // e.g. "/" or "/app"
	DistDir     string // path to flutter build/web directory
	FallbackSPA bool   // fallback non-file routes to index.html for Flutter Web client routing
}

// DefaultOptions provides default settings for Flutter Web serving.
func DefaultOptions() Options {
	return Options{
		MountPath:   "/",
		DistDir:     "client/build/web",
		FallbackSPA: true,
	}
}

// ServeWeb registers routes to serve a Flutter Web build from GoLaravel.
// It serves static files (main.dart.js, assets, canvaskit, etc.) and falls back to index.html for SPA routing,
// while preserving all other API and Templ routes.
func ServeWeb(r *router.Router, opts ...Options) {
	config := DefaultOptions()
	if len(opts) > 0 {
		config = opts[0]
	}

	mount := config.MountPath
	if !strings.HasPrefix(mount, "/") {
		mount = "/" + mount
	}
	mount = strings.TrimSuffix(mount, "/")

	dist := config.DistDir

	fileServer := http.FileServer(http.Dir(dist))

	// Catch-all route for Flutter Web mount
	routePath := mount + "/*filepath"
	if mount == "" {
		routePath = "/*filepath"
	}

	r.GET(routePath, func(c *context.Context) error {
		reqPath := c.Param("filepath")
		if reqPath == "" || reqPath == "/" {
			reqPath = "index.html"
		}

		filePath := filepath.Join(dist, filepath.Clean("/"+reqPath))
		info, err := os.Stat(filePath)

		// If file exists and is not a directory, serve the static asset directly
		if err == nil && !info.IsDir() {
			http.StripPrefix(mount, fileServer).ServeHTTP(&c.Response, c.Request)
			return nil
		}

		// Fallback to index.html for client-side Flutter routing (SPA)
		if config.FallbackSPA {
			indexPath := filepath.Join(dist, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				c.SetHeader("Content-Type", "text/html; charset=utf-8")
				http.ServeFile(&c.Response, c.Request, indexPath)
				return nil
			}
		}

		return c.String(http.StatusNotFound, "Flutter Web asset '%s' not found", reqPath)
	})
}
