package offline_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golaravel/golaravel/pkg/auth"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/privacy"
)

func TestHTTPEndpoints(t *testing.T) {
	e := newEnv(t)
	app := golaravel.New()
	// Stand-ins for the app's auth and tenant middleware.
	signIn := func(c *gcontext.Context) error {
		if u := c.Header("X-Test-User"); u != "" {
			c.Set(auth.ContextUserKey, &auth.SimpleUser{ID: u})
		}
		if tn := c.Header("X-Test-Tenant"); tn != "" {
			c.Request = c.Request.WithContext(privacy.WithTenantID(c.Context(), tn))
		}
		return c.Next()
	}
	e.eng.Routes(app.Router.Group("/api/sync", signIn))
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	call := func(method, path, device string, body any, out any) int {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+"/api/sync"+path, rd)
		req.Header.Set("X-Test-User", "cashier-1")
		req.Header.Set("X-Test-Tenant", "acme")
		if device != "" {
			req.Header.Set(offline.DeviceHeader, device)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode
	}

	var dev offline.Device
	if code := call("POST", "/devices", "", offline.DeviceInfo{Name: "Till 1", Platform: "tauri"}, &dev); code != 201 {
		t.Fatalf("register: %d", code)
	}
	if dev.UserID != "cashier-1" || dev.TenantID != "acme" {
		t.Fatalf("device: %+v", dev)
	}

	c := newClock(dev.ID, e.now)
	var pushed offline.PushResponse
	code := call("POST", "/push", dev.ID, offline.PushRequest{Mutations: []offline.Mutation{
		create(c, "p1", set("name", "Tea", "stock", 3))}}, &pushed)
	if code != 200 || pushed.Results[0].Status != offline.StatusApplied || pushed.ServerHLC == "" {
		t.Fatalf("push: %d %+v", code, pushed)
	}

	var pulled offline.PullResponse
	if code := call("GET", "/pull?cursor=0", dev.ID, nil, &pulled); code != 200 || len(pulled.Changes) != 1 {
		t.Fatalf("pull: %d %+v", code, pulled)
	}

	var snap offline.SnapshotResponse
	if code := call("GET", "/snapshot?entity=product", dev.ID, nil, &snap); code != 200 || len(snap.Records) != 1 || snap.Cursor != 1 {
		t.Fatalf("snapshot: %d %+v", code, snap)
	}

	var errBody map[string]string
	for _, tc := range []struct {
		method, path, device string
		want                 int
		code                 string
	}{
		{"GET", "/pull?cursor=-1", dev.ID, 400, "bad_request"},
		{"GET", "/pull", "nope", 403, "unknown_device"},
		{"GET", "/snapshot?entity=order", dev.ID, 404, "unknown_entity"},
	} {
		if got := call(tc.method, tc.path, tc.device, nil, &errBody); got != tc.want || errBody["error"] != tc.code {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, got, errBody)
		}
	}

	// No signed-in user.
	req, _ := http.NewRequest("GET", srv.URL+"/api/sync/pull", nil)
	req.Header.Set("X-Test-Tenant", "acme")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous pull: %d", resp.StatusCode)
	}

	// Oversized bodies are refused before parsing.
	big := strings.Repeat("x", 5<<20)
	req, _ = http.NewRequest("POST", srv.URL+"/api/sync/push", strings.NewReader(`{"device_id":"`+big+`"}`))
	req.Header.Set("X-Test-User", "u")
	req.Header.Set("X-Test-Tenant", "acme")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 413 && resp.StatusCode != 400 {
		t.Fatalf("oversized push: %d", resp.StatusCode)
	}
}
