package oauth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/session"
)

func TestBaseProvider_AuthURL(t *testing.T) {
	cfg := Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-secret",
		RedirectURL:  "https://app.com/auth/callback",
		Scopes:       []string{"read:user", "user:email"},
	}

	provider := NewBaseProvider(
		"test",
		cfg,
		"https://provider.com/oauth/authorize",
		"https://provider.com/oauth/token",
		"https://provider.com/oauth/user",
	)

	provider.With(map[string]string{
		"prompt": "consent",
	})

	authURL := provider.GetAuthURL("random-state-123")
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("failed to parse auth URL: %v", err)
	}

	q := u.Query()
	if q.Get("client_id") != "test-client-id" {
		t.Errorf("expected client_id test-client-id, got %s", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "https://app.com/auth/callback" {
		t.Errorf("expected redirect_uri, got %s", q.Get("redirect_uri"))
	}
	if q.Get("state") != "random-state-123" {
		t.Errorf("expected state random-state-123, got %s", q.Get("state"))
	}
	if q.Get("scope") != "read:user user:email" {
		t.Errorf("expected scope, got %s", q.Get("scope"))
	}
	if q.Get("prompt") != "consent" {
		t.Errorf("expected prompt consent, got %s", q.Get("prompt"))
	}
}

func TestBaseProvider_PKCE(t *testing.T) {
	cfg := Config{
		ClientID:    "pkce-client",
		RedirectURL: "https://app.com/callback",
	}

	provider := NewBaseProvider("pkce", cfg, "https://provider.com/auth", "https://provider.com/token", "")
	provider.WithPKCE()

	authURL := provider.GetAuthURL("state123")
	u, _ := url.Parse(authURL)
	q := u.Query()

	challenge := q.Get("code_challenge")
	method := q.Get("code_challenge_method")

	if challenge == "" {
		t.Fatal("expected non-empty code_challenge")
	}
	if method != "S256" {
		t.Fatalf("expected code_challenge_method S256, got %s", method)
	}
}

func TestBaseProvider_StateValidation(t *testing.T) {
	cfg := Config{ClientID: "cid", RedirectURL: "https://app.com/callback"}
	provider := NewBaseProvider("cookie_test", cfg, "https://provider.com/auth", "https://provider.com/token", "")

	// 1. Redirect sets state cookie
	req := httptest.NewRequest("GET", "/auth/redirect", nil)
	w := httptest.NewRecorder()
	c := context.NewContext()
	c.Reset(w, req)

	err := provider.Redirect(c)
	if err != nil {
		t.Fatalf("redirect failed: %v", err)
	}
	if w.Code != http.StatusFound {
		t.Fatalf("expected status 302, got %d", w.Code)
	}

	cookies := w.Result().Cookies()
	var stateCookie *http.Cookie
	for _, ck := range cookies {
		if ck.Name == "oauth_state_cookie_test" {
			stateCookie = ck
			break
		}
	}
	if stateCookie == nil || stateCookie.Value == "" {
		t.Fatal("expected oauth_state cookie to be set")
	}

	// 2. Callback with valid state
	cbReq := httptest.NewRequest("GET", "/auth/callback?code=valid_code&state="+stateCookie.Value, nil)
	cbReq.AddCookie(stateCookie)
	cbW := httptest.NewRecorder()
	cbCtx := context.NewContext()
	cbCtx.Reset(cbW, cbReq)

	code, err := provider.ValidateCallback(cbCtx)
	if err != nil || code != "valid_code" {
		t.Fatalf("expected valid code, got %s (err: %v)", code, err)
	}

	// 3. Callback with mismatched state should fail with ErrInvalidState
	badReq := httptest.NewRequest("GET", "/auth/callback?code=valid_code&state=wrong_state", nil)
	badReq.AddCookie(stateCookie)
	badW := httptest.NewRecorder()
	badCtx := context.NewContext()
	badCtx.Reset(badW, badReq)

	_, err = provider.ValidateCallback(badCtx)
	if err != ErrInvalidState {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}

	// 4. Stateless mode ignores state
	provider.Stateless()
	statelessReq := httptest.NewRequest("GET", "/auth/callback?code=mobile_code", nil)
	statelessCtx := context.NewContext()
	statelessCtx.Reset(httptest.NewRecorder(), statelessReq)

	code, err = provider.ValidateCallback(statelessCtx)
	if err != nil || code != "mobile_code" {
		t.Fatalf("stateless validation failed: %v", err)
	}
}

func TestBaseProvider_SessionStateValidation(t *testing.T) {
	cfg := Config{ClientID: "cid", RedirectURL: "https://app.com/callback"}
	provider := NewBaseProvider("session_test", cfg, "https://provider.com/auth", "https://provider.com/token", "")

	req := httptest.NewRequest("GET", "/auth/redirect", nil)
	w := httptest.NewRecorder()
	c := context.NewContext()
	c.Reset(w, req)

	// Attach active session
	sess := session.NewSession()
	c.Set(session.ContextSessionKey, sess)

	_ = provider.Redirect(c)

	storedState := sess.GetString("oauth_state_session_test")
	if storedState == "" {
		t.Fatal("expected state in session")
	}

	// Validate callback using same session
	cbReq := httptest.NewRequest("GET", "/auth/callback?code=sess_code&state="+storedState, nil)
	cbCtx := context.NewContext()
	cbCtx.Reset(httptest.NewRecorder(), cbReq)
	cbCtx.Set(session.ContextSessionKey, sess)

	code, err := provider.ValidateCallback(cbCtx)
	if err != nil || code != "sess_code" {
		t.Fatalf("expected code sess_code, got %s (err: %v)", code, err)
	}

	// State should have been pulled (cleared)
	if sess.Has("oauth_state_session_test") {
		t.Fatal("expected state to be cleared from session after validation")
	}
}

func TestGitHubProvider_FullFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Method != "POST" {
				http.Error(w, "bad method", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "gho_test_token_999",
				"token_type":   "bearer",
				"scope":        "read:user,user:email",
			})

		case "/user":
			auth := r.Header.Get("Authorization")
			if auth != "Bearer gho_test_token_999" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         1234567,
				"login":      "octocat",
				"name":       "The Octocat",
				"email":      nil, // Private email
				"avatar_url": "https://avatars.githubusercontent.com/u/1234567",
			})

		case "/user/emails":
			auth := r.Header.Get("Authorization")
			if auth != "Bearer gho_test_token_999" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"email": "secondary@example.com", "primary": false, "verified": true},
				{"email": "octocat@github.com", "primary": true, "verified": true},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := Config{
		ClientID:     "gh_client",
		ClientSecret: "gh_secret",
		RedirectURL:  "https://app.com/auth/github/callback",
	}

	gh := NewGitHubProvider(cfg)
	gh.authURL = server.URL + "/login/oauth/authorize"
	gh.tokenURL = server.URL + "/login/oauth/access_token"
	gh.userURL = server.URL + "/user"
	gh.emailsURL = server.URL + "/user/emails"
	gh.httpClient = server.Client()
	gh.Stateless()

	req := httptest.NewRequest("GET", "/auth/github/callback?code=gh_auth_code", nil)
	c := context.NewContext()
	c.Reset(httptest.NewRecorder(), req)

	user, err := gh.User(c)
	if err != nil {
		t.Fatalf("GitHub User() failed: %v", err)
	}

	if user.ID != "1234567" {
		t.Errorf("expected ID 1234567, got %s", user.ID)
	}
	if user.Nickname != "octocat" {
		t.Errorf("expected Nickname octocat, got %s", user.Nickname)
	}
	if user.Name != "The Octocat" {
		t.Errorf("expected Name The Octocat, got %s", user.Name)
	}
	if user.Email != "octocat@github.com" {
		t.Errorf("expected Email octocat@github.com, got %s", user.Email)
	}
	if user.Avatar != "https://avatars.githubusercontent.com/u/1234567" {
		t.Errorf("expected Avatar URL, got %s", user.Avatar)
	}
	if user.Token != "gho_test_token_999" {
		t.Errorf("expected Token gho_test_token_999, got %s", user.Token)
	}
	if user.GetAuthIdentifier() != "1234567" {
		t.Errorf("expected GetAuthIdentifier 1234567, got %s", user.GetAuthIdentifier())
	}
}

func TestGoogleProvider_FullFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "google_token_123",
				"refresh_token": "google_refresh_456",
				"expires_in":    3600,
				"token_type":    "Bearer",
			})
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":        "google_uid_99",
				"name":       "Larry Page",
				"given_name": "Larry",
				"email":      "larry@google.com",
				"picture":    "https://google.com/photo.jpg",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := Config{ClientID: "gid", ClientSecret: "gsecret", RedirectURL: "https://app.com/callback"}
	google := NewGoogleProvider(cfg)
	google.tokenURL = server.URL + "/token"
	google.userURL = server.URL + "/userinfo"
	google.httpClient = server.Client()
	google.Stateless()

	req := httptest.NewRequest("GET", "/auth/google/callback?code=google_code", nil)
	c := context.NewContext()
	c.Reset(httptest.NewRecorder(), req)

	user, err := google.User(c)
	if err != nil {
		t.Fatalf("Google User() failed: %v", err)
	}

	if user.ID != "google_uid_99" {
		t.Errorf("expected ID google_uid_99, got %s", user.ID)
	}
	if user.Name != "Larry Page" {
		t.Errorf("expected Name Larry Page, got %s", user.Name)
	}
	if user.Nickname != "Larry" {
		t.Errorf("expected Nickname Larry, got %s", user.Nickname)
	}
	if user.Email != "larry@google.com" {
		t.Errorf("expected Email larry@google.com, got %s", user.Email)
	}
	if user.Avatar != "https://google.com/photo.jpg" {
		t.Errorf("expected Avatar URL, got %s", user.Avatar)
	}
	if user.RefreshToken != "google_refresh_456" {
		t.Errorf("expected RefreshToken google_refresh_456, got %s", user.RefreshToken)
	}
	if user.ExpiresIn != 3600 {
		t.Errorf("expected ExpiresIn 3600, got %d", user.ExpiresIn)
	}
}

func TestDiscordProvider_FullFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "discord_tok_555",
			})
		case "/users/@me":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "80351110224678912",
				"username":    "wumpus",
				"global_name": "Wumpus The Great",
				"email":       "wumpus@discord.gg",
				"avatar":      "83427290963482",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := Config{ClientID: "did", ClientSecret: "dsec", RedirectURL: "https://app.com/callback"}
	discord := NewDiscordProvider(cfg)
	discord.tokenURL = server.URL + "/token"
	discord.userURL = server.URL + "/users/@me"
	discord.httpClient = server.Client()
	discord.Stateless()

	req := httptest.NewRequest("GET", "/auth/discord/callback?code=disc_code", nil)
	c := context.NewContext()
	c.Reset(httptest.NewRecorder(), req)

	user, err := discord.User(c)
	if err != nil {
		t.Fatalf("Discord User() failed: %v", err)
	}

	if user.ID != "80351110224678912" {
		t.Errorf("expected ID 80351110224678912, got %s", user.ID)
	}
	if user.Nickname != "wumpus" {
		t.Errorf("expected Nickname wumpus, got %s", user.Nickname)
	}
	if user.Name != "Wumpus The Great" {
		t.Errorf("expected Name Wumpus The Great, got %s", user.Name)
	}
	if user.Avatar != "https://cdn.discordapp.com/avatars/80351110224678912/83427290963482.png" {
		t.Errorf("expected avatar CDN URL, got %s", user.Avatar)
	}
}

func TestGenericOAuth2Provider_CustomMapperAndRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			grant := r.FormValue("grant_type")
			if grant == "refresh_token" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "new_refreshed_token",
					"refresh_token": "new_refresh_token_2",
					"expires_in":    7200,
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "generic_tok",
			})
		case "/userinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":   "custom_999",
				"fname": "Ada",
				"lname": "Lovelace",
				"mail":  "ada@computing.org",
			})
		}
	}))
	defer server.Close()

	cfg := Config{ClientID: "cid", RedirectURL: "https://app.com/cb"}
	gen := NewGenericOAuth2Provider("custom", cfg, server.URL+"/auth", server.URL+"/token", server.URL+"/userinfo")
	gen.httpClient = server.Client()

	// Custom mapper
	gen.WithUserMapper(func(raw map[string]any, token *Token) (*User, error) {
		return &User{
			ID:    fmt.Sprint(raw["sub"]),
			Name:  fmt.Sprintf("%s %s", raw["fname"], raw["lname"]),
			Email: fmt.Sprint(raw["mail"]),
		}, nil
	})

	user, err := gen.UserFromToken("generic_tok")
	if err != nil {
		t.Fatalf("UserFromToken failed: %v", err)
	}

	if user.ID != "custom_999" || user.Name != "Ada Lovelace" || user.Email != "ada@computing.org" {
		t.Fatalf("custom mapping failed, got: %+v", user)
	}

	// Test RefreshToken
	refreshed, err := gen.RefreshToken("old_refresh")
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}
	if refreshed.AccessToken != "new_refreshed_token" {
		t.Fatalf("expected new_refreshed_token, got %s", refreshed.AccessToken)
	}
}

func TestOAuthManager_RegistrationAndEnv(t *testing.T) {
	mgr := NewManager()

	os.Setenv("GITHUB_CLIENT_ID", "env_gh_id")
	os.Setenv("GITHUB_CLIENT_SECRET", "env_gh_secret")
	os.Setenv("GITHUB_REDIRECT_URL", "https://app.com/gh/cb")
	defer func() {
		os.Unsetenv("GITHUB_CLIENT_ID")
		os.Unsetenv("GITHUB_CLIENT_SECRET")
		os.Unsetenv("GITHUB_REDIRECT_URL")
	}()

	gh := mgr.Driver("github")
	if gh.Name() != "github" {
		t.Fatalf("expected driver github, got %s", gh.Name())
	}

	// Custom provider registration
	mgr.Register("gitlab", func(cfg Config) Provider {
		return NewGenericOAuth2Provider("gitlab", cfg, "https://gitlab.com/auth", "https://gitlab.com/token", "https://gitlab.com/user")
	})

	gl := mgr.Driver("gitlab")
	if gl.Name() != "gitlab" {
		t.Fatalf("expected driver gitlab, got %s", gl.Name())
	}

	// Unregistered should panic
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unregistered driver")
		}
	}()
	mgr.Driver("nonexistent")
}

func TestMountRoutes_Integration(t *testing.T) {
	app := golaravel.New()

	var authenticatedUser *User
	MountRoutes(app, "github", func(c *context.Context, user *User) error {
		authenticatedUser = user
		_ = authenticatedUser
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// 1. GET /auth/github/redirect
	req := httptest.NewRequest("GET", "/auth/github/redirect", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 Found, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "github.com/login/oauth/authorize") {
		t.Fatalf("expected GitHub authorize URL in Location, got %s", loc)
	}

	// 2. GET /auth/github/callback with missing code should return 400
	cbReq := httptest.NewRequest("GET", "/auth/github/callback", nil)
	cbW := httptest.NewRecorder()
	app.ServeHTTP(cbW, cbReq)

	if cbW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on invalid callback, got %d", cbW.Code)
	}
}
