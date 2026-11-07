package appupdate

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

// Platform defines target client environments.
type Platform string

const (
	PlatformWeb     Platform = "web"
	PlatformAndroid Platform = "android"
	PlatformIOS     Platform = "ios"
	PlatformAll     Platform = "all"
)

// AppRule defines the version requirements and upgrade policies for a mobile or client app.
type AppRule struct {
	LatestVersion  string    `json:"latest_version"`            // e.g. "1.4.0"
	LatestBuild    int       `json:"latest_build,omitempty"`    // e.g. 14 (numeric build number)
	MinimumVersion string    `json:"minimum_version,omitempty"` // e.g. "1.2.0". If client < MinimumVersion => ForceUpdate = true
	MinimumBuild   int       `json:"minimum_build,omitempty"`   // e.g. 10. If client < MinimumBuild => ForceUpdate = true
	ForceUpdate    bool      `json:"force_update,omitempty"`    // If true, any version < Latest is forced
	Title          string    `json:"title,omitempty"`           // Custom notification title (e.g. "Update Required")
	Message        string    `json:"message,omitempty"`         // Custom notification body
	UpdateURL      string    `json:"update_url,omitempty"`      // Play Store, App Store, or direct APK download URL
	ReleaseNotes   string    `json:"release_notes,omitempty"`   // Changelog or description
	ReleasedAt     time.Time `json:"released_at,omitempty"`
}

// WebRule defines the versioning and reload prompt rules for Web / Admin applications.
type WebRule struct {
	BuildID       string        `json:"build_id"`                 // Unique build identifier (commit hash, timestamp, or release ID)
	Version       string        `json:"version"`                  // e.g. "1.4.0"
	ForceReload   bool          `json:"force_reload,omitempty"`   // If true, clients must reload immediately (blocking overlay)
	CheckInterval time.Duration `json:"check_interval,omitempty"` // Polling interval for web clients (default 5m)
	Title         string        `json:"title,omitempty"`          // e.g. "System Update Available"
	Message       string        `json:"message,omitempty"`        // e.g. "A new version of the system is live. Please reload."
}

// CheckQuery represents a client version check request.
type CheckQuery struct {
	Platform string `json:"platform"`           // "web", "android", "ios"
	App      string `json:"app"`                // e.g. "user_app", "partner_worker", "admin"
	Version  string `json:"version"`            // Client's current semantic version (e.g. "1.3.1")
	Build    int    `json:"build,omitempty"`   // Client's current build number (e.g. 12)
	BuildID  string `json:"build_id,omitempty"`// Client's current web build ID
}

// CheckResult is returned by the update check API.
type CheckResult struct {
	UpdateAvailable      bool   `json:"update_available"`
	ForceUpdate          bool   `json:"force_update"`          // If true, client cannot proceed without updating / reloading
	LatestVersion        string `json:"latest_version"`
	LatestBuild          int    `json:"latest_build,omitempty"`
	MinimumVersion       string `json:"minimum_version,omitempty"`
	MinimumBuild         int    `json:"minimum_build,omitempty"`
	CurrentVersion       string `json:"current_version,omitempty"`
	CurrentBuild         int    `json:"current_build,omitempty"`
	BuildID              string `json:"build_id,omitempty"`    // Returned for web applications
	Title                string `json:"title,omitempty"`
	Message              string `json:"message,omitempty"`
	UpdateURL            string `json:"update_url,omitempty"`
	ReleaseNotes         string `json:"release_notes,omitempty"`
	CheckIntervalSeconds int    `json:"check_interval_seconds,omitempty"`
}

// Manager manages system update policies for web and mobile clients.
type Manager struct {
	mu     sync.RWMutex
	web    WebRule
	mobile map[string]AppRule
}

// New creates an initialized Manager.
func New() *Manager {
	return &Manager{
		web: WebRule{
			CheckInterval: 5 * time.Minute,
			Title:         "System Update Available",
			Message:       "A new version of the application is available. Please reload to apply updates.",
		},
		mobile: make(map[string]AppRule),
	}
}

// SetWeb configures update and reload policies for web applications.
func (m *Manager) SetWeb(rule WebRule) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rule.CheckInterval <= 0 {
		rule.CheckInterval = 5 * time.Minute
	}
	if rule.Title == "" {
		rule.Title = "System Update Available"
	}
	if rule.Message == "" {
		rule.Message = "A new version of the application is available. Please reload to apply updates."
	}
	m.web = rule
	return m
}

// GetWeb returns the current WebRule.
func (m *Manager) GetWeb() WebRule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.web
}

// SetMobile sets the update rule for a specific app identifier or platform ("android", "ios", "default", etc.).
func (m *Manager) SetMobile(key string, rule AppRule) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rule.Title == "" {
		if rule.ForceUpdate {
			rule.Title = "Mandatory Update Required"
		} else {
			rule.Title = "Update Available"
		}
	}
	if rule.Message == "" {
		if rule.ForceUpdate {
			rule.Message = fmt.Sprintf("A required update (%s) is needed to continue using the application.", rule.LatestVersion)
		} else {
			rule.Message = fmt.Sprintf("Version %s is now available with new features and improvements.", rule.LatestVersion)
		}
	}
	m.mobile[strings.ToLower(strings.TrimSpace(key))] = rule
	return m
}

// GetMobile retrieves the update rule for the given app key or platform.
func (m *Manager) GetMobile(key string) (AppRule, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	norm := strings.ToLower(strings.TrimSpace(key))
	if rule, ok := m.mobile[norm]; ok {
		return rule, true
	}
	// Fallback to "default" or "all"
	if rule, ok := m.mobile["default"]; ok {
		return rule, true
	}
	if rule, ok := m.mobile["all"]; ok {
		return rule, true
	}
	return AppRule{}, false
}

// Check evaluates client version against current policies.
func (m *Manager) Check(q CheckQuery) CheckResult {
	platform := strings.ToLower(strings.TrimSpace(q.Platform))

	// 1. Web client check
	if platform == "web" {
		m.mu.RLock()
		web := m.web
		m.mu.RUnlock()

		intervalSecs := int(web.CheckInterval.Seconds())
		if intervalSecs <= 0 {
			intervalSecs = 300
		}

		// If client provided a BuildID or Version, compare
		updateAvailable := false
		if q.BuildID != "" && web.BuildID != "" {
			updateAvailable = (q.BuildID != web.BuildID)
		} else if q.Version != "" {
			if web.BuildID != "" && q.Version == web.BuildID {
				updateAvailable = false
			} else if web.BuildID != "" && !strings.Contains(q.Version, ".") {
				updateAvailable = (q.Version != web.BuildID)
			} else if web.Version != "" {
				updateAvailable = (CompareVersions(q.Version, web.Version) < 0)
			}
		}

		return CheckResult{
			UpdateAvailable:      updateAvailable,
			ForceUpdate:          web.ForceReload,
			LatestVersion:        web.Version,
			BuildID:              web.BuildID,
			CurrentVersion:       q.Version,
			Title:                web.Title,
			Message:              web.Message,
			CheckIntervalSeconds: intervalSecs,
		}
	}

	// 2. Mobile client check
	// Try finding rule by app name first, then by platform (e.g. "android", "ios"), then "default"
	var rule AppRule
	var found bool

	if q.App != "" {
		rule, found = m.GetMobile(q.App)
	}
	if !found && platform != "" {
		rule, found = m.GetMobile(platform)
	}
	if !found {
		rule, found = m.GetMobile("default")
	}

	if !found {
		return CheckResult{
			UpdateAvailable: false,
			ForceUpdate:     false,
			CurrentVersion:  q.Version,
			CurrentBuild:    q.Build,
		}
	}

	updateAvailable := false
	forceUpdate := false

	// Compare build numbers first if both are provided (> 0)
	if q.Build > 0 && rule.LatestBuild > 0 {
		if q.Build < rule.LatestBuild {
			updateAvailable = true
			if rule.MinimumBuild > 0 && q.Build < rule.MinimumBuild {
				forceUpdate = true
			} else if rule.ForceUpdate {
				forceUpdate = true
			}
		}
	} else if q.Version != "" && rule.LatestVersion != "" {
		// Compare semantic version strings
		cmpLatest := CompareVersions(q.Version, rule.LatestVersion)
		if cmpLatest < 0 {
			updateAvailable = true

			if rule.MinimumVersion != "" {
				cmpMin := CompareVersions(q.Version, rule.MinimumVersion)
				if cmpMin < 0 {
					forceUpdate = true
				} else if rule.ForceUpdate {
					forceUpdate = true
				}
			} else if rule.ForceUpdate {
				forceUpdate = true
			}
		}
	}

	title := rule.Title
	if forceUpdate && !strings.Contains(strings.ToLower(title), "required") && !strings.Contains(strings.ToLower(title), "mandatory") {
		title = "Update Required"
	}

	return CheckResult{
		UpdateAvailable: updateAvailable,
		ForceUpdate:     forceUpdate,
		LatestVersion:   rule.LatestVersion,
		LatestBuild:     rule.LatestBuild,
		MinimumVersion:  rule.MinimumVersion,
		MinimumBuild:    rule.MinimumBuild,
		CurrentVersion:  q.Version,
		CurrentBuild:    q.Build,
		Title:           title,
		Message:         rule.Message,
		UpdateURL:       rule.UpdateURL,
		ReleaseNotes:    rule.ReleaseNotes,
	}
}

// CompareVersions compares two semantic version strings (e.g. "1.3.1" and "1.4.0").
// Returns -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2.
func CompareVersions(v1, v2 string) int {
	clean1 := cleanVersionString(v1)
	clean2 := cleanVersionString(v2)

	parts1 := strings.Split(clean1, ".")
	parts2 := strings.Split(clean2, ".")

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(parts1) {
			n1, _ = strconv.Atoi(parts1[i])
		}
		if i < len(parts2) {
			n2, _ = strconv.Atoi(parts2[i])
		}

		if n1 < n2 {
			return -1
		}
		if n1 > n2 {
			return 1
		}
	}

	return 0
}

func cleanVersionString(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	// Strip build metadata after '+' (e.g. "1.4.1+7" -> "1.4.1")
	if idx := strings.Index(v, "+"); idx != -1 {
		v = v[:idx]
	}
	// Strip pre-release tag after '-' (e.g. "1.4.0-beta.1" -> "1.4.0")
	if idx := strings.Index(v, "-"); idx != -1 {
		v = v[:idx]
	}
	return v
}

// RegisterRoutes registers the version and update check routes on the router.
func (m *Manager) RegisterRoutes(r *router.Router, pathPrefix ...string) {
	prefix := "/api/app-update"
	if len(pathPrefix) > 0 && pathPrefix[0] != "" {
		prefix = strings.TrimSuffix(pathPrefix[0], "/")
	}

	handler := func(c *context.Context) error {
		// Read parameters from Query String or HTTP Headers
		platform := c.Query("platform")
		if platform == "" {
			platform = c.Header("X-App-Platform")
		}

		app := c.Query("app")
		if app == "" {
			app = c.Header("X-App-ID")
		}

		version := c.Query("version")
		if version == "" {
			version = c.Header("X-App-Version")
		}

		buildStr := c.Query("build")
		if buildStr == "" {
			buildStr = c.Header("X-App-Build")
		}
		build, _ := strconv.Atoi(buildStr)

		buildID := c.Query("build_id")
		if buildID == "" {
			buildID = c.Header("X-App-Build-ID")
		}

		result := m.Check(CheckQuery{
			Platform: platform,
			App:      app,
			Version:  version,
			Build:    build,
			BuildID:  buildID,
		})

		// Set informational headers
		if result.UpdateAvailable {
			c.SetHeader("X-App-Update-Available", "true")
			if result.ForceUpdate {
				c.SetHeader("X-App-Force-Update", "true")
			} else {
				c.SetHeader("X-App-Force-Update", "false")
			}
			if result.LatestVersion != "" {
				c.SetHeader("X-App-Latest-Version", result.LatestVersion)
			}
		} else {
			c.SetHeader("X-App-Update-Available", "false")
		}

		return c.JSON(http.StatusOK, result)
	}

	r.GET(prefix+"/check", handler)
	r.GET(prefix, handler)
}

// Middleware returns a GoLaravel middleware that evaluates version headers and informs clients of updates.
func (m *Manager) Middleware() context.HandlerFunc {
	return func(c *context.Context) error {
		platform := c.Header("X-App-Platform")
		version := c.Header("X-App-Version")

		if platform != "" && (version != "" || c.Header("X-App-Build-ID") != "") {
			app := c.Header("X-App-ID")
			build, _ := strconv.Atoi(c.Header("X-App-Build"))
			buildID := c.Header("X-App-Build-ID")

			res := m.Check(CheckQuery{
				Platform: platform,
				App:      app,
				Version:  version,
				Build:    build,
				BuildID:  buildID,
			})

			if res.UpdateAvailable {
				c.SetHeader("X-App-Update-Available", "true")
				if res.ForceUpdate {
					c.SetHeader("X-App-Force-Update", "true")
				}
				if res.LatestVersion != "" {
					c.SetHeader("X-App-Latest-Version", res.LatestVersion)
				}
			}
		}

		return c.Next()
	}
}
