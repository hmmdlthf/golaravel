package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/session"
)

// Provider defines the interface for OAuth2 authentication providers.
type Provider interface {
	Name() string
	Redirect(c *context.Context) error
	GetAuthURL(state string) string
	User(c *context.Context) (*User, error)
	UserFromToken(token string) (*User, error)
	RefreshToken(refreshToken string) (*Token, error)
	Scopes(scopes ...string) Provider
	With(params map[string]string) Provider
	Stateless() Provider
	WithPKCE() Provider
	SetHTTPClient(client *http.Client) Provider
}

// UserMapperFunc defines a custom transformation function from raw API data to User.
type UserMapperFunc func(raw map[string]any, token *Token) (*User, error)

// BaseProvider encapsulates shared OAuth2 flow mechanics (state, PKCE, token exchange).
type BaseProvider struct {
	name         string
	config       Config
	scopes       []string
	customParams map[string]string
	isStateless  bool
	usePKCE      bool
	httpClient   *http.Client
	authURL      string
	tokenURL     string
	userURL      string
	codeVerifier string
	userMapper   UserMapperFunc
}

// NewBaseProvider initializes a BaseProvider with sensible defaults.
func NewBaseProvider(name string, cfg Config, authURL, tokenURL, userURL string) BaseProvider {
	return BaseProvider{
		name:         name,
		config:       cfg,
		scopes:       cfg.Scopes,
		customParams: make(map[string]string),
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		authURL:  authURL,
		tokenURL: tokenURL,
		userURL:  userURL,
	}
}

// Name returns the provider driver name.
func (b *BaseProvider) Name() string {
	return b.name
}

// Scopes sets or overrides the requested authorization scopes.
func (b *BaseProvider) Scopes(scopes ...string) Provider {
	b.scopes = scopes
	return b
}

// With adds arbitrary query parameters to the authorization URL.
func (b *BaseProvider) With(params map[string]string) Provider {
	for k, v := range params {
		b.customParams[k] = v
	}
	return b
}

// Stateless disables state CSRF verification (ideal for mobile/Flutter clients).
func (b *BaseProvider) Stateless() Provider {
	b.isStateless = true
	return b
}

// WithPKCE enables RFC 7636 Proof Key for Code Exchange (S256).
func (b *BaseProvider) WithPKCE() Provider {
	b.usePKCE = true
	return b
}

// SetHTTPClient configures a custom HTTP client (useful for unit tests or proxies).
func (b *BaseProvider) SetHTTPClient(client *http.Client) Provider {
	if client != nil {
		b.httpClient = client
	}
	return b
}

// User must be implemented by concrete providers.
func (b *BaseProvider) User(c *context.Context) (*User, error) {
	return nil, fmt.Errorf("oauth: User() must be implemented by concrete provider %s", b.name)
}

// UserFromToken must be implemented by concrete providers.
func (b *BaseProvider) UserFromToken(token string) (*User, error) {
	return nil, fmt.Errorf("oauth: UserFromToken() must be implemented by concrete provider %s", b.name)
}

// Redirect initiates the OAuth2 redirect to the provider's authorization page.
func (b *BaseProvider) Redirect(c *context.Context) error {
	state := b.generateState()

	if !b.isStateless {
		b.storeState(c, state)
	}

	var codeVerifier string
	if b.usePKCE {
		codeVerifier = b.generateCodeVerifier()
		b.codeVerifier = codeVerifier
		b.storeCodeVerifier(c, codeVerifier)
	}

	authURL := b.buildAuthURL(state, codeVerifier)
	return c.Redirect(http.StatusFound, authURL)
}

// GetAuthURL generates the full authorization URL for a given state.
func (b *BaseProvider) GetAuthURL(state string) string {
	var verifier string
	if b.usePKCE {
		verifier = b.generateCodeVerifier()
		b.codeVerifier = verifier
	}
	return b.buildAuthURL(state, verifier)
}

func (b *BaseProvider) buildAuthURL(state, codeVerifier string) string {
	u, err := url.Parse(b.authURL)
	if err != nil {
		return b.authURL
	}

	q := u.Query()
	q.Set("client_id", b.config.ClientID)
	q.Set("redirect_uri", b.config.RedirectURL)
	q.Set("response_type", "code")

	if len(b.scopes) > 0 {
		q.Set("scope", strings.Join(b.scopes, " "))
	}

	if state != "" {
		q.Set("state", state)
	}

	if b.usePKCE && codeVerifier != "" {
		h := sha256.Sum256([]byte(codeVerifier))
		challenge := base64.RawURLEncoding.EncodeToString(h[:])
		q.Set("code_challenge", challenge)
		q.Set("code_challenge_method", "S256")
	}

	for k, v := range b.customParams {
		q.Set(k, v)
	}

	u.RawQuery = q.Encode()
	return u.String()
}

// ValidateCallback validates the callback state and extracts the authorization code.
func (b *BaseProvider) ValidateCallback(c *context.Context) (string, error) {
	if !b.isStateless {
		reqState := c.Query("state")
		if reqState == "" {
			return "", ErrInvalidState
		}

		expectedState := b.retrieveState(c)
		if expectedState == "" || expectedState != reqState {
			return "", ErrInvalidState
		}
	}

	code := c.Query("code")
	if code == "" {
		return "", ErrMissingCode
	}

	return code, nil
}

// ExchangeCode exchanges an authorization code for an OAuth2 token.
func (b *BaseProvider) ExchangeCode(c *context.Context, code string) (*Token, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {b.config.ClientID},
		"client_secret": {b.config.ClientSecret},
		"code":          {code},
		"redirect_uri":  {b.config.RedirectURL},
	}

	if b.usePKCE {
		verifier := b.codeVerifier
		if verifier == "" && c != nil {
			verifier = b.retrieveCodeVerifier(c)
		}
		if verifier != "" {
			data.Set("code_verifier", verifier)
		}
	}

	req, err := http.NewRequest("POST", b.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenExchange, err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenExchange, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenExchange, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d, body: %s", ErrTokenExchange, resp.StatusCode, string(body))
	}

	// Try parsing JSON first
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err == nil {
		token := &Token{
			Raw: raw,
		}
		if at, ok := raw["access_token"].(string); ok {
			token.AccessToken = at
		}
		if tt, ok := raw["token_type"].(string); ok {
			token.TokenType = tt
		}
		if rt, ok := raw["refresh_token"].(string); ok {
			token.RefreshToken = rt
		}
		if sc, ok := raw["scope"].(string); ok {
			token.Scope = sc
		}
		if exp, ok := raw["expires_in"].(float64); ok {
			token.ExpiresIn = int64(exp)
		}
		if token.AccessToken != "" {
			return token, nil
		}
	}

	// Fallback to URL-encoded form parsing
	values, err := url.ParseQuery(string(body))
	if err == nil && values.Get("access_token") != "" {
		token := &Token{
			AccessToken:  values.Get("access_token"),
			TokenType:    values.Get("token_type"),
			RefreshToken: values.Get("refresh_token"),
			Scope:        values.Get("scope"),
			Raw:          make(map[string]any),
		}
		for k, v := range values {
			if len(v) > 0 {
				token.Raw[k] = v[0]
			}
		}
		return token, nil
	}

	return nil, fmt.Errorf("%w: could not parse token response: %s", ErrTokenExchange, string(body))
}

// RefreshToken exchanges a refresh token for a new access token.
func (b *BaseProvider) RefreshToken(refreshToken string) (*Token, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {b.config.ClientID},
		"client_secret": {b.config.ClientSecret},
		"refresh_token": {refreshToken},
	}

	req, err := http.NewRequest("POST", b.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenExchange, err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenExchange, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenExchange, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d, body: %s", ErrTokenExchange, resp.StatusCode, string(body))
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenExchange, err)
	}

	token := &Token{Raw: raw}
	if at, ok := raw["access_token"].(string); ok {
		token.AccessToken = at
	}
	if rt, ok := raw["refresh_token"].(string); ok {
		token.RefreshToken = rt
	}
	if exp, ok := raw["expires_in"].(float64); ok {
		token.ExpiresIn = int64(exp)
	}

	return token, nil
}

// FetchRawUserInfo fetches raw JSON from the provider's userinfo endpoint.
func (b *BaseProvider) FetchRawUserInfo(token string) (map[string]any, error) {
	req, err := http.NewRequest("GET", b.userURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserInfo, err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GoLaravel-Socialite/1.0")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserInfo, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: status %d, body: %s", ErrUserInfo, resp.StatusCode, string(body))
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: failed to decode user info: %v", ErrUserInfo, err)
	}

	return raw, nil
}

// State & PKCE internal helpers

func (b *BaseProvider) generateState() string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func (b *BaseProvider) generateCodeVerifier() string {
	bytes := make([]byte, 32)
	_, _ = rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}

func (b *BaseProvider) stateKey() string {
	return "oauth_state_" + b.name
}

func (b *BaseProvider) verifierKey() string {
	return "oauth_verifier_" + b.name
}

func (b *BaseProvider) storeState(c *context.Context, state string) {
	if session.FromContext(c) != nil {
		session.Put(c, b.stateKey(), state)
	} else {
		c.SetCookie(b.stateKey(), state, 300, "/", "", false, true)
	}
}

func (b *BaseProvider) retrieveState(c *context.Context) string {
	if sess := session.FromContext(c); sess != nil {
		val := sess.Pull(b.stateKey())
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}

	val, err := c.Cookie(b.stateKey())
	if err == nil {
		c.SetCookie(b.stateKey(), "", -1, "/", "", false, true)
		return val
	}
	return ""
}

func (b *BaseProvider) storeCodeVerifier(c *context.Context, verifier string) {
	if session.FromContext(c) != nil {
		session.Put(c, b.verifierKey(), verifier)
	} else {
		c.SetCookie(b.verifierKey(), verifier, 300, "/", "", false, true)
	}
}

func (b *BaseProvider) retrieveCodeVerifier(c *context.Context) string {
	if sess := session.FromContext(c); sess != nil {
		val := sess.Pull(b.verifierKey())
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}

	val, err := c.Cookie(b.verifierKey())
	if err == nil {
		c.SetCookie(b.verifierKey(), "", -1, "/", "", false, true)
		return val
	}
	return ""
}
