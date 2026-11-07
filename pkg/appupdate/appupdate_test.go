package appupdate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1, v2   string
		expected int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.2.0", "1.3.0", -1},
		{"1.3.0", "1.2.0", 1},
		{"1.2.1", "1.2.10", -1},
		{"1.10.0", "1.2.0", 1},
		{"1.4", "1.4.0", 0},
		{"1.4.0+5", "1.4.0+10", 0}, // semver metadata stripped
		{"1.3.5-beta.1", "1.3.5", 0},
		{"1.3.1", "1.4.0", -1},
	}

	for _, tt := range tests {
		actual := CompareVersions(tt.v1, tt.v2)
		if actual != tt.expected {
			t.Errorf("CompareVersions(%q, %q) = %d; want %d", tt.v1, tt.v2, actual, tt.expected)
		}
	}
}

func TestWebRuleCheck(t *testing.T) {
	mgr := New()
	mgr.SetWeb(WebRule{
		BuildID:       "build-20261107",
		Version:       "1.4.0",
		ForceReload:   true,
		CheckInterval: 2 * time.Minute,
		Title:         "System Upgrade",
		Message:       "Please reload immediately.",
	})

	// Same build ID -> no update
	res := mgr.Check(CheckQuery{
		Platform: "web",
		Version:  "build-20261107",
	})
	if res.UpdateAvailable {
		t.Errorf("Expected no update for identical build ID, got %+v", res)
	}

	// Different build ID -> update available and forced reload
	res = mgr.Check(CheckQuery{
		Platform: "web",
		Version:  "build-old",
	})
	if !res.UpdateAvailable {
		t.Errorf("Expected update available for different build ID, got %+v", res)
	}
	if !res.ForceUpdate {
		t.Errorf("Expected ForceUpdate=true because ForceReload=true, got %+v", res)
	}
	if res.CheckIntervalSeconds != 120 {
		t.Errorf("Expected CheckIntervalSeconds=120, got %d", res.CheckIntervalSeconds)
	}
}

func TestMobileRuleCheck(t *testing.T) {
	mgr := New()
	mgr.SetMobile("user_app", AppRule{
		LatestVersion:  "1.4.0",
		LatestBuild:    14,
		MinimumVersion: "1.2.0",
		MinimumBuild:   10,
		ForceUpdate:    false, // Only force if below minimum
		UpdateURL:      "https://example.com/download",
		Message:        "Enjoy the new features!",
	})

	// 1. Client up-to-date
	res := mgr.Check(CheckQuery{
		App:     "user_app",
		Version: "1.4.0",
		Build:   14,
	})
	if res.UpdateAvailable {
		t.Errorf("Expected no update, got %+v", res)
	}

	// 2. Client is between minimum and latest -> Optional update (ForceUpdate = false)
	res = mgr.Check(CheckQuery{
		App:     "user_app",
		Version: "1.3.0",
		Build:   12,
	})
	if !res.UpdateAvailable {
		t.Errorf("Expected UpdateAvailable=true, got %+v", res)
	}
	if res.ForceUpdate {
		t.Errorf("Expected ForceUpdate=false for version >= minimum, got %+v", res)
	}
	if res.UpdateURL != "https://example.com/download" {
		t.Errorf("Expected update URL, got %s", res.UpdateURL)
	}

	// 3. Client is below minimum -> Mandatory / Force Update (ForceUpdate = true)
	res = mgr.Check(CheckQuery{
		App:     "user_app",
		Version: "1.1.0",
		Build:   8,
	})
	if !res.UpdateAvailable {
		t.Errorf("Expected UpdateAvailable=true, got %+v", res)
	}
	if !res.ForceUpdate {
		t.Errorf("Expected ForceUpdate=true for version < minimum, got %+v", res)
	}

	// 4. Test explicit ForceUpdate=true rule
	mgr.SetMobile("critical_app", AppRule{
		LatestVersion: "2.0.0",
		ForceUpdate:   true,
	})
	res = mgr.Check(CheckQuery{
		App:     "critical_app",
		Version: "1.9.9",
	})
	if !res.ForceUpdate {
		t.Errorf("Expected ForceUpdate=true when rule specifies ForceUpdate=true")
	}
}

func TestHTTPRoutes(t *testing.T) {
	mgr := New()
	mgr.SetMobile("user_app", AppRule{
		LatestVersion:  "1.4.0",
		LatestBuild:    14,
		MinimumVersion: "1.2.0",
		MinimumBuild:   10,
		UpdateURL:      "https://benzat.com/app",
	})

	app := golaravel.New()
	mgr.RegisterRoutes(app.Router)

	// Make request
	req := httptest.NewRequest("GET", "/api/app-update/check?app=user_app&version=1.1.0&build=8", nil)
	w := httptest.NewRecorder()

	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("X-App-Update-Available") != "true" {
		t.Errorf("Expected header X-App-Update-Available=true, got %s", w.Header().Get("X-App-Update-Available"))
	}
	if w.Header().Get("X-App-Force-Update") != "true" {
		t.Errorf("Expected header X-App-Force-Update=true, got %s", w.Header().Get("X-App-Force-Update"))
	}

	var result CheckResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if !result.UpdateAvailable || !result.ForceUpdate {
		t.Errorf("Unexpected result: %+v", result)
	}
}

func TestMiddleware(t *testing.T) {
	mgr := New()
	mgr.SetMobile("android", AppRule{
		LatestVersion: "1.5.0",
		ForceUpdate:   false,
	})

	app := golaravel.New()
	app.Use(mgr.Middleware())
	app.GET("/api/test", func(c *context.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-App-Platform", "android")
	req.Header.Set("X-App-Version", "1.4.0")
	w := httptest.NewRecorder()

	app.ServeHTTP(w, req)

	if w.Header().Get("X-App-Update-Available") != "true" {
		t.Errorf("Expected X-App-Update-Available header in middleware, got %s", w.Header().Get("X-App-Update-Available"))
	}
	if w.Header().Get("X-App-Latest-Version") != "1.5.0" {
		t.Errorf("Expected X-App-Latest-Version header, got %s", w.Header().Get("X-App-Latest-Version"))
	}
}
