package flutter

import (
	"bytes"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

func init() {
	_ = mime.AddExtensionType(".wasm", "application/wasm")
	_ = mime.AddExtensionType(".otf", "font/otf")
	_ = mime.AddExtensionType(".ttf", "font/ttf")
	_ = mime.AddExtensionType(".woff", "font/woff")
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".json", "application/json")
	_ = mime.AddExtensionType(".bin", "application/octet-stream")
	_ = mime.AddExtensionType(".frag", "text/plain")
}

// Options defines configuration for serving Flutter Web applications from GoLaravel.
type Options struct {
	MountPath     string        // e.g. "/" or "/app"
	DistDir       string        // path to flutter build/web directory
	FallbackSPA   bool          // fallback non-file routes to index.html for Flutter Web client routing
	LoadingConfig LoadingConfig // visual pre-Flutter HTML/CSS loading screen configuration
	Version       string        // application semantic version (e.g. "1.4.0")
	BuildID       string        // deployed build identifier / timestamp
}

// DefaultOptions provides default settings for Flutter Web serving.
func DefaultOptions() Options {
	return Options{
		MountPath:     "/app",
		DistDir:       "client/build/web",
		FallbackSPA:   true,
		LoadingConfig: DefaultLoadingConfig(),
	}
}

// findDistDir searches for the Flutter Web build directory across common workspace locations.
func findDistDir(configured string) string {
	candidates := []string{
		configured,
		filepath.Join("example", "client", "build", "web"),
		filepath.Join("client", "build", "web"),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !info.IsDir() {
			return c
		}
	}
	return configured
}

// ServeWeb registers routes to serve a Flutter Web build from GoLaravel.
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

	dist := findDistDir(config.DistDir)
	fileServer := http.FileServer(http.Dir(dist))

	serveIndex := func(c *context.Context) error {
		indexPath := filepath.Join(dist, "index.html")
		content, err := os.ReadFile(indexPath)
		if err != nil {
			return c.String(http.StatusNotFound, "Flutter Web index.html not found in: %s", dist)
		}

		// Adjust <base href="..."> if mounted under a sub-path like /app/
		baseHref := "/"
		if mount != "" {
			baseHref = mount + "/"
		}

		if baseHref != "/" {
			content = bytes.Replace(content, []byte(`<base href="/">`), []byte(`<base href="`+baseHref+`">`), 1)
		}

		// Inject version and build-id meta tags if configured
		if config.Version != "" || config.BuildID != "" {
			metaTags := ""
			if config.Version != "" {
				metaTags += `<meta name="app-version" content="` + config.Version + `">`
				c.SetHeader("X-App-Version", config.Version)
			}
			if config.BuildID != "" {
				metaTags += `<meta name="app-build-id" content="` + config.BuildID + `">`
				c.SetHeader("X-App-Build-ID", config.BuildID)
			}
			if strings.Contains(string(content), "<head>") {
				content = bytes.Replace(content, []byte("<head>"), []byte("<head>\n  "+metaTags), 1)
			}
			if config.BuildID != "" {
				content = bytes.Replace(content, []byte(`src="flutter_bootstrap.js"`), []byte(`src="flutter_bootstrap.js?v=`+config.BuildID+`"`), 1)
			}
		}

		// Inject customizable pre-loader screen into index.html
		if config.LoadingConfig.Enabled {
			content = InjectLoadingScreen(content, config.LoadingConfig)
		}

		c.SetHeader("Cache-Control", "no-cache, no-store, must-revalidate")
		c.SetHeader("Pragma", "no-cache")
		c.SetHeader("Expires", "0")
		c.SetHeader("Content-Type", "text/html; charset=utf-8")
		return c.Bytes(http.StatusOK, "text/html; charset=utf-8", content)
	}

	// 1. If mounted on sub-path (e.g. /app), register /app to redirect to /app/
	if mount != "" {
		r.GET(mount, func(c *context.Context) error {
			return c.Redirect(http.StatusMovedPermanently, mount+"/")
		})
	}

	// 2. Catch-all route for Flutter Web (handles /app/, /app/main.dart.js, /app/settings, etc.)
	routePath := mount + "/*filepath"
	if mount == "" {
		routePath = "/*filepath"
	}

	r.GET(routePath, func(c *context.Context) error {
		reqPath := c.Param("filepath")
		reqPath = strings.TrimPrefix(reqPath, "/")

		if reqPath == "" || reqPath == "index.html" {
			return serveIndex(c)
		}

		filePath := filepath.Join(dist, filepath.Clean("/"+reqPath))
		info, err := os.Stat(filePath)

		// Intercept flutter_bootstrap.js to inject main.dart.js cache-busting and disable service worker caching
		if reqPath == "flutter_bootstrap.js" && err == nil && !info.IsDir() {
			content, readErr := os.ReadFile(filePath)
			if readErr == nil {
				if config.BuildID != "" {
					content = bytes.Replace(content, []byte(`"mainJsPath":"main.dart.js"`), []byte(`"mainJsPath":"main.dart.js?v=`+config.BuildID+`"`), -1)
				}
				content = bytes.Replace(content, []byte(`serviceWorkerSettings:`), []byte(`/* disabled */ _swSettings:`), -1)

				c.SetHeader("Cache-Control", "no-cache, no-store, must-revalidate")
				c.SetHeader("Pragma", "no-cache")
				c.SetHeader("Expires", "0")
				c.SetHeader("Content-Type", "application/javascript; charset=utf-8")
				return c.Bytes(http.StatusOK, "application/javascript; charset=utf-8", content)
			}
		}

		// Serve static file if it exists on disk
		if err == nil && !info.IsDir() {
			if strings.HasSuffix(reqPath, ".js") || strings.HasSuffix(reqPath, ".json") ||
				strings.HasSuffix(reqPath, ".otf") || strings.HasSuffix(reqPath, ".ttf") ||
				strings.HasSuffix(reqPath, ".woff") || strings.HasSuffix(reqPath, ".woff2") ||
				strings.HasSuffix(reqPath, ".wasm") {
				c.SetHeader("Cache-Control", "no-cache, no-store, must-revalidate")
				c.SetHeader("Pragma", "no-cache")
				c.SetHeader("Expires", "0")
			}
			if mount != "" {
				http.StripPrefix(mount, fileServer).ServeHTTP(&c.Response, c.Request)
			} else {
				fileServer.ServeHTTP(&c.Response, c.Request)
			}
			return nil
		}

		// Fallback to index.html for Flutter client-side SPA routing
		if config.FallbackSPA {
			return serveIndex(c)
		}

		return c.String(http.StatusNotFound, "Flutter Web asset '%s' not found", reqPath)
	})
}
