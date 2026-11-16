package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/session"
)

type sessionApp struct {
	t   *testing.T
	mgr *session.Manager
	jar []*http.Cookie
}

func (a *sessionApp) do(method, path string, handlers ...context.HandlerFunc) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, nil)
	for _, ck := range a.jar {
		r.AddCookie(ck)
	}
	c := context.NewContext()
	c.Reset(w, r)
	c.SetHandlers(append([]context.HandlerFunc{session.StartSession(a.mgr)}, handlers...))
	_ = c.Next()
	if cks := w.Result().Cookies(); len(cks) > 0 {
		a.jar = cks
	}
	return w
}

func TestSessionLoginGuardLogout(t *testing.T) {
	driver := session.NewMemoryDriver()
	app := &sessionApp{t: t, mgr: session.NewManager(session.Config{Driver: driver, SecretKey: "test-key"})}
	users := UserProviderFunc(func(id string) (Authenticatable, error) {
		if id == "42" {
			return &SimpleUser{ID: "42", Email: "a@b.c"}, nil
		}
		return nil, errors.New("not found")
	})
	guard := SessionGuard(users, SessionGuardOptions{RedirectTo: "/login"})
	ok := func(c *context.Context) error { return c.String(200, "%s", User(c).GetAuthIdentifier()) }

	if w := app.do("GET", "/admin", guard, ok); w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
		t.Fatalf("guest: %d %s", w.Code, w.Header().Get("Location"))
	}
	var guestID, guestToken string
	app.do("GET", "/login", func(c *context.Context) error {
		guestID, guestToken = session.FromContext(c).ID(), session.Token(c)
		return nil
	})
	var loggedID string
	app.do("POST", "/login", func(c *context.Context) error {
		if err := Login(c, &SimpleUser{ID: "42"}); err != nil {
			t.Fatal(err)
		}
		s := session.FromContext(c)
		loggedID = s.ID()
		if loggedID == guestID || s.Token() == guestToken {
			t.Fatal("login must regenerate session id and CSRF token")
		}
		return nil
	})
	if _, err := driver.Read(guestID); err == nil {
		t.Fatal("pre-login session id still valid (fixation)")
	}
	if w := app.do("GET", "/admin", guard, ok); w.Code != 200 || w.Body.String() != "42" {
		t.Fatalf("logged in: %d %q", w.Code, w.Body.String())
	}

	// A second device, then log out the others from the first.
	other := &sessionApp{t: t, mgr: app.mgr}
	other.do("POST", "/login", func(c *context.Context) error { return Login(c, &SimpleUser{ID: "42"}) })
	if w := other.do("GET", "/admin", guard, ok); w.Code != 200 {
		t.Fatal("second device not logged in")
	}
	app.do("POST", "/password", func(c *context.Context) error { return LogoutOtherDevices(c) })
	if w := other.do("GET", "/admin", guard, ok); w.Code != http.StatusFound {
		t.Fatal("other device still logged in")
	}
	if w := app.do("GET", "/admin", guard, ok); w.Code != 200 {
		t.Fatal("current device logged out")
	}

	app.do("POST", "/logout", func(c *context.Context) error { return Logout(c) })
	if _, err := driver.Read(loggedID); err == nil {
		t.Fatal("logged-in session not destroyed on logout")
	}
	if w := app.do("GET", "/admin", guard, ok); w.Code != http.StatusFound {
		t.Fatal("still logged in after logout")
	}

	// JSON 401 without RedirectTo; no session middleware -> ErrNoSession.
	w := httptest.NewRecorder()
	c := context.NewContext()
	c.Reset(w, httptest.NewRequest("GET", "/", nil))
	if err := Login(c, &SimpleUser{ID: "1"}); !errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v", err)
	}
	if w := app.do("GET", "/api", SessionGuard(users), ok); w.Code != http.StatusUnauthorized {
		t.Fatalf("api guest: %d", w.Code)
	}
}
